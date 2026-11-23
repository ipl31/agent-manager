package execution

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/YoanWai/agent-manager/internal/agentsession"
	"github.com/YoanWai/agent-manager/internal/config"
	"github.com/YoanWai/agent-manager/internal/status"
	"github.com/YoanWai/agent-manager/internal/store"
)

func observationRunner(t testing.TB) *Runner {
	t.Helper()
	cfg, err := config.Default()
	if err != nil {
		t.Fatal(err)
	}
	engine, err := status.NewEngine(cfg)
	if err != nil {
		t.Fatal(err)
	}
	return &Runner{engine: engine, paneHashes: map[string]uint64{}, waitingScreens: map[string]uint64{}, quietSince: map[string]quietTimer{}, transcripts: map[string]transcriptTailCache{}}
}

func combinedObservation(p *Runner, sess store.Session, pane string) (string, string, string, error) {
	observation := p.observePane(sess.Tool, pane)
	quote, prompt := p.rowLines(context.Background(), sess, observation, "")
	state, err := p.derivePaneStatus(sess, observation, true, map[string]uint64{})
	return quote, prompt, state, err
}

func BenchmarkPaneObservation(b *testing.B) {
	for _, waiting := range []bool{false, true} {
		name := "ordinary"
		pane := "❯ user request\n\n⏺ " + strings.Repeat("\x1b[31mA colored reply with words.\x1b[39m\n", 30) + "\n❯ \n"
		if waiting {
			name = "waiting"
			pane += " Bash command\n Do you want to proceed?\n ❯ 1. Yes\n Enter to confirm\n"
		}
		b.Run(name, func(b *testing.B) {
			p := observationRunner(b)
			sess := store.Session{ID: "observation", Tool: "claude"}
			_, _, state, err := combinedObservation(p, sess, pane)
			if err != nil || (state == status.Waiting) != waiting {
				b.Fatalf("fixture state=%q waiting=%v err=%v", state, waiting, err)
			}
			b.ReportAllocs()
			b.ResetTimer()
			for range b.N {
				if _, _, _, err := combinedObservation(p, sess, pane); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

func fixtureObservationRunner(t testing.TB) *Runner {
	t.Helper()
	cfg := config.Config{Tools: map[string]config.Tool{
		"fixture": {ActivityCutoff: `(?m)^PROMPT`, MessageStart: `^MSG: `, DialogQuestion: `(?m)^ASK: (.*)`, Rules: []config.Rule{{State: status.Waiting, Pattern: `(?m)^ASK:`}, {State: status.Working, Pattern: `(?m)^BUSY`}}},
	}}
	engine, err := status.NewEngine(cfg)
	if err != nil {
		t.Fatal(err)
	}
	return &Runner{engine: engine, paneHashes: map[string]uint64{}, waitingScreens: map[string]uint64{}, quietSince: map[string]quietTimer{}, transcripts: map[string]transcriptTailCache{}, reportStyles: map[string]string{"fixture": "claude"}}
}

func TestPaneObservationKeepsOriginalQuestionBeyondDisplayCap(t *testing.T) {
	p := fixtureObservationRunner(t)
	sess := store.Session{ID: "long-question", Tool: "fixture"}
	question := strings.Repeat("界", rowQuoteCap) + "first?"
	raw := "\x1b[31mMSG: old reply\x1b[0m\nPROMPT\nASK: " + question + "\n"
	observed := p.observePane(sess.Tool, raw)
	quote, _, state, err := combinedObservation(p, sess, raw)
	if err != nil || state != status.Waiting {
		t.Fatalf("state=%q err=%v", state, err)
	}
	if quote != strings.Repeat("界", rowQuoteCap) {
		t.Fatalf("display quote=%q", quote)
	}
	wantScreen := "MSG: old reply\n\x00" + question
	if got := p.waitingScreen(sess.Tool, observed); got != wantScreen {
		t.Fatalf("screen=%q want=%q", got, wantScreen)
	}
	sess.Acked = true
	if _, _, got, err := combinedObservation(p, sess, raw); err != nil || got != status.Idle {
		t.Fatalf("unchanged acknowledged question=%q,%v", got, err)
	}
	next := strings.Replace(raw, "first?", "second?", 1)
	if _, _, got, err := combinedObservation(p, sess, next); err != nil || got != status.Waiting {
		t.Fatalf("changed question beyond display cap=%q,%v", got, err)
	}
	if observed.raw != raw {
		t.Fatal("raw ANSI preview changed")
	}
}

func TestPaneObservationNoRegionUsesWholePlainScreen(t *testing.T) {
	p := fixtureObservationRunner(t)
	raw := "\x1b[31mfirst\x1b[0m\n\nASK: Trust this directory?\n  \n"
	observed := p.observePane("fixture", raw)
	want := "first\n\nASK: Trust this directory?\n  \n"
	if got := p.waitingScreen("fixture", observed); got != want {
		t.Fatalf("screen=%q want=%q", got, want)
	}
	quote, _, state, err := combinedObservation(p, store.Session{ID: "no-region", Tool: "fixture"}, raw)
	if err != nil || state != status.Waiting || quote != "Trust this directory?" {
		t.Fatalf("quote=%q state=%q err=%v", quote, state, err)
	}
}

func TestPaneObservationFallbackDoesNotBecomeTheWaitingMessage(t *testing.T) {
	p := fixtureObservationRunner(t)
	raw := "\x1b[31mNo composer yet\x1b[0m\n"
	observed := p.observePane("fixture", raw)
	quote, _, _, err := combinedObservation(p, store.Session{ID: "fallback", Tool: "fixture"}, raw)
	if err != nil || quote != "No composer yet" || observed.message != "" || observed.messageOK {
		t.Fatalf("quote=%q observation=%+v err=%v", quote, observed, err)
	}
}

func TestPaneObservationStuckTimerStillHashesRawStyles(t *testing.T) {
	p := fixtureObservationRunner(t)
	sess := store.Session{ID: "styles", Tool: "fixture", Status: status.Working}
	first := "MSG: \x1b[31mBUSY\x1b[0m\nPROMPT\n"
	next := "MSG: \x1b[32mBUSY\x1b[0m\nPROMPT\n"
	// Match the work marker independently of its message prefix.
	cfg := config.Config{Tools: map[string]config.Tool{"fixture": {ActivityCutoff: `(?m)^PROMPT`, MessageStart: `^MSG: `, Rules: []config.Rule{{State: status.Working, Pattern: `BUSY`}}}}}
	var err error
	p.engine, err = status.NewEngine(cfg)
	if err != nil {
		t.Fatal(err)
	}
	a, b := p.observePane(sess.Tool, first), p.observePane(sess.Tool, next)
	if a.plain != b.plain {
		t.Fatal("style-only change changed plain text")
	}
	region, _ := p.engine.ActivityRegion(sess.Tool, a.plain)
	p.paneHashes[sess.ID] = hashString(region)
	old := time.Now().Add(-stuckEndGrace - time.Second)
	p.quietSince[sess.ID] = quietTimer{since: old, stuck: true, pane: hashString(first)}
	hashes := map[string]uint64{}
	got, err := p.derivePaneStatus(sess, b, true, hashes)
	if err != nil || got != status.Working {
		t.Fatalf("status=%q err=%v", got, err)
	}
	timer := p.quietSince[sess.ID]
	if !timer.since.After(old) || timer.pane != hashString(next) || hashes[sess.ID] != p.paneHashes[sess.ID] {
		t.Fatalf("timer=%+v hashes=%v", timer, hashes)
	}
}

func TestPaneObservationNormalizationPreservesBlinkAndScrollbarRules(t *testing.T) {
	p := observationRunner(t)
	blink := "\x1b[38;5;246m\x1b[49m \x1b[39m Sleeping\n❯ \n"
	observed := p.observePane("claude", blink)
	if observed.plain != "⏺ Sleeping\n❯ \n" || observed.message != "Sleeping" {
		t.Fatalf("blink observation=%+v", observed)
	}
	scrollbar := " ● Keep this glyph: ┃\n" + strings.Repeat(" ", 30) + "┃\n ● a reply" + strings.Repeat(" ", 20) + "┃"
	got := p.observePane("copilot", scrollbar)
	if got.plain != " ● Keep this glyph: ┃\n\n ● a reply" || got.raw != scrollbar {
		t.Fatalf("scrollbar observation=%+v", got)
	}
}

func observationTranscript(t testing.TB, sess store.Session) {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	t.Setenv("USERPROFILE", os.Getenv("HOME"))
	path, err := agentsession.ClaudeTranscriptPath(sess.Cwd, sess.AgentSessionID)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	content := `{"type":"user","message":{"role":"user","content":"recovered prompt"}}` + "\n" + `{"type":"assistant","message":{"content":[{"type":"text","text":"recovered reply"}]}}` + "\n"
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestPaneObservationRecoveredDisplayKeepsVisibleWaitingMessage(t *testing.T) {
	p := fixtureObservationRunner(t)
	cfg := config.Config{Tools: map[string]config.Tool{"fixture": {ActivityCutoff: `(?m)^PROMPT`, MessageStart: `^MSG: `, Rules: []config.Rule{{State: status.Waiting, Pattern: `WAIT`}}}}}
	var err error
	p.engine, err = status.NewEngine(cfg)
	if err != nil {
		t.Fatal(err)
	}
	sess := store.Session{ID: "recovery", Tool: "fixture", Cwd: t.TempDir(), AgentSessionID: "conversation"}
	observationTranscript(t, sess)
	raw := "\x1b[31mvisible tail WAIT\x1b[0m\nPROMPT\n"
	observation := p.observePane(sess.Tool, raw)
	quote, prompt, state, err := combinedObservation(p, sess, raw)
	if err != nil || quote != "recovered reply" || prompt != "recovered prompt" || state != status.Waiting {
		t.Fatalf("quote=%q prompt=%q state=%q err=%v", quote, prompt, state, err)
	}
	if got := p.waitingScreen(sess.Tool, observation); got != "visible tail WAIT\n\x00visible tail WAIT" {
		t.Fatalf("waiting screen=%q", got)
	}
	if observation.message != "visible tail WAIT" || observation.anchored {
		t.Fatalf("visible observation=%+v", observation)
	}
}

func BenchmarkPaneObservationTranscriptFallback(b *testing.B) {
	p := observationRunner(b)
	p.reportStyles = map[string]string{"claude": "claude"}
	sess := store.Session{ID: "recovery", Tool: "claude", Cwd: b.TempDir(), AgentSessionID: "conversation"}
	observationTranscript(b, sess)
	pane := strings.Repeat("\x1b[31mUnanchored reply tail.\x1b[0m\n", 30) + "\n❯ \n"
	if quote, prompt, _, err := combinedObservation(p, sess, pane); err != nil || quote != "recovered reply" || prompt != "recovered prompt" {
		b.Fatalf("fallback quote=%q prompt=%q err=%v", quote, prompt, err)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		if _, _, _, err := combinedObservation(p, sess, pane); err != nil {
			b.Fatal(err)
		}
	}
}
