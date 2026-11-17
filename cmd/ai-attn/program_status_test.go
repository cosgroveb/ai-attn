package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// withFakeTTY points ttyPath at an empty file for the test and returns its path.
func withFakeTTY(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "tty")
	if err := os.WriteFile(path, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	prev := ttyPath
	ttyPath = path
	t.Cleanup(func() { ttyPath = prev })
	return path
}

func enableOSC7501(t *testing.T, home string) {
	t.Helper()
	cfg := filepath.Join(home, ".config", "ai-attn", "config.toml")
	if err := os.MkdirAll(filepath.Dir(cfg), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(cfg, []byte("osc7501 = true\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}

func runClaudeHook(t *testing.T, payload string) string {
	t.Helper()
	var stdout, stderr bytes.Buffer
	if rc := run([]string{"hook", "--agent", "claude"}, strings.NewReader(payload), &stdout, &stderr); rc != exitOK {
		t.Fatalf("hook rc=%d stderr=%s", rc, stderr.String())
	}
	return stderr.String()
}

func TestProgramStatus(t *testing.T) {
	tests := []struct {
		state, reason, want string
	}{
		{"working", "PreToolUse", "state=working"},
		{"waiting", "permission_request", "state=blocked:kind=permission"},
		{"waiting", "PermissionRequest", "state=blocked:kind=permission"},
		{"waiting", "permission.updated", "state=blocked:kind=permission"},
		{"waiting", "elicitation_dialog", "state=blocked:kind=question"},
		{"waiting", "notification", "state=blocked"},
		{"done", "agent-turn-complete", "state=done"},
		{"stopped", "StopFailure", "state=error"},
		{"stopped", "session.error", "state=error"},
		{"stopped", "PostToolUseFailure", "state=working"},
	}
	for _, tt := range tests {
		got, ok := programStatus(tt.state, tt.reason)
		if !ok || got != tt.want {
			t.Errorf("programStatus(%q, %q) = %q, %t; want %q", tt.state, tt.reason, got, ok, tt.want)
		}
	}
	if _, ok := programStatus("bogus", ""); ok {
		t.Error("programStatus accepted an unknown state")
	}
}

func TestHookEmitsProgramStatusWhenEnabled(t *testing.T) {
	enableOSC7501(t, withTempHome(t))
	tty := withFakeTTY(t)

	tests := []struct {
		event, want string
	}{
		{"UserPromptSubmit", "\x1b]7501;state=working:app=claude\x1b\\"},
		{"PermissionRequest", "\x1b]7501;state=blocked:kind=permission:app=claude\x1b\\"},
		{"Stop", "\x1b]7501;state=done:app=claude\x1b\\"},
		{"SessionEnd", "\x1b]7501;state=clear:app=claude\x1b\\"},
	}
	for _, tt := range tests {
		if err := os.Truncate(tty, 0); err != nil {
			t.Fatal(err)
		}
		runClaudeHook(t, `{"hook_event_name":"`+tt.event+`","session_id":"osc-1","cwd":"/work"}`)
		got, err := os.ReadFile(tty)
		if err != nil {
			t.Fatal(err)
		}
		if string(got) != tt.want {
			t.Errorf("%s: tty got %q, want %q", tt.event, got, tt.want)
		}
	}
}

func TestHookSkipsProgramStatusByDefault(t *testing.T) {
	withTempHome(t)
	tty := withFakeTTY(t)

	runClaudeHook(t, `{"hook_event_name":"UserPromptSubmit","session_id":"osc-2","cwd":"/work"}`)

	got, err := os.ReadFile(tty)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Fatalf("expected no report with osc7501 off, got %q", got)
	}
}

func TestHookWithoutTerminalStillRecordsState(t *testing.T) {
	enableOSC7501(t, withTempHome(t))
	prev := ttyPath
	ttyPath = filepath.Join(t.TempDir(), "missing", "tty")
	t.Cleanup(func() { ttyPath = prev })

	if stderr := runClaudeHook(t, `{"hook_event_name":"Stop","session_id":"osc-3","cwd":"/work"}`); stderr != "" {
		t.Fatalf("expected a silent skip, got stderr %q", stderr)
	}
	rc, out, _ := runCLI(t, "status", "--agent", "claude", "--session-id", "osc-3")
	if rc != exitOK || !strings.Contains(out, "state=done") {
		t.Fatalf("expected done: rc=%d out=%s", rc, out)
	}
}
