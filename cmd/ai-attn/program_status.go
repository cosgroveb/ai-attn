package main

import (
	"encoding/base64"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strconv"
	"strings"
)

// programStatus maps an ai-attn state and reason to the body of an OSC 7501
// program status report
// (https://www.superlogical.com/rex/docs/build/program-status). The message
// is a fixed phrase per state, so prompts, commands and paths from hook
// payloads never reach status lines or notifications.
func programStatus(state, reason string) (string, bool) {
	switch state {
	case "working":
		return "state=working:" + msg("Working"), true
	case "waiting":
		lower := strings.ToLower(reason)
		switch {
		case strings.Contains(lower, "permission"):
			return "state=blocked:kind=permission:" + msg("Needs permission"), true
		case strings.Contains(lower, "elicitation"):
			return "state=blocked:kind=question:" + msg("Has a question"), true
		}
		return "state=blocked:" + msg("Waiting for input"), true
	case "done":
		return "state=done:" + msg("Turn complete"), true
	case "stopped":
		// A failed tool call does not end the turn; the agent keeps going.
		if reason == "PostToolUseFailure" {
			return "state=working:" + msg("Working"), true
		}
		return "state=error:" + msg("Failed"), true
	}
	return "", false
}

// msg returns the msg field for phrase. OSC 7501 carries msg as base64, and
// terminals drop a report whose msg does not decode.
func msg(phrase string) string {
	return "msg=" + base64.StdEncoding.EncodeToString([]byte(phrase))
}

// terminalPath returns the controlling terminal of the nearest process,
// starting with this one, that has one. Claude Code runs hooks without a
// terminal, but the agent process that started them still has the pane's,
// so walking parents is the one lookup that works for every agent. ps reads
// the same way on macOS and Linux.
var terminalPath = func() string {
	pid := os.Getpid()
	for range 16 {
		out, err := exec.Command("ps", "-o", "ppid=,tty=", "-p", strconv.Itoa(pid)).Output()
		if err != nil {
			return ""
		}
		fields := strings.Fields(string(out))
		if len(fields) == 0 {
			return ""
		}
		if len(fields) == 2 && fields[1] != "?" && fields[1] != "??" {
			return "/dev/" + fields[1]
		}
		pid, err = strconv.Atoi(fields[0])
		if err != nil || pid <= 1 {
			return ""
		}
	}
	return ""
}

// emitProgramStatus writes an OSC 7501 report for agent to the agent's
// terminal, where the terminal or tmux shows it against the agent's pane.
// Hooks with no terminal anywhere above them skip the report.
func emitProgramStatus(agent, body string, stderr io.Writer) {
	path := terminalPath()
	if path == "" {
		return
	}
	tty, err := os.OpenFile(path, os.O_WRONLY, 0)
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
