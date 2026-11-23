//go:build darwin || linux

package tmux

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestFailedGreetingRetainsWaitAndExclusiveGateAfterNilReturn(t *testing.T) {
	dir := t.TempDir()
	started := filepath.Join(dir, "started")
	stub := filepath.Join(dir, "tmux")
	script := "#!/bin/sh\n: > " + ShellQuote(started) + "\nwhile :; do sleep 1; done\n"
	if err := os.WriteFile(stub, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	driver := &Driver{bin: stub, socket: testSocket + "failed-open"}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	allowWait, waiting := make(chan struct{}), make(chan struct{})
	var releaseOnce sync.Once
	releaseWait := func() { releaseOnce.Do(func() { close(allowWait) }) }
	defer releaseWait()
	type result struct {
		control *Control
		err     error
	}
	returned := make(chan result, 1)
	go func() {
		control, err := driver.openControlContext(ctx, "fixture", func(cmd *exec.Cmd) error {
			close(waiting)
			<-allowWait
			return cmd.Wait()
		})
		returned <- result{control, err}
	}()
	deadline := time.Now().Add(3 * time.Second)
	for {
		if _, err := os.Stat(started); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("control did not start")
		}
		time.Sleep(5 * time.Millisecond)
	}
	cancel()
	<-waiting
	var opened result
	select {
	case opened = <-returned:
	case <-time.After(4 * time.Second):
		t.Fatal("failed open did not return within cleanup budget")
	}
	if opened.control != nil || !errors.Is(opened.err, context.Canceled) {
		t.Fatalf("failed greeting = %v %v", opened.control, opened.err)
	}
	driver.controls.mu.Lock()
	if len(driver.controls.clients) != 1 {
		driver.controls.mu.Unlock()
		t.Fatal("nil-return open lost its successfully started client")
	}
	var record *controlCompletion
	for _, entry := range driver.controls.clients {
		record = entry
	}
	driver.controls.mu.Unlock()
	select {
	case <-record.done:
		t.Fatal("unreaped client reported complete")
	default:
	}
	gateCtx, stopGate := context.WithTimeout(context.Background(), 20*time.Millisecond)
	if release, err := enterGateContext(gateCtx, driver.socket, false); err == nil {
		release()
		stopGate()
		t.Fatal("failed open released exclusive handshake before Wait")
	}
	stopGate()
	closeCtx, stopClose := context.WithTimeout(context.Background(), 20*time.Millisecond)
	began := time.Now()
	if err := driver.CloseControls(closeCtx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("unreaped close = %v", err)
	}
	stopClose()
	if time.Since(began) > time.Second {
		t.Fatal("failed-open cleanup tried to close through its own exclusive gate")
	}
	lateCtx, stopLate := context.WithTimeout(context.Background(), time.Second)
	if _, err := driver.OpenControlContext(lateCtx, "late"); err == nil || !strings.Contains(err.Error(), "stopped") {
		t.Fatalf("late control start = %v", err)
	}
	stopLate()
	releaseWait()
	confirmedCtx, stopConfirmed := context.WithTimeout(context.Background(), 3*time.Second)
	defer stopConfirmed()
	if err := driver.CloseControls(confirmedCtx); err != nil {
		t.Fatal(err)
	}
	// A bounded observer did not consume the only completion acknowledgement.
	if err := driver.CloseControls(confirmedCtx); err != nil {
		t.Fatal(err)
	}
	select {
	case <-record.done:
	default:
		t.Fatal("actual Wait/gate completion not observed")
	}
	release, err := enterGateContext(confirmedCtx, driver.socket, false)
	if err != nil {
		t.Fatal(err)
	}
	release()
}
