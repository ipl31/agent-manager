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

func TestCodexLiveTurnScreens(t *testing.T) {
	engine := defaultEngine(t)
	for _, width := range []struct {
		name, prompt, heading string
	}{
		{"wide", "› Run the shell command sleep 18, then reply with exactly LIVE_SECOND.", "• Messages to be submitted after next tool call (press esc to interrupt and send immediately)"},
		{"narrow", "› Run the shell command sleep 18, then reply with exactly\n  LIVE_SECOND.", "• Messages to be submitted after next tool call (press esc\n  to interrupt and send immediately)"},
	} {
		t.Run(width.name, func(t *testing.T) {
			baseline := "› Reply with exactly LIVE_BASELINE and nothing else.\n\n" +
				"• LIVE_BASELINE\n\n  Worked for 7s • 00:34\n\n"
			working := baseline + width.prompt + "\n\n• Working (0s • esc to interrupt)\n\n"
			queued := working + width.heading + "\n  ↳ After that, reply with exactly LIVE_QUEUED.\n\n"
			for _, screen := range []struct{ name, pane, state string }{
				{"baseline", baseline, Finished},
				{"working", working, Working},
				{"queued", queued, Working},
			} {
				t.Run(screen.name, func(t *testing.T) {
					pane := screen.pane + "› Ask Codex to do anything\n"
					if got, matched := engine.Match("codex", pane); !matched || got != screen.state {
						t.Errorf("Match = %q, matched=%t; want %q", got, matched, screen.state)
					}
					if got, _, ok := engine.LastMessage("codex", pane); !ok || got != "LIVE_BASELINE" {
						t.Errorf("LastMessage = %q, ok=%t; want LIVE_BASELINE", got, ok)
					}
					if got, _, ok := engine.FullTurnText("codex", pane); !ok || got != "• LIVE_BASELINE" {
						t.Errorf("FullTurnText = %q, ok=%t; want only baseline reply", got, ok)
					}
				})
			}
		})
	}
}
