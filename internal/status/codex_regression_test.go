package status

import "testing"

func TestCodexWorkingRowIsNotTheReply(t *testing.T) {
	engine := defaultEngine(t)
	pane := "• Current answer.\n" +
		"• Working (12s • esc to interrupt)\n\n" +
		"› Ask Codex to do anything\n"
	if got, _, ok := engine.LastMessage("codex", pane); !ok || got != "Current answer." {
		t.Fatalf("LastMessage = %q, ok=%t; want current reply", got, ok)
	}
}

func TestCodexQueuedFollowUpKeepsWorkingStatus(t *testing.T) {
	engine := defaultEngine(t)
	pane := "› original prompt\n" +
		"• Current answer.\n" +
		"• Working (12s • esc to interrupt)\n\n" +
		"• Queued follow-up inputs\n" +
		"  ↳ 1. Yes, proceed? esc to interrupt\n" +
		"    shift + ← edit last queued message\n\n" +
		"› Ask Codex to do anything\n"
	if got, matched := engine.Match("codex", pane); !matched || got != Working {
		t.Fatalf("Match = %q, matched=%t; want working", got, matched)
	}
	if got, _, ok := engine.LastMessage("codex", pane); !ok || got != "Current answer." {
		t.Fatalf("LastMessage = %q, ok=%t; want current reply", got, ok)
	}
}
