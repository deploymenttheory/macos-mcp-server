//go:build darwin && (amd64 || arm64)

package macdesktop

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/deploymenttheory/macos-mcp-server/internal/clirunner"
)

// LogQuery selects unified-log entries.
type LogQuery struct {
	Subsystem string
	Process   string
	Level     string // default | info | debug | error | fault
	Hours     int
	Max       int
	Contains  string
}

// LogEntry is one unified-log record, reduced to what a diagnosis reads.
type LogEntry struct {
	Time      string `json:"time"`
	Type      string `json:"type"`
	Subsystem string `json:"subsystem,omitempty"`
	Category  string `json:"category,omitempty"`
	Process   string `json:"process"`
	PID       int    `json:"pid"`
	Message   string `json:"message"`
}

// QueryLog runs `log show` and reduces its ndjson output. The system log
// store is not readable through OSLogStore without root or a private
// entitlement; `log show` is the one path every user has.
func (d *Desktop) QueryLog(ctx context.Context, q LogQuery) ([]LogEntry, error) {
	argv := []string{"log", "show", "--style", "ndjson", "--last", fmt.Sprintf("%dh", q.Hours), "--info"}
	if q.Level == "debug" {
		argv = append(argv, "--debug")
	}
	if pred := LogPredicate(q); pred != "" {
		argv = append(argv, "--predicate", pred)
	}
	res, err := clirunner.Run(ctx, argv, clirunner.Options{Timeout: 90 * time.Second})
	if err != nil {
		return nil, fmt.Errorf("log show: %w", err)
	}
	return ParseLogNDJSON(res.Stdout, q.Max), nil
}

// LogPredicate builds the `log show --predicate` expression. Values are
// quoted as predicate string literals, so a subsystem or process name cannot
// become an operator.
func LogPredicate(q LogQuery) string {
	var parts []string
	if q.Subsystem != "" {
		parts = append(parts, "subsystem == "+predicateString(q.Subsystem))
	}
	if q.Process != "" {
		parts = append(parts, "process == "+predicateString(q.Process))
	}
	switch q.Level {
	case "error":
		parts = append(parts, "messageType == error")
	case "fault":
		parts = append(parts, "messageType == fault")
	case "info":
		parts = append(parts, "messageType == info")
	case "default":
		parts = append(parts, "messageType == default")
	}
	if q.Contains != "" {
		parts = append(parts, "eventMessage CONTAINS[c] "+predicateString(q.Contains))
	}
	return strings.Join(parts, " AND ")
}

func predicateString(s string) string {
	r := strings.NewReplacer(`\`, `\\`, `"`, `\"`)
	return `"` + r.Replace(s) + `"`
}

// ParseLogNDJSON reduces `log show --style ndjson` output to entries, newest
// last, capped at max.
func ParseLogNDJSON(out string, max int) []LogEntry {
	var entries []LogEntry
	sc := bufio.NewScanner(strings.NewReader(out))
	sc.Buffer(make([]byte, 0, 1<<20), 1<<20)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if !strings.HasPrefix(line, "{") {
			continue
		}
		var raw struct { //nolint:tagliatelle // the field names are Apple's ndjson schema
			Timestamp    string `json:"timestamp"`
			MessageType  string `json:"messageType"`
			Subsystem    string `json:"subsystem"`
			Category     string `json:"category"`
			ProcessImage string `json:"processImagePath"`
			Process      string `json:"process"`
			PID          int    `json:"processID"`
			Message      string `json:"eventMessage"`
		}
		if err := json.Unmarshal([]byte(line), &raw); err != nil {
			continue
		}
		proc := raw.Process
		if proc == "" && raw.ProcessImage != "" {
			proc = raw.ProcessImage[strings.LastIndex(raw.ProcessImage, "/")+1:]
		}
		entries = append(entries, LogEntry{
			Time: raw.Timestamp, Type: raw.MessageType, Subsystem: raw.Subsystem, Category: raw.Category,
			Process: proc, PID: raw.PID, Message: strings.Join(strings.Fields(raw.Message), " "),
		})
	}
	if max > 0 && len(entries) > max {
		entries = entries[len(entries)-max:]
	}
	return entries
}
