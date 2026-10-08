//go:build darwin && (amd64 || arm64)

// Package clirunner runs the command-line tools the server leans on where the
// SDK has no binding (launchctl, log, profiles, pfctl, networksetup, osascript,
// codesign, ...). It exists so every such call shares three properties:
//
//   - argv only, never a shell. A command is an executable and its arguments;
//     nothing here builds a command line a shell would interpret, so there are
//     no quoting concerns and no injection surface. The one deliberate exception
//     is the Shell tool, which calls RunScript with the interpreter named.
//   - a rebuilt environment. MCP hosts strip the environment they hand a stdio
//     server, so PATH is reconstructed from the system's own path files and the
//     Homebrew prefix, and every variable carrying one of this server's secrets
//     is withheld from the child.
//   - a timeout. Every call is bounded; a hung tool must not hang a session.
package clirunner

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// DefaultTimeout bounds a call whose caller did not choose one.
const DefaultTimeout = 30 * time.Second

// SecretPrefix is the environment-variable prefix withheld from every child
// process. It is the first of two defences; the runtime's ScrubSecretEnv is the
// second.
const SecretPrefix = "MACOS_MCP_" //nolint:gosec // G101: a variable-name prefix, not a credential

// Errors the runner returns. They are static so callers can match them.
var (
	ErrTimeout            = errors.New("command timed out")
	ErrEmptyArgv          = errors.New("clirunner: empty argv")
	ErrUnknownInterpreter = errors.New("clirunner: unsupported interpreter")
	ErrNotFound           = errors.New("clirunner: executable not found on the rebuilt PATH")
)

// Options tunes one call.
type Options struct {
	// Timeout bounds the call. Zero means DefaultTimeout.
	Timeout time.Duration
	// Dir is the working directory. Empty means inherit.
	Dir string
	// Stdin is fed to the child.
	Stdin []byte
	// Env adds variables on top of the rebuilt environment.
	Env []string
}

// Result is what a command produced.
type Result struct {
	Stdout   string
	Stderr   string
	ExitCode int
	Duration time.Duration
}

// Output is Stdout with trailing whitespace trimmed, which is what callers
// parsing a single value usually want.
func (r Result) Output() string { return strings.TrimRight(r.Stdout, "\r\n\t ") }

// Run executes argv[0] with argv[1:] and returns what it produced. A non-zero
// exit is returned as both a populated Result and an error, so a caller can
// read stderr for the reason. The executable is resolved against the rebuilt
// PATH, never the inherited one.
func Run(ctx context.Context, argv []string, opts Options) (Result, error) {
	if len(argv) == 0 {
		return Result{}, ErrEmptyArgv
	}
	exe, err := LookPath(argv[0])
	if err != nil {
		return Result{}, err
	}
	return run(ctx, exe, argv[1:], opts)
}

// RunScript feeds script to interpreter on stdin. It is the one path that hands
// text to a shell, and it exists only for the Shell tool: the interpreter is
// named by the caller from a closed set, the script is never spliced into a
// command line, and rc files are not read.
func RunScript(ctx context.Context, interpreter string, script string, opts Options) (Result, error) {
	var args []string
	switch interpreter {
	case "zsh":
		args = []string{"-f", "-s"} // -f: no rc files; -s: read commands from stdin
	case "bash":
		args = []string{"--noprofile", "--norc", "-s"}
	case "osascript":
		args = []string{"-"} // AppleScript from stdin
	default:
		return Result{}, fmt.Errorf("%w: %q", ErrUnknownInterpreter, interpreter)
	}
	exe, err := LookPath(interpreter)
	if err != nil {
		return Result{}, err
	}
	opts.Stdin = []byte(script)
	return run(ctx, exe, args, opts)
}

func run(ctx context.Context, exe string, args []string, opts Options) (Result, error) {
	timeout := opts.Timeout
	if timeout <= 0 {
		timeout = DefaultTimeout
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, exe, args...) //nolint:gosec // argv-only by construction; see package doc
	cmd.Env = append(Environment(), opts.Env...)
	cmd.Dir = opts.Dir
	if opts.Stdin != nil {
		cmd.Stdin = bytes.NewReader(opts.Stdin)
	}
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr

	started := time.Now()
	err := cmd.Run()
	res := Result{Stdout: stdout.String(), Stderr: stderr.String(), Duration: time.Since(started)}
	if cmd.ProcessState != nil {
		res.ExitCode = cmd.ProcessState.ExitCode()
	}
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return res, fmt.Errorf("%w after %s: %s", ErrTimeout, timeout, filepath.Base(exe))
	}
	if err != nil {
		return res, fmt.Errorf("%s: %w: %s", filepath.Base(exe), err, strings.TrimSpace(stderr.String()))
	}
	return res, nil
}

// LookPath resolves an executable against the rebuilt PATH. An absolute path is
// accepted as given.
func LookPath(name string) (string, error) {
	if filepath.IsAbs(name) {
		if _, err := os.Stat(name); err != nil {
			return "", fmt.Errorf("clirunner: %w", err)
		}
		return name, nil
	}
	for _, dir := range SearchPath() {
		candidate := filepath.Join(dir, name)
		if info, err := os.Stat(candidate); err == nil && !info.IsDir() && info.Mode()&0o111 != 0 {
			return candidate, nil
		}
	}
	return "", fmt.Errorf("%w: %q", ErrNotFound, name)
}

// systemPathFiles are what path_helper(8) reads: one directory per line.
var systemPathFiles = []string{"/etc/paths"}

// fallbackPath is the PATH a Mac has with nothing else configured, plus the
// two Homebrew prefixes. Order matters: the system tools this server calls by
// name (launchctl, log, pfctl) must come from /usr/sbin and /usr/bin, not from
// anything a package manager installed over them.
var fallbackPath = []string{
	"/usr/bin", "/bin", "/usr/sbin", "/sbin",
	"/opt/homebrew/bin", "/usr/local/bin",
}

// SearchPath is the directory list the runner resolves executables against.
// It is built from /etc/paths and /etc/paths.d (what the login shell would
// get) merged with the fallback, with the system directories first.
func SearchPath() []string {
	seen := map[string]bool{}
	var out []string
	add := func(dir string) {
		dir = strings.TrimSpace(dir)
		if dir == "" || seen[dir] {
			return
		}
		seen[dir] = true
		out = append(out, dir)
	}
	for _, dir := range fallbackPath[:4] {
		add(dir)
	}
	for _, f := range systemPathFiles {
		for _, line := range readLines(f) {
			add(line)
		}
	}
	if entries, err := os.ReadDir("/etc/paths.d"); err == nil {
		for _, e := range entries {
			for _, line := range readLines(filepath.Join("/etc/paths.d", e.Name())) {
				add(line)
			}
		}
	}
	for _, dir := range fallbackPath[4:] {
		add(dir)
	}
	return out
}

// Environment is the environment every child gets: the rebuilt PATH, the
// locale and home the tools expect, and nothing carrying one of this server's
// secrets. The inherited environment is consulted only for HOME, USER, TMPDIR
// and LANG.
func Environment() []string {
	env := []string{
		"PATH=" + strings.Join(SearchPath(), ":"),
		"LC_ALL=en_US.UTF-8",
	}
	for _, key := range []string{"HOME", "USER", "LOGNAME", "TMPDIR", "LANG", "SHELL"} {
		if v, ok := os.LookupEnv(key); ok && !strings.HasPrefix(key, SecretPrefix) {
			env = append(env, key+"="+v)
		}
	}
	return env
}

func readLines(path string) []string {
	raw, err := os.ReadFile(path) //nolint:gosec // a fixed system path
	if err != nil {
		return nil
	}
	return strings.Split(string(raw), "\n")
}
