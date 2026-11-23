package tmux

import (
	"context"
	"errors"
	"sync"
)

type controlCompletion struct {
	control      *Control
	gateReleased chan struct{}
	done         chan struct{}
	// cleanup means a failed open already owns the exclusive gate until Wait.
	// It is read only after all admitted open calls have returned.
	cleanup bool
}

type controlRegistry struct {
	mu      sync.Mutex
	closed  bool
	opening sync.WaitGroup
	clients map[*Control]*controlCompletion
}

func (r *controlRegistry) begin() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		return false
	}
	r.opening.Add(1)
	return true
}

func (r *controlRegistry) add(control *Control) *controlCompletion {
	record := &controlCompletion{control: control, gateReleased: make(chan struct{}), done: make(chan struct{})}
	r.mu.Lock()
	if r.clients == nil {
		r.clients = make(map[*Control]*controlCompletion)
	}
	r.clients[control] = record
	r.mu.Unlock()
	return record
}

func (r *controlRegistry) finish(record *controlCompletion) {
	<-record.gateReleased
	r.mu.Lock()
	delete(r.clients, record.control)
	close(record.done)
	r.mu.Unlock()
}

// CloseControls fences every successful control Start, including a failed
// greeting that returned no Control. Both Wait and handshake release must
// finish before the owning runtime can release its lifetime lock.
func (d *Driver) CloseControls(ctx context.Context) error {
	r := &d.controls
	r.mu.Lock()
	r.closed = true
	r.mu.Unlock()
	opened := make(chan struct{})
	go func() { r.opening.Wait(); close(opened) }()
	select {
	case <-opened:
	case <-ctx.Done():
		return ctx.Err()
	}
	r.mu.Lock()
	clients := make([]*controlCompletion, 0, len(r.clients))
	for _, record := range r.clients {
		clients = append(clients, record)
	}
	r.mu.Unlock()
	for _, record := range clients {
		if !record.cleanup {
			// A failed-open record already has cleanup in progress while holding
			// its exclusive gate. It must not ask for a shared gate behind itself.
			closeErr := record.control.CloseContext(ctx)
			if closeErr != nil {
				select {
				case <-record.done:
					continue
				default:
				}
				return closeErr
			}
		}
		select {
		case <-record.done:
		case <-ctx.Done():
			return errors.Join(ctx.Err(), errors.New("control client has not been reaped"))
		}
	}
	return nil
}
