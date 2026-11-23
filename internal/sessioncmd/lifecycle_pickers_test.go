package sessioncmd

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/YoanWai/agent-manager/internal/config"
	"github.com/YoanWai/agent-manager/internal/status"
	"github.com/YoanWai/agent-manager/internal/store"
	"github.com/YoanWai/agent-manager/internal/testenv"
	"github.com/YoanWai/agent-manager/internal/tmux"
	"github.com/google/uuid"
)

func TestLifecyclePickerScopeAdmitsBeforeDispatchAndJoinsLastWrite(t *testing.T) {
	lifecycle := &Lifecycle{pickers: &pickerWorkers{}}
	entered, release := make(chan struct{}), make(chan struct{})
	if !lifecycle.pickers.start(func() { close(entered); <-release }) {
		t.Fatal("initial dispatch refused")
	}
	<-entered
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Millisecond)
	defer cancel()
	if err := lifecycle.ClosePickers(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("running picker falsely drained: %v", err)
	}
	late := false
	if lifecycle.pickers.start(func() { late = true }) {
		t.Fatal("late picker admitted")
	}
	close(release)
	if err := lifecycle.ClosePickers(context.Background()); err != nil {
		t.Fatal(err)
	}
	if late {
		t.Fatal("late picker wrote after close")
	}
}

func TestLifecycleReviveJoinsPickerReadinessAndSecondEnter(t *testing.T) {
	testenv.SkipPOSIXShell(t)
	for _, reuse := range []bool{false, true} {
		t.Run(map[bool]string{false: "new-pane", true: "reused-pane"}[reuse], func(t *testing.T) {
			h := newSessionHarness(t)
			runtime := harnessRuntime(t, h)
			ready := filepath.Join(t.TempDir(), "ready")
			scriptPath := filepath.Join(t.TempDir(), "picker.sh")
			script := "printf 'WAITING\\n'\nwhile [ ! -f " + tmux.ShellQuote(ready) + " ]; do sleep 0.02; done\nprintf '> '\nIFS= read -r keys\nprintf '> %s' \"$keys\"\nIFS= read -r again\nprintf '\\nOPENED %s\\n' \"$keys\"\nsleep 30\n"
			if err := os.WriteFile(scriptPath, []byte(script), 0o700); err != nil {
				t.Fatal(err)
			}
			runtime.Config.Tools["ownedpicker"] = config.Tool{Command: "bash " + tmux.ShellQuote(scriptPath), InputPrefix: "^>", ResumePickerKeys: "/sessions"}
			lifecycle, err := NewLifecycle(runtime)
			if err != nil {
				t.Fatal(err)
			}
			sess := store.Session{ID: uuid.NewString()[:8], Name: "owned-picker", Tool: "ownedpicker", Cwd: h.caller.Cwd, Group: h.caller.Group, Status: status.Dead}
			if err := h.store.CreateSession(sess); err != nil {
				t.Fatal(err)
			}
			if reuse {
				if err := h.driver.Create(sess.ID, sess.Cwd, "", nil, 80, 24); err != nil {
					t.Fatal(err)
				}
				waitForAgentGone(t, h.driver, sess.ID)
			}
			t.Cleanup(func() { _ = h.driver.Kill(sess.ID) })
			dispatch := lifecycle.Capture(runtime.Config, runtime.Snapshot)
			lateCapture := lifecycle.Capture(runtime.Config, runtime.Snapshot)
			started := time.Now()
			if _, err := dispatch.Revive(sess, PaneSize{80, 24}); err != nil {
				t.Fatal(err)
			}
			if time.Since(started) > 3*time.Second {
				t.Fatal("revive synchronously waited for picker readiness")
			}
			waitForPaneText(t, h.driver, sess.ID, "WAITING")
			shortJoin := func(stage string) {
				t.Helper()
				ctx, cancel := context.WithTimeout(context.Background(), 15*time.Millisecond)
				defer cancel()
				if err := lifecycle.ClosePickers(ctx); !errors.Is(err, context.DeadlineExceeded) {
					t.Fatalf("picker drained before %s: %v", stage, err)
				}
			}
			shortJoin("readiness")
			lateCapture.injectPickerKeys(h.driver, sess.ID, config.Tool{InputPrefix: "^>", ResumePickerKeys: "DO-NOT-SEND"})
			if lateCapture.pickers.start(func() {}) {
				t.Fatal("pre-existing capture admitted picker after owner closed")
			}
			if err := os.WriteFile(ready, nil, 0o600); err != nil {
				t.Fatal(err)
			}
			waitForPaneText(t, h.driver, sess.ID, "> /sessions")
			shortJoin("possible second Enter")
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			if err := lifecycle.ClosePickers(ctx); err != nil {
				t.Fatal(err)
			}
			pane := waitForPaneText(t, h.driver, sess.ID, "OPENED /sessions")
			if strings.Contains(pane, "DO-NOT-SEND") || strings.Count(pane, "OPENED /sessions") != 1 {
				t.Fatalf("picker opened more than once: %s", pane)
			}
		})
	}
}
