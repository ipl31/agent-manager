package execution

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/YoanWai/agent-manager/internal/config"
	"github.com/YoanWai/agent-manager/internal/hooks"
	"github.com/YoanWai/agent-manager/internal/status"
	"github.com/YoanWai/agent-manager/internal/store"
	"github.com/YoanWai/agent-manager/internal/sysstat"
	"github.com/YoanWai/agent-manager/internal/testenv"
	"github.com/YoanWai/agent-manager/internal/tmux"
)

func TestAgentCPUUsesTreeSampleTimes(t *testing.T) {
	testenv.SkipPOSIXShell(t)
	fixture := t.TempDir()
	for name, script := range map[string]string{
		"ps": `#!/bin/sh
printf '123 1 0.0 1024 %s S\n' "$AM_TEST_TREE_CPU"
`,
		tmux.Binary: `#!/bin/sh
case "$*" in
	*-V) printf 'tmux 3.5\n' ;;
	*list-panes*) printf 'am_stats 0 123 80 24 1 0 /dev/null \n' ;;
	*capture-pane*)
		if [ -n "$AM_TEST_CAPTURE_MARK" ]; then
			sleep 0.1
			printf 'done\n' > "$AM_TEST_CAPTURE_MARK"
		fi
		for boundary do :; done
		printf 'pane contents\n%s\n' "$boundary"
		;;
	*display-message*) printf '/fake/poll-cpu.sock\n' ;;
	*) exit 1 ;;
esac
`,
	} {
		if err := os.WriteFile(filepath.Join(fixture, name), []byte(script), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", fixture+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("AM_TEST_TREE_CPU", "1.00")
	t.Setenv("AM_TEST_CAPTURE_MARK", "")
	driver, err := tmux.NewWithSocket(testSocket)
	if err != nil {
		t.Fatal(err)
	}
	st, err := store.Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	if err := st.CreateSession(store.Session{ID: "stats", Name: "stats", TmuxSocket: driver.SocketPath()}); err != nil {
		t.Fatal(err)
	}
	engine, err := status.NewEngine(config.Config{})
	if err != nil {
		t.Fatal(err)
	}
	runner := New(Dependencies{Store: st, TMux: driver, Engine: engine, Hooks: hooks.NewManager(t.TempDir())}, Options{})
	runner.selectedID = "stats"
	if result := runner.Step(); result.Err != nil || !result.Snapshot.Proc.OK {
		t.Fatalf("first sample = %+v, err = %v", result.Snapshot.Proc, result.Err)
	}
	firstSampleAt := runner.prevTreeAt

	// Give the two tree samples enough separation to use the delta path.
	time.Sleep(60 * time.Millisecond)
	t.Setenv("AM_TEST_TREE_CPU", "1.015625")
	captureMark := filepath.Join(fixture, "capture-done")
	t.Setenv("AM_TEST_CAPTURE_MARK", captureMark)
	result := runner.Step()
	if result.Err != nil || !result.Snapshot.Proc.OK {
		t.Fatalf("second sample = %+v, err = %v", result.Snapshot.Proc, result.Err)
	}
	capture, err := os.Stat(captureMark)
	if err != nil {
		t.Fatal(err)
	}
	if !runner.prevTreeAt.Before(capture.ModTime()) {
		t.Fatalf("tree sample at %v should precede delayed capture at %v", runner.prevTreeAt, capture.ModTime())
	}
	want := sysstat.HostCPUFromDelta(1.015625-1.0, runner.prevTreeAt.Sub(firstSampleAt).Seconds(), sysstat.LogicalCPUs())
	if got := result.Snapshot.Proc.CPUPercent; got != want {
		t.Errorf("selected CPU = %v, want %v", got, want)
	}
	if got := result.Snapshot.Agents.CPU; got != want {
		t.Errorf("fleet CPU = %v, want %v", got, want)
	}
}
