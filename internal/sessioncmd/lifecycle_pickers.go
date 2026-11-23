package sessioncmd

import (
	"context"
	"sync"
)

// pickerWorkers belongs to the exact Lifecycle that dispatched the async
// picker. Managers join it after their ordered effects; CLI/MCP calls keep
// their existing asynchronous return policy.
type pickerWorkers struct {
	mu      sync.Mutex
	closed  bool
	running sync.WaitGroup
}

func (p *pickerWorkers) start(work func()) bool {
	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		return false
	}
	p.running.Add(1)
	p.mu.Unlock()
	go func() { defer p.running.Done(); work() }()
	return true
}

// ClosePickers refuses future picker dispatch and joins the last possible
// Enter, including the stability window after the first send. It must be
// called after admitted outer lifecycle effects finish dispatching work.
func (l *Lifecycle) ClosePickers(ctx context.Context) error {
	p := l.pickers
	p.mu.Lock()
	p.closed = true
	p.mu.Unlock()
	done := make(chan struct{})
	go func() { p.running.Wait(); close(done) }()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
