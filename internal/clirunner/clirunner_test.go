//go:build darwin && (amd64 || arm64)

package clirunner

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestRunCapturesOutputAndExit(t *testing.T) {
	res, err := Run(context.Background(), []string{"sh", "-c", "echo out; echo err >&2; exit 3"}, Options{})
	if err == nil {
		t.Fatal("a non-zero exit must be an error")
	}
	if res.ExitCode != 3 || res.Output() != "out" || !strings.Contains(res.Stderr, "err") {
		t.Fatalf("result = %+v", res)
	}
}

func TestRunTimesOut(t *testing.T) {
	_, err := Run(context.Background(), []string{"sleep", "5"}, Options{Timeout: 100 * time.Millisecond})
	if !errors.Is(err, ErrTimeout) {
		t.Fatalf("want ErrTimeout, got %v", err)
	}
}

func TestEnvironmentWithholdsSecrets(t *testing.T) {
	t.Setenv("MACOS_MCP_AUDIT_KEY", "secret")
	t.Setenv("HOME", "/Users/test")
	for _, kv := range Environment() {
		if strings.HasPrefix(kv, SecretPrefix) {
			t.Fatalf("secret leaked into the child environment: %s", kv)
		}
	}
	res, err := Run(context.Background(), []string{"sh", "-c", "echo ${MACOS_MCP_AUDIT_KEY:-unset}"}, Options{})
	if err != nil || res.Output() != "unset" {
		t.Fatalf("child saw the secret: %+v %v", res, err)
	}
}

func TestSearchPathPutsSystemDirsFirst(t *testing.T) {
	p := SearchPath()
	if len(p) < 4 || p[0] != "/usr/bin" || p[1] != "/bin" || p[2] != "/usr/sbin" || p[3] != "/sbin" {
		t.Fatalf("system directories must lead the search path: %v", p)
	}
}

func TestRunScriptRejectsUnknownInterpreter(t *testing.T) {
	if _, err := RunScript(context.Background(), "python", "print(1)", Options{}); err == nil {
		t.Fatal("an interpreter outside the closed set must be refused")
	}
	res, err := RunScript(context.Background(), "zsh", "echo $((6*7))", Options{})
	if err != nil || res.Output() != "42" {
		t.Fatalf("zsh script: %+v %v", res, err)
	}
}

func TestRunRejectsEmptyArgv(t *testing.T) {
	if _, err := Run(context.Background(), nil, Options{}); err == nil {
		t.Fatal("empty argv must be refused")
	}
}
