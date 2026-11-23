package effects

import (
	"context"
	"errors"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

func TestLeafGeneratedBeforeCloseRefusesExecutionAfterClose(t *testing.T) {
	lifetime := &Lifetime{}
	ran := false
	cmd := Leaf(lifetime, func() tea.Msg { ran = true; return nil })
	lifetime.CloseAdmission()
	cmd()
	if ran {
		t.Fatal("late leaf started after admission closed")
	}
	if err := lifetime.Join(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestLeafJoinWaitsForAdmittedWork(t *testing.T) {
	lifetime := &Lifetime{}
	started, release, finished := make(chan struct{}), make(chan struct{}), make(chan struct{})
	cmd := Leaf(lifetime, func() tea.Msg { close(started); <-release; return nil })
	go func() { cmd(); close(finished) }()
	<-started
	lifetime.CloseAdmission()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	if err := lifetime.Join(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("unconfirmed work reported drained: %v", err)
	}
	close(release)
	<-finished
	if err := lifetime.Join(context.Background()); err != nil {
		t.Fatal(err)
	}
	if lifetime.Begin() {
		t.Fatal("join reopened admission")
	}
}
