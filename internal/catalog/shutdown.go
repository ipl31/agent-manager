package catalog

import "context"

// Close prevents already admitted catalog work from starting another child
// after shutdown's snapshot, and confirms every started child was reaped.
// The admission gate is process-lifetime: a clean self-update replaces it.
func Close(ctx context.Context) error {
	running.Lock()
	running.closing = true
	procs := make([]*process, 0, len(running.pending))
	for proc := range running.pending {
		procs = append(procs, proc)
	}
	running.Unlock()
	finished := make(chan struct{})
	go func() {
		for _, proc := range procs {
			proc.stop()
			<-proc.exited
		}
		close(finished)
	}()
	select {
	case <-finished:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
