package ui

import (
	"testing"
	"time"

	"github.com/YoanWai/agent-manager/internal/config"
	"github.com/YoanWai/agent-manager/internal/status"
	"github.com/YoanWai/agent-manager/internal/store"
)

func FuzzPollerStatusTrace(f *testing.F) {
	cfg, err := config.Default()
	if err != nil {
		f.Fatal(err)
	}
	engine, err := status.NewEngine(cfg)
	if err != nil {
		f.Fatal(err)
	}
	f.Add([]byte{0, 1, 1, 2, 0, 3, 4})
	f.Add([]byte{1, 0, 0, 1, 3, 0, 4})
	frames := []struct {
		pane string
		want string
	}{
		{"› Ask Codex to do anything\n  model · /work", status.Idle},
		{"• Working (12s • esc to interrupt)\n\n› Ask Codex to do anything", status.Working},
		{"› 1. Yes, proceed\n  2. No\nenter to submit answer", status.Waiting},
		{"• Final answer.\n─ Worked for 2s ─\n› Ask Codex to do anything", status.Finished},
		{"You've hit your usage limit\n› Ask Codex to do anything", status.Errored},
	}
	f.Fuzz(func(t *testing.T, events []byte) {
		if len(events) > 32 {
			return
		}
		p := &poller{engine: engine, statusSources: map[string]string{}, paneHashes: map[string]uint64{}, quietSince: map[string]quietTimer{}}
		sess := store.Session{ID: "s", Tool: "codex", Status: status.Idle}
		for step, value := range events {
			frame := frames[int(value)%len(frames)]
			updated := map[string]uint64{}
			priorTimer := p.quietSince[sess.ID]
			started := time.Now()
			got, err := p.derivePaneStatus(sess, frame.pane, true, updated)
			if err != nil {
				t.Fatalf("step %d: %v", step, err)
			}
			quietExpired := priorTimer.stuck && priorTimer.pane == hashString(frame.pane) && !priorTimer.since.IsZero() &&
				(time.Since(priorTimer.since) >= stuckEndGrace || time.Since(started) >= stuckEndGrace)
			if frame.want != status.Idle && got != frame.want && !(frame.want == status.Working && quietExpired && got == status.Finished) {
				t.Fatalf("step %d: status %q for %q, want %q", step, got, frame.pane, frame.want)
			}
			if frame.want == status.Idle && got != status.Idle && got != status.Working && got != status.Finished && got != status.Waiting {
				t.Fatalf("step %d: idle frame made %q", step, got)
			}
			if _, ready := engine.ActivityRegion("codex", frame.pane); ready {
				if gotHash, ok := updated[sess.ID]; !ok || gotHash == 0 {
					t.Fatalf("step %d: missing region hash", step)
				}
			}
			for id, hash := range updated {
				p.paneHashes[id] = hash
			}
			sess.Status = got
		}
	})
}
