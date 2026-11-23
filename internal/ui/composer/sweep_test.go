package composer

import (
	"errors"
	"github.com/YoanWai/agent-manager/internal/ui/effects"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func TestSweepReportsSweepError(t *testing.T) {
	orig := SweepStale
	defer func() { SweepStale = orig }()
	SweepStale = func() error { return errors.New("permission denied") }

	msg, ok := Sweep().(SweepMsg)
	if !ok {
		t.Fatalf("want SweepMsg, got %T", Sweep())
	}
	if msg.Err == nil || msg.Err.Error() != "permission denied" {
		t.Fatalf("got %v", msg.Err)
	}
}

func TestSweepTickSweepsAgainAndRearms(t *testing.T) {
	if SweepTick() == nil {
		t.Fatal("tick must return work")
	}
	// A manager left open for weeks only keeps sweeping if the tick both
	// sweeps and re-arms. The root admits the owned sweep,
	// and running the timer itself would wait out the real interval.
	cmd, handled := Update(&sweepRoot{}, SweepTickMsg{})
	if !handled || cmd == nil {
		t.Fatal("the tick went unhandled")
	}
	batch, ok := cmd().(tea.BatchMsg)
	if !ok {
		t.Fatal("the tick did not batch a sweep and a re-arm")
	}
	if len(batch) != 2 {
		t.Fatalf("want sweep plus re-arm, got %d commands", len(batch))
	}
}

type sweepRoot struct{ scope effects.Lifetime }

func (r *sweepRoot) Background(cmd tea.Cmd) tea.Cmd { return effects.Leaf(&r.scope, cmd) }
func (r *sweepRoot) ComposerFor(ID) *Composer       { panic("sweep must not read a composer") }
func (r *sweepRoot) ComposerOpen(ID) bool           { panic("sweep must not read a composer") }
func (r *sweepRoot) ReportErr(string)               { panic("tick must not report before sweep") }
func (r *sweepRoot) ClearErr()                      { panic("tick must not change error state") }
