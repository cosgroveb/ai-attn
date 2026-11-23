package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// withFakeTTY points terminalPath at an empty file for the test and returns its path.
func withFakeTTY(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "tty")
	if err := os.WriteFile(path, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	prev := terminalPath
	terminalPath = func() string { return path }
	t.Cleanup(func() { terminalPath = prev })
	return path
}

// writeConfig writes the given TOML to the test HOME's ai-attn config.
func writeConfig(t *testing.T, home, toml string) {
	t.Helper()
	cfg := filepath.Join(home, ".config", "ai-attn", "config.toml")
	if err := os.MkdirAll(filepath.Dir(cfg), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(cfg, []byte(toml), 0o644); err != nil {
		t.Fatal(err)
	}
}

// hookReport runs one hook event and returns what it wrote to the fake terminal.
func hookReport(t *testing.T, tty, agent, payload string) string {
	t.Helper()
	if err := os.Truncate(tty, 0); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	if rc := run([]string{"hook", "--agent", agent}, strings.NewReader(payload), &stdout, &stderr); rc != exitOK {
		t.Fatalf("hook rc=%d stderr=%s", rc, stderr.String())
	}
	got, err := os.ReadFile(tty)
	if err != nil {
		t.Fatal(err)
	}
	return string(got)
}

func TestProgramStatus(t *testing.T) {
	tests := []struct {
		state, reason, want string
	}{
		{"working", "PreToolUse", "state=working:msg=V29ya2luZw=="},
		{"waiting", "permission_request", "state=blocked:kind=permission:msg=TmVlZHMgcGVybWlzc2lvbg=="},
		{"waiting", "ToolPermission", "state=blocked:kind=permission:msg=TmVlZHMgcGVybWlzc2lvbg=="},
		{"waiting", "permission.updated", "state=blocked:kind=permission:msg=TmVlZHMgcGVybWlzc2lvbg=="},
		{"waiting", "elicitation_dialog", "state=blocked:kind=question:msg=SGFzIGEgcXVlc3Rpb24="},
		{"waiting", "notification", "state=blocked:msg=V2FpdGluZyBmb3IgaW5wdXQ="},
		{"done", "agent-turn-complete", "state=done:msg=VHVybiBjb21wbGV0ZQ=="},
		{"stopped", "StopFailure", "state=error:msg=RmFpbGVk"},
		{"stopped", "session.error", "state=error:msg=RmFpbGVk"},
		{"stopped", "PostToolUseFailure", "state=working:msg=V29ya2luZw=="},
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

func TestEmitsOSC7501(t *testing.T) {
	tests := []struct {
		name  string
		cfg   Config
		agent string
		want  bool
	}{
		{"off", Config{}, "gemini", false},
		{"default emits non-native", Config{OSC7501: true}, "gemini", true},
		{"default skips native", Config{OSC7501: true}, "claude", false},
		{"explicit forces native", Config{OSC7501: true, OSC7501Agents: []string{"claude"}}, "claude", true},
		{"explicit excludes unlisted", Config{OSC7501: true, OSC7501Agents: []string{"claude"}}, "gemini", false},
		{"empty list emits nothing", Config{OSC7501: true, OSC7501Agents: []string{}}, "gemini", false},
	}
	for _, tt := range tests {
		if got := tt.cfg.emitsOSC7501(tt.agent); got != tt.want {
			t.Errorf("%s: emitsOSC7501(%q) = %t, want %t", tt.name, tt.agent, got, tt.want)
		}
	}
}

func TestParseConfigDistinguishesUnsetAndEmptyAgents(t *testing.T) {
	unset, err := parseConfig([]byte("osc7501 = true\n"))
	if err != nil || unset.OSC7501Agents != nil {
		t.Fatalf("unset osc7501_agents: %+v, %v", unset, err)
	}
	empty, err := parseConfig([]byte("osc7501 = true\nosc7501_agents = []\n"))
	if err != nil || empty.OSC7501Agents == nil || len(empty.OSC7501Agents) != 0 {
		t.Fatalf("empty osc7501_agents: %+v, %v", empty, err)
	}
}

func TestDefaultOSC7501AgentsExcludesNative(t *testing.T) {
	for _, agent := range defaultOSC7501Agents() {
		if nativeOSC7501Agents[agent] {
			t.Errorf("default list includes native agent %q", agent)
		}
	}
	for agent := range nativeOSC7501Agents {
		if !strings.Contains(strings.Join(allAgents, ","), agent) {
			t.Errorf("native list names unsupported agent %q", agent)
		}
	}
}

func TestHookEmitsProgramStatusWhenEnabled(t *testing.T) {
	writeConfig(t, withTempHome(t), "osc7501 = true\n")
	tty := withFakeTTY(t)

	tests := []struct {
		payload, want string
	}{
		{`{"hook_event_name":"BeforeAgent","session_id":"osc-1"}`, "\x1b]7501;state=working:msg=V29ya2luZw==:app=gemini\x1b\\"},
		{`{"hook_event_name":"Notification","notification_type":"ToolPermission","session_id":"osc-1"}`, "\x1b]7501;state=blocked:kind=permission:msg=TmVlZHMgcGVybWlzc2lvbg==:app=gemini\x1b\\"},
		{`{"hook_event_name":"AfterAgent","session_id":"osc-1"}`, "\x1b]7501;state=done:msg=VHVybiBjb21wbGV0ZQ==:app=gemini\x1b\\"},
		{`{"hook_event_name":"SessionEnd","session_id":"osc-1"}`, "\x1b]7501;state=clear:app=gemini\x1b\\"},
	}
	for _, tt := range tests {
		if got := hookReport(t, tty, "gemini", tt.payload); got != tt.want {
			t.Errorf("%s: tty got %q, want %q", tt.payload, got, tt.want)
		}
	}
}

func TestHookSkipsNativeAgentByDefault(t *testing.T) {
	writeConfig(t, withTempHome(t), "osc7501 = true\n")
	tty := withFakeTTY(t)

	for _, event := range []string{"UserPromptSubmit", "SessionEnd"} {
		if got := hookReport(t, tty, "claude", `{"hook_event_name":"`+event+`","session_id":"osc-2","cwd":"/work"}`); got != "" {
			t.Errorf("%s: expected no report for native claude, got %q", event, got)
		}
	}
}

func TestHookExplicitAgentsForceNativeAgent(t *testing.T) {
	writeConfig(t, withTempHome(t), "osc7501 = true\nosc7501_agents = [\"claude\"]\n")
	tty := withFakeTTY(t)

	want := "\x1b]7501;state=working:msg=V29ya2luZw==:app=claude\x1b\\"
	if got := hookReport(t, tty, "claude", `{"hook_event_name":"UserPromptSubmit","session_id":"osc-3","cwd":"/work"}`); got != want {
		t.Fatalf("tty got %q, want %q", got, want)
	}
}

func TestHookSkipsProgramStatusByDefault(t *testing.T) {
	withTempHome(t)
	tty := withFakeTTY(t)

	if got := hookReport(t, tty, "gemini", `{"hook_event_name":"BeforeAgent","session_id":"osc-4"}`); got != "" {
		t.Fatalf("expected no report with osc7501 off, got %q", got)
	}
}

func TestHookWithoutTerminalStillRecordsState(t *testing.T) {
	writeConfig(t, withTempHome(t), "osc7501 = true\n")
	prev := terminalPath
	terminalPath = func() string { return "" }
	t.Cleanup(func() { terminalPath = prev })

	var stdout, stderr bytes.Buffer
	rc := run([]string{"hook", "--agent", "gemini"}, strings.NewReader(`{"hook_event_name":"AfterAgent","session_id":"osc-5"}`), &stdout, &stderr)
	if rc != exitOK || stderr.Len() != 0 {
		t.Fatalf("expected a silent skip: rc=%d stderr=%q", rc, stderr.String())
	}
	rc, out, _ := runCLI(t, "status", "--agent", "gemini", "--session-id", "osc-5")
	if rc != exitOK || !strings.Contains(out, "state=done") {
		t.Fatalf("expected done: rc=%d out=%s", rc, out)
	}
}
