package execution

import (
	"context"
	"errors"
	"github.com/YoanWai/agent-manager/internal/testenv"
	"github.com/YoanWai/agent-manager/internal/tmux"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/YoanWai/agent-manager/internal/notify"
	"github.com/YoanWai/agent-manager/internal/status"
)

func TestRunnerJoinsNativeCommandAndLateNotificationLog(t *testing.T) {
	p, sess, _, _ := newCommandTestPoller(t, "fixture-command")
	p.interval = time.Hour
	nativeStarted, releaseNative := make(chan struct{}), make(chan struct{})
	commandStarted, releaseCommand := make(chan struct{}), make(chan struct{})
	logStarted, releaseLog := make(chan struct{}), make(chan struct{})
	dir := t.TempDir()
	p.notifyFn = func(notify.Event) { close(nativeStarted); <-releaseNative }
	p.commandFn = func(event notify.Event, _ string, _ error) {
		close(commandStarted)
		<-releaseCommand
		close(logStarted)
		<-releaseLog
		notify.LogCommandFailure(dir, event, errors.New("fixture late failure"))
	}
	p.runMu.Lock()
	p.notifyTransition(sess, status.Waiting, "", "question")
	p.runMu.Unlock()
	<-nativeStarted
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	results := p.Run(ctx)
	assertPending := func(stage string) {
		t.Helper()
		select {
		case <-results:
			t.Fatalf("Runner completed before %s", stage)
		case <-time.After(15 * time.Millisecond):
		}
	}
	assertPending("native posting")
	close(releaseNative)
	<-commandStarted
	assertPending("configured command")
	close(releaseCommand)
	<-logStarted
	assertPending("late log write")
	close(releaseLog)
	select {
	case _, open := <-results:
		if open {
			t.Fatal("canceled Runner produced a result")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("notification completion did not release Runner")
	}
	raw, err := os.ReadFile(filepath.Join(dir, notify.CommandLog))
	if err != nil || len(raw) == 0 {
		t.Fatalf("late failure log missing: %q %v", raw, err)
	}
}

func TestRunnerDoesNotHoldOwnershipForIdleOldPIDClickWindow(t *testing.T) {
	p, sess, _, _ := newCommandTestPoller(t, "")
	p.interval = time.Hour
	posted, click, clicked := make(chan struct{}), make(chan struct{}), make(chan struct{})
	dir := t.TempDir()
	oldPID := os.Getpid() + 100000
	p.notifyFn = func(event notify.Event) {
		go func() { defer close(clicked); <-click; _ = notify.RequestFocus(dir, oldPID, event.ID) }()
		close(posted)
	}
	p.runMu.Lock()
	p.notifyTransition(sess, status.Waiting, "", "")
	p.runMu.Unlock()
	<-posted
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	results := p.Run(ctx)
	select {
	case _, open := <-results:
		if open {
			t.Fatal("canceled Runner produced a result")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("idle desktop click held Runner")
	}
	close(click)
	<-clicked
	if id, ok := notify.TakeFocus(dir); ok {
		t.Fatalf("new manager took old-PID click: %s", id)
	}
}

func TestRunnerJoinsActualNotifyCommandAndFailureLog(t *testing.T) {
	testenv.SkipPOSIXShell(t)
	p, sess, _, _ := newCommandTestPoller(t, "")
	p.interval = time.Hour
	dir := t.TempDir()
	t.Setenv("AGENT_MANAGER_CONFIG_DIR", dir)
	started, release := filepath.Join(dir, "started"), filepath.Join(dir, "release")
	script := filepath.Join(dir, "command.sh")
	body := ": > " + tmux.ShellQuote(started) + "\nwhile [ ! -f " + tmux.ShellQuote(release) + " ]; do sleep 0.02; done\nprintf 'fixture-command-failure' >&2\nexit 7\n"
	if err := os.WriteFile(script, []byte(body), 0o700); err != nil {
		t.Fatal(err)
	}
	defer os.WriteFile(release, nil, 0o600)
	if err := p.store.SetSetting(notifyCommandSetting, "sh "+tmux.ShellQuote(script)); err != nil {
		t.Fatal(err)
	}
	p.notifyFn = func(notify.Event) {}
	p.commandFn = runNotifyCommand
	p.runMu.Lock()
	p.notifyTransition(sess, status.Waiting, "", "")
	p.runMu.Unlock()
	deadline := time.Now().Add(4 * time.Second)
	for {
		if _, err := os.Stat(started); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("fixture command did not start")
		}
		time.Sleep(5 * time.Millisecond)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	results := p.Run(ctx)
	select {
	case <-results:
		t.Fatal("Runner closed while real notify child was blocked")
	case <-time.After(20 * time.Millisecond):
	}
	if err := os.WriteFile(release, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	select {
	case _, open := <-results:
		if open {
			t.Fatal("canceled Runner emitted result")
		}
	case <-time.After(4 * time.Second):
		t.Fatal("real notification/log did not complete")
	}
	raw, err := os.ReadFile(filepath.Join(dir, notify.CommandLog))
	if err != nil || !strings.Contains(string(raw), "fixture-command-failure") {
		t.Fatalf("command failure log=%q err=%v", raw, err)
	}
}
