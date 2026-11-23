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

// nativeOSC7501Agents are agents that report OSC 7501 program status
// themselves in current releases. ai-attn leaves them out of the default
// osc7501_agents so their own reports aren't overwritten by a second one.
var nativeOSC7501Agents = map[string]bool{
	"claude":   true, // Claude Code 2.1.295 (CHANGELOG)
	"codex":    true, // Codex rust-v0.163.0 (codex-rs/tui/src/terminal_program_status.rs)
	"opencode": true, // OpenCode v2.1.0 (packages/tui/src/context/program-status.ts)
	"agy":      true, // Antigravity CLI 1.4.0 (release notes)
	"copilot":  true, // Copilot CLI 1.0.106 (bundled changelog; uses tmux passthrough)
	"grok":     true, // Grok Build 1.0.52 (program_status = "on" needed inside tmux)
	"pi":       true, // Pi 1.1.0 (packages/tui/src/program-status.ts)
	"omp":      true, // Oh My Pi v18.8.7 (packages/coding-agent/src/utils/run-status.ts)
	"muse":     true, // Muse Code (changelog)
}

// defaultOSC7501Agents returns the agents that emit when osc7501_agents is
// unset: every supported agent that does not report OSC 7501 itself.
func defaultOSC7501Agents() []string {
	var agents []string
	for _, agent := range allAgents {
		if !nativeOSC7501Agents[agent] {
			agents = append(agents, agent)
		}
	}
	return agents
}

// emitsOSC7501 reports whether hooks for agent write OSC 7501 reports.
// An explicit osc7501_agents list is used as written, so listing a native
// agent turns its reports on.
func (c Config) emitsOSC7501(agent string) bool {
	if !c.OSC7501 {
		return false
	}
	if c.OSC7501Agents == nil {
		return !nativeOSC7501Agents[agent]
	}
	for _, a := range c.OSC7501Agents {
		if a == agent {
			return true
		}
	}
	return false
}

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
