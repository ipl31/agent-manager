package main

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/YoanWai/agent-manager/internal/app"
	"github.com/YoanWai/agent-manager/internal/ui/effects"
	tea "github.com/charmbracelet/bubbletea"
)

func TestShutdownProcessHelper(t *testing.T) {
	args := os.Args
	marker := -1
	for i, arg := range args {
		if arg == "ownership-shutdown-helper" {
			marker = i
			break
		}
	}
	if marker < 0 {
		return
	}
	args = args[marker+1:]
	if args[0] == "writer" {
		fmt.Println("WRITER_READY")
		if bufio.NewScanner(os.Stdin).Scan() {
			if err := os.WriteFile(args[1], []byte("writer completed"), 0o600); err != nil {
				t.Fatal(err)
			}
			fmt.Println("WRITER_DONE")
		}
		os.Exit(0)
	}
	ctx := context.Background()
	if args[0] == "contender" {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, 200*time.Millisecond)
		defer cancel()
	}
	owner, err := app.AcquireOwnership(ctx, args[1], args[2], true, nil)
	if err != nil {
		fmt.Println("ERROR", err)
		os.Exit(0)
	}
	if args[0] == "contender" {
		fmt.Println("CONTENDER_READY")
		owner.Close()
		os.Exit(0)
	}
	scope := &effects.Lifetime{}
	writer := exec.Command(os.Args[0], "-test.run=^TestShutdownProcessHelper$", "--", "ownership-shutdown-helper", "writer", args[3])
	writer.Stdout, writer.Stderr = os.Stdout, os.Stderr
	input, err := writer.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	started := make(chan struct{})
	go effects.Leaf(scope, func() tea.Msg {
		if err := writer.Start(); err != nil {
			fmt.Println("CHILD_ERROR", err)
			close(started)
			return nil
		}
		close(started)
		if err := writer.Wait(); err != nil {
			fmt.Println("CHILD_ERROR", err)
		}
		return nil
	})()
	<-started
	fmt.Println("OWNER_READY")
	quit := make(chan struct{})
	var quitOnce sync.Once
	go func() {
		commands := bufio.NewScanner(os.Stdin)
		for commands.Scan() {
			switch commands.Text() {
			case "quit":
				quitOnce.Do(func() { close(quit) })
			case "release":
				fmt.Fprintln(input, "finish")
			}
		}
		input.Close()
		quitOnce.Do(func() { close(quit) })
	}()
	<-quit
	// The monitor has already stopped before a later contender publishes.
	// Completion is the actual writer cmd.Wait, not cancellation or a notice.
	scope.CloseAdmission()
	fmt.Println("MONITOR_STOPPED")
	shutdown := startRuntimeShutdown(scope.Join)
	forced := awaitRuntimeShutdown(shutdown.done, make(chan struct{}), false, 5*time.Second, func() { fmt.Println("WAITING") })
	if forced {
		t.Fatal("non-forced quit became forced")
	}
	owner.Close()
	fmt.Println("OWNER_RELEASED")
	os.Exit(0)
}

type shutdownProcess struct {
	cmd   *exec.Cmd
	input io.WriteCloser
	lines chan string
}

func startShutdownProcess(t *testing.T, role, profile, server, marker string) *shutdownProcess {
	t.Helper()
	cmd := exec.Command(os.Args[0], "-test.run=^TestShutdownProcessHelper$", "--", "ownership-shutdown-helper", role, profile, server, marker)
	env := []string{}
	for _, value := range os.Environ() {
		if !strings.HasPrefix(value, "TMUX=") && !strings.HasPrefix(value, "TMUX_PANE=") {
			env = append(env, value)
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
	process := &shutdownProcess{cmd: cmd, input: input, lines: make(chan string, 16)}
	go func() {
		scanner := bufio.NewScanner(output)
		for scanner.Scan() {
			process.lines <- scanner.Text()
		}
		close(process.lines)
	}()
	t.Cleanup(func() {
		input.Close()
		if err := cmd.Wait(); err != nil {
			t.Errorf("shutdown helper: %v", err)
		}
	})
	return process
}

func (p *shutdownProcess) expect(t *testing.T, prefix string) {
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

func TestOrdinaryQuitKeepsOwnerPastNoticeUntilActualChildWait(t *testing.T) {
	profile := t.TempDir()
	server := filepath.Join(t.TempDir(), "socket")
	marker := filepath.Join(t.TempDir(), "written")
	first := startShutdownProcess(t, "owner", profile, server, marker)
	seen := map[string]bool{}
	for len(seen) < 2 {
		select {
		case line := <-first.lines:
			if line != "OWNER_READY" && line != "WRITER_READY" {
				t.Fatalf("startup = %q", line)
			}
			seen[line] = true
		case <-time.After(5 * time.Second):
			t.Fatal("writer never started")
		}
	}
	fmt.Fprintln(first.input, "quit")
	first.expect(t, "MONITOR_STOPPED")
	first.expect(t, "WAITING")
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatalf("writer crossed barrier: %v", err)
	}
	later := startShutdownProcess(t, "contender", profile, server, marker)
	later.expect(t, "ERROR waiting for previous manager")
	select {
	case line := <-first.lines:
		t.Fatalf("owner exited before real child completion: %q", line)
	default:
	}
	fmt.Fprintln(first.input, "release")
	first.expect(t, "WRITER_DONE")
	first.expect(t, "OWNER_RELEASED")
	fresh := startShutdownProcess(t, "contender", profile, server, marker)
	fresh.expect(t, "CONTENDER_READY")
	if _, err := os.Stat(marker); err != nil {
		t.Fatalf("real writer did not finish: %v", err)
	}
}
