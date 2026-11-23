package main

import (
	"context"
	"sync"
	"time"
)

// runtimeShutdown has repeat-observable completion: a notice or a second
// observer must not consume the only acknowledgement of cleanup.
type runtimeShutdown struct {
	done chan struct{}
	mu   sync.Mutex
	err  error
}

func startRuntimeShutdown(confirm func(context.Context) error) *runtimeShutdown {
	shutdown := &runtimeShutdown{done: make(chan struct{})}
	go func() {
		for {
			err := confirm(context.Background())
			shutdown.mu.Lock()
			shutdown.err = err
			shutdown.mu.Unlock()
			if err == nil {
				close(shutdown.done)
				return
			}
			// A real close failure is uncertainty, not permission to exit. Retry
			// the same retained resources; no poll/effect admission is reopened.
			timer := time.NewTimer(200 * time.Millisecond)
			<-timer.C
		}
	}()
	return shutdown
}

func (s *runtimeShutdown) uncertainty() error { s.mu.Lock(); defer s.mu.Unlock(); return s.err }

// awaitRuntimeShutdown's notice budget is independent of cleanup methods
// that may need their own gate/reap budget. Only explicit force bypasses it.
func awaitRuntimeShutdown(done, force <-chan struct{}, forced bool, after time.Duration, notice func()) bool {
	if forced {
		return true
	}
	timer := time.NewTimer(after)
	defer timer.Stop()
	timerC := timer.C
	for {
		select {
		case <-done:
			return false
		case <-force:
			return true
		case <-timerC:
			notice()
			timerC = nil
		}
	}
}
