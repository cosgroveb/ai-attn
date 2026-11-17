package main

import (
	"fmt"
	"io"
	"os"
	"strings"
)

// ttyPath is where OSC 7501 reports are written. Tests point it at a file.
var ttyPath = "/dev/tty"

// programStatus maps an ai-attn state and reason to the body of an OSC 7501
// program status report
// (https://www.superlogical.com/rex/docs/build/program-status).
func programStatus(state, reason string) (string, bool) {
	switch state {
	case "working":
		return "state=working", true
	case "waiting":
		lower := strings.ToLower(reason)
		switch {
		case strings.Contains(lower, "permission"):
			return "state=blocked:kind=permission", true
		case strings.Contains(lower, "elicitation"):
			return "state=blocked:kind=question", true
		}
		return "state=blocked", true
	case "done":
		return "state=done", true
	case "stopped":
		// A failed tool call does not end the turn; the agent keeps going.
		if reason == "PostToolUseFailure" {
			return "state=working", true
		}
		return "state=error", true
	}
	return "", false
}

// emitProgramStatus writes an OSC 7501 report for agent to the controlling
// terminal, where the terminal or tmux shows it against the agent's pane.
// Hooks run without a controlling terminal skip the report.
func emitProgramStatus(agent, body string, stderr io.Writer) {
	tty, err := os.OpenFile(ttyPath, os.O_WRONLY, 0)
	if err != nil {
		return
	}
	defer tty.Close()
	// One write, so the report cannot interleave with other output mid-sequence.
	report := fmt.Sprintf("\x1b]7501;%s:app=%s\x1b\\", body, agent)
	if _, err := io.WriteString(tty, report); err != nil {
		fmt.Fprintf(stderr, "ai-attn: warning: failed to write program status: %v\n", err)
	}
}
