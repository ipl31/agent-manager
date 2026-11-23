package ui

import (
	"context"
	"errors"
	uiterminal "github.com/YoanWai/agent-manager/internal/ui/terminal"
	"testing"
	"time"

	"github.com/YoanWai/agent-manager/internal/store"
	uiinstall "github.com/YoanWai/agent-manager/internal/ui/launchhint/install"
	uirail "github.com/YoanWai/agent-manager/internal/ui/rail"
	tea "github.com/charmbracelet/bubbletea"
)

func TestTakeoverJoinsUserQuitWithoutAbandoningEffects(t *testing.T) {
	m := buildModel(t)
	m.SetOwnerID("owner")
	m.queueRail([]uirail.Mutation{{Kind: uirail.SaveCollapsed, Collapsed: []string{"a"}}, {Kind: uirail.SaveCollapsed, Collapsed: []string{"b"}}}, 0, false)
	m.requestQuit()
	m.Update(TakeoverMsg{Owner: "owner", Reason: errors.New("replacement")})
	m.Update(TakeoverMsg{Owner: "owner", Reason: errors.New("duplicate")})
	if !m.TakenOver() || len(m.AbandonedEffects()) != 0 || len(m.effects.Main.Pending) != 1 {
		t.Fatal("takeover forced a second quit instead of joining the drain")
	}
}

func TestTakeoverKeepsInstallerProtectedThenQuitsAfterCompletion(t *testing.T) {
	m := buildModel(t)
	m.SetOwnerID("owner")
	m.ledger.install.Pending = &uiinstall.Pending{Name: "installer"}
	m.Update(TakeoverMsg{Owner: "old-owner"})
	if m.TakenOver() {
		t.Fatal("stale owner quit current runtime")
	}
	m.Update(TakeoverMsg{Owner: "owner"})
	if m.effects.Quitting {
		t.Fatal("takeover bypassed active installer")
	}
	m.ledger.install.Pending = nil
	_, cmd := m.Update(struct{}{})
	if !m.effects.Quitting || cmd == nil {
		t.Fatal("deferred takeover never joined normal quit")
	}
}

func TestQuitRejectsGeneratedBackgroundLeafAndJoinsRunningLeaf(t *testing.T) {
	m := buildModel(t)
	started, release, done := make(chan struct{}), make(chan struct{}), make(chan struct{})
	running := m.Background(func() tea.Msg { close(started); <-release; return nil })
	go func() { running(); close(done) }()
	<-started
	lateRan := false
	late := m.Background(func() tea.Msg { lateRan = true; return nil })
	m.requestQuit()
	late()
	if lateRan {
		t.Fatal("generated leaf started after quit")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	if err := m.ConfirmShutdown(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("blocked worker falsely drained: %v", err)
	}
	close(release)
	<-done
	if err := m.ConfirmShutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestTakeoverDoesNotChangeSessionProjection(t *testing.T) {
	m := buildModel(t)
	m.workspace.sessions = []store.Session{{ID: "retained"}}
	m.SetOwnerID("owner")
	m.Update(TakeoverMsg{Owner: "owner"})
	if len(m.workspace.sessions) != 1 || m.workspace.sessions[0].ID != "retained" {
		t.Fatal("takeover mutated sessions")
	}
}

func TestInternalUpdateQuitJoinsDrainWithoutSynthesizingForce(t *testing.T) {
	m := buildModel(t)
	m.queueRail([]uirail.Mutation{{Kind: uirail.SaveCollapsed, Collapsed: []string{"a"}}}, 0, false)
	m.requestQuit()
	releaseRoot{m}.Quit()
	if m.ForcedQuit() || len(m.AbandonedEffects()) != 0 {
		t.Fatal("internal update completion synthesized a user's second quit")
	}
	m.requestQuit()
	if !m.ForcedQuit() {
		t.Fatal("explicit user's second quit did not record force")
	}
}

func TestInstallerRefusalDoesNotCloseAdmissionOrRecordForce(t *testing.T) {
	m := buildModel(t)
	m.ledger.install.Pending = &uiinstall.Pending{Name: "protected"}
	m.requestQuit()
	m.requestQuit()
	if m.ForcedQuit() || m.effects.Quitting {
		t.Fatal("installer refusal became an accepted/forced quit")
	}
	if !m.effects.background.Begin() {
		t.Fatal("refused quit closed background admission")
	}
	m.effects.background.Done()
}

func TestLateSSHLinkPageUsesClosedOwnedScope(t *testing.T) {
	m := buildModel(t)
	m.requestQuit()
	old := showLinkPage
	t.Cleanup(func() { showLinkPage = old })
	called := false
	showLinkPage = func(scope uiterminal.ExecLifetime, url string) tea.Cmd {
		called = true
		if scope != m.effects.background || url != "https://example.com/late" {
			t.Fatal("late page did not receive the runtime scope")
		}
		if scope.Begin() {
			scope.Done()
			t.Fatal("late page's actual Run would be admitted")
		}
		return nil
	}
	if _, _, handled := m.routeFocusMsg(linkPageMsg{url: "https://example.com/late"}); !handled || !called {
		t.Fatal("late SSH page was not routed through owned factory")
	}
}
