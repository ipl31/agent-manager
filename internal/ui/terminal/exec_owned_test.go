package terminal

import (
	"context"
	"errors"
	"io"
	"testing"
	"time"

	"github.com/YoanWai/agent-manager/internal/ui/effects"
)

type ownedExecFixture struct{ start, release chan struct{} }

func (c ownedExecFixture) Run() error          { close(c.start); <-c.release; return nil }
func (c ownedExecFixture) SetStdin(io.Reader)  {}
func (c ownedExecFixture) SetStdout(io.Writer) {}
func (c ownedExecFixture) SetStderr(io.Writer) {}

func TestOwnedExecQueuedBeforeQuitCannotStartAfterQuit(t *testing.T) {
	lifetime := &effects.Lifetime{}
	child := ownedExecFixture{start: make(chan struct{}), release: make(chan struct{})}
	queued := ownedExec{ExecCommand: child, lifetime: lifetime}
	lifetime.CloseAdmission()
	if err := queued.Run(); !errors.Is(err, ErrExecStopped) {
		t.Fatalf("delayed exec = %v", err)
	}
	select {
	case <-child.start:
		t.Fatal("child started after quit")
	default:
	}
}

func TestOwnedExecJoinWaitsForActualCommandReturn(t *testing.T) {
	lifetime := &effects.Lifetime{}
	child := ownedExecFixture{start: make(chan struct{}), release: make(chan struct{})}
	done := make(chan error, 1)
	go func() { done <- (ownedExec{ExecCommand: child, lifetime: lifetime}).Run() }()
	<-child.start
	lifetime.CloseAdmission()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	if err := lifetime.Join(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("interactive command falsely drained: %v", err)
	}
	close(child.release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if err := lifetime.Join(context.Background()); err != nil {
		t.Fatal(err)
	}
}
