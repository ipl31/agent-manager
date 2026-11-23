package app

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"github.com/YoanWai/agent-manager/internal/store"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestOwnershipHelper(t *testing.T) {
	args := os.Args
	split := -1
	for i, arg := range args {
		if arg == "--ownership-helper" {
			split = i
			break
		}
	}
	if split < 0 {
		return
	}
	args = args[split+1:]
	owner, err := AcquireOwnership(context.Background(), args[1], args[2], args[0] == "tui", nil)
	if err != nil {
		fmt.Println("ERROR", err)
		os.Exit(0)
	}
	fmt.Println("READY")
	commands := bufio.NewScanner(os.Stdin)
	for commands.Scan() {
		switch commands.Text() {
		case "changed":
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			err := owner.Changed(ctx)
			cancel()
			fmt.Println("CHANGED", err)
		case "retain":
			owner.RetainUntilExit(owner.store)
			owner = nil
			runtime.GC()
			fmt.Println("RETAINED")
		case "release":
			owner.Close()
			fmt.Println("RELEASED")
			os.Exit(0)
		case "crash":
			os.Exit(0)
		}
	}
	os.Exit(0)
}

type ownerProcess struct {
	cmd   *exec.Cmd
	input io.WriteCloser
	lines chan string
}

func startOwnerProcess(t *testing.T, role, profile, server string) *ownerProcess {
	t.Helper()
	cmd := exec.Command(os.Args[0], "-test.run=^TestOwnershipHelper$", "--", "--ownership-helper", role, profile, server)
	var env []string
	for _, item := range os.Environ() {
		if !strings.HasPrefix(item, "TMUX=") && !strings.HasPrefix(item, "TMUX_PANE=") {
			env = append(env, item)
		}
	}
	cmd.Env = append(env, "SHELL=/bin/sh")
	input, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	output, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	p := &ownerProcess{cmd: cmd, input: input, lines: make(chan string, 8)}
	go func() {
		scanner := bufio.NewScanner(output)
		for scanner.Scan() {
			p.lines <- scanner.Text()
		}
		close(p.lines)
	}()
	t.Cleanup(func() {
		input.Close()
		if err := cmd.Wait(); err != nil {
			t.Errorf("helper: %v", err)
		}
	})
	return p
}

func (p *ownerProcess) say(t *testing.T, command string) {
	t.Helper()
	if _, err := fmt.Fprintln(p.input, command); err != nil {
		t.Fatal(err)
	}
}
func (p *ownerProcess) expect(t *testing.T, prefix string) {
	t.Helper()
	select {
	case line := <-p.lines:
		if !strings.HasPrefix(line, prefix) {
			t.Fatalf("helper = %q, want %q", line, prefix)
		}
	case <-time.After(8 * time.Second):
		t.Fatalf("timed out waiting for %q", prefix)
	}
}

func TestOwnershipProcessTakeoverWaitsForLifetimeRelease(t *testing.T) {
	profile := t.TempDir()
	server := filepath.Join(t.TempDir(), "socket")
	first := startOwnerProcess(t, "tui", profile, server)
	first.expect(t, "READY")
	second := startOwnerProcess(t, "tui", profile, server)
	first.say(t, "changed")
	first.expect(t, "CHANGED another manager took over")
	select {
	case line := <-second.lines:
		t.Fatalf("successor started before drain barrier: %s", line)
	case <-time.After(250 * time.Millisecond):
	}
	first.say(t, "release")
	first.expect(t, "RELEASED")
	second.expect(t, "READY")
	second.say(t, "release")
	second.expect(t, "RELEASED")
}

func TestOwnershipRetainedHandleSurvivesGCAndCrashReleasesIt(t *testing.T) {
	profile := t.TempDir()
	server := filepath.Join(t.TempDir(), "socket")
	first := startOwnerProcess(t, "serve", profile, server)
	first.expect(t, "READY")
	first.say(t, "retain")
	first.expect(t, "RETAINED")
	second := startOwnerProcess(t, "tui", profile, server)
	select {
	case line := <-second.lines:
		t.Fatalf("uncertain owner unlocked by GC: %s", line)
	case <-time.After(300 * time.Millisecond):
	}
	first.say(t, "crash")
	second.expect(t, "READY")
	second.say(t, "release")
	second.expect(t, "RELEASED")
}

func TestOwnershipServeNeverDisplacesInteractiveManager(t *testing.T) {
	profile := t.TempDir()
	server := filepath.Join(t.TempDir(), "socket")
	first := startOwnerProcess(t, "tui", profile, server)
	first.expect(t, "READY")
	second := startOwnerProcess(t, "serve", profile, server)
	second.expect(t, "ERROR another manager is active")
	first.say(t, "release")
	first.expect(t, "RELEASED")
}

func TestOwnershipSeparatesServersAndProfiles(t *testing.T) {
	profile := t.TempDir()
	server := filepath.Join(t.TempDir(), "socket")
	seeded, err := store.Open(filepath.Join(profile, "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	if err := seeded.Close(); err != nil {
		t.Fatal(err)
	}

	owners := []*ownerProcess{
		startOwnerProcess(t, "tui", profile, server),
		startOwnerProcess(t, "tui", profile, server+"-other"),
		startOwnerProcess(t, "tui", t.TempDir(), server),
	}
	for _, p := range owners {
		p.expect(t, "READY")
	}
	for _, p := range owners {
		p.say(t, "release")
		p.expect(t, "RELEASED")
	}
}

func TestOwnershipServeDefersToPreviouslyPublishedPendingRequest(t *testing.T) {
	profile := t.TempDir()
	server := filepath.Join(t.TempDir(), "socket")
	first, err := AcquireOwnership(context.Background(), profile, server, true, nil)
	if err != nil {
		t.Fatal(err)
	}
	request, err := first.ReserveContinuation(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	first.ReleaseContinuation()
	if owner, err := AcquireOwnership(context.Background(), profile, server, false, nil); !errors.Is(err, ErrManagerBusy) {
		if owner != nil {
			owner.Close()
		}
		t.Fatalf("serve bypassed pending continuation: %v", err)
	}
	next, err := AcquireOwnership(context.Background(), profile, server, true, &request)
	if err != nil {
		t.Fatal(err)
	}
	next.Close()
}

func TestOwnershipNewestContenderWinsWithoutOverlappingHolder(t *testing.T) {
	profile := t.TempDir()
	server := filepath.Join(t.TempDir(), "socket")
	first := startOwnerProcess(t, "tui", profile, server)
	first.expect(t, "READY")
	st, err := store.Open(filepath.Join(profile, "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	identity := anchorKeyForTest(t, server)
	_, err = st.ManagerRequest(context.Background(), identity)
	if err != nil {
		t.Fatal(err)
	}
	waitGeneration := func(want int64) {
		t.Helper()
		deadline := time.Now().Add(5 * time.Second)
		for {
			r, err := st.ManagerRequest(context.Background(), identity)
			if err != nil {
				t.Fatal(err)
			}
			if r.Generation >= want {
				return
			}
			if time.Now().After(deadline) {
				t.Fatalf("request %d never published", want)
			}
			time.Sleep(5 * time.Millisecond)
		}
	}
	second := startOwnerProcess(t, "tui", profile, server)
	waitGeneration(2)
	third := startOwnerProcess(t, "tui", profile, server)
	waitGeneration(3)
	second.expect(t, "ERROR another manager took over")
	first.say(t, "changed")
	first.expect(t, "CHANGED another manager took over")
	first.say(t, "release")
	first.expect(t, "RELEASED")
	third.expect(t, "READY")
	third.say(t, "release")
	third.expect(t, "RELEASED")
}

func TestOwnershipSupersededContinuationNeverPublishesAnotherGeneration(t *testing.T) {
	profile := t.TempDir()
	server := filepath.Join(t.TempDir(), "socket")
	old, err := AcquireOwnership(context.Background(), profile, server, true, nil)
	if err != nil {
		t.Fatal(err)
	}
	request, err := old.ReserveContinuation(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	old.ReleaseContinuation()
	newest, err := AcquireOwnership(context.Background(), profile, server, true, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer newest.Close()
	if resumed, err := AcquireOwnership(context.Background(), profile, server, true, &request); !errors.Is(err, ErrManagerSuperseded) {
		if resumed != nil {
			resumed.Close()
		}
		t.Fatalf("old continuation = %v", err)
	}
	current, err := newest.store.ManagerRequest(context.Background(), newest.request.Server)
	if err != nil || current.Generation != newest.request.Generation || current.State != store.ManagerAcquired {
		t.Fatalf("continuation changed new owner: %+v %v", current, err)
	}
}

func TestOwnershipExpiredRequestAndFailedRestartDoNotBarServe(t *testing.T) {
	profile := t.TempDir()
	server := filepath.Join(t.TempDir(), "socket")
	owner, err := AcquireOwnership(context.Background(), profile, server, true, nil)
	if err != nil {
		t.Fatal(err)
	}
	if ok, err := owner.store.ReserveManager(context.Background(), owner.request, time.Second); err != nil || !ok {
		t.Fatalf("short reservation = %v %v", ok, err)
	}
	owner.ReleaseContinuation()
	if serve, err := AcquireOwnership(context.Background(), profile, server, false, nil); !errors.Is(err, ErrManagerBusy) {
		if serve != nil {
			serve.Close()
		}
		t.Fatalf("serve ignored pending priority: %v", err)
	}
	deadline := time.Now().Add(3 * time.Second)
	for {
		serve, err := AcquireOwnership(context.Background(), profile, server, false, nil)
		if err == nil {
			serve.Close()
			break
		}
		if !errors.Is(err, ErrManagerBusy) || time.Now().After(deadline) {
			t.Fatalf("stale reservation blocked serve: %v", err)
		}
		time.Sleep(20 * time.Millisecond)
	}
}
