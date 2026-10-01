package config

import (
	"slices"
	"testing"
)

func TestAgentToolSelectionMatchesPickerOrderAndSettings(t *testing.T) {
	cfg := Config{Tools: map[string]Tool{
		"codex": {}, "claude": {}, "custom": {}, "terminal": {Shell: true},
	}}
	all := AgentToolNames(cfg)
	if !slices.Equal(all, []string{"claude", "codex", "custom"}) {
		t.Fatalf("tool order = %v", all)
	}
	names := EnabledAgentTools(cfg, HiddenTools("claude, terminal"))
	if !slices.Equal(names, []string{"codex", "custom"}) {
		t.Fatalf("enabled = %v", names)
	}
	if got := DefaultAgentTool(names, "custom"); got != "custom" {
		t.Fatalf("saved default = %q", got)
	}
	if got := DefaultAgentTool(names, "claude"); got != "codex" {
		t.Fatalf("hidden default fallback = %q", got)
	}
	if got := DefaultAgentTool(names, "deleted"); got != "codex" {
		t.Fatalf("stale default fallback = %q", got)
	}
	if got := DefaultAgentTool(EnabledAgentTools(cfg, HiddenTools("claude,codex,custom")), "claude"); got != "" {
		t.Fatalf("all hidden = %q", got)
	}
}
