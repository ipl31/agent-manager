package sysstat

import (
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"testing"
)

func TestLookupChildNamesPreservesArgvZero(t *testing.T) {
	child := exec.Command("/bin/sleep", "5")
	child.Args[0] = "/tmp/agent manager/codex"
	if err := child.Start(); err != nil {
		t.Fatalf("start child: %v", err)
	}
	t.Cleanup(func() {
		child.Process.Kill()
		child.Wait()
	})

	wanted := []childRef{{pid: child.Process.Pid, parent: os.Getpid()}}
	got := lookupChildNames(wanted)
	if len(got) != 1 {
		t.Fatalf("lookupChildNames() = %v, want one child", got)
	}
	if got[0].command != child.Args[0] {
		t.Fatalf("command = %q, want %q", got[0].command, child.Args[0])
	}
}

func TestLookupChildNamesRejectsWrongParent(t *testing.T) {
	wanted := []childRef{{pid: os.Getpid(), parent: os.Getppid() + 1}}
	if got := lookupChildNames(wanted); len(got) != 0 {
		t.Fatalf("lookupChildNames() = %v, want none", got)
	}
}

func TestLookupChildNamesSkipsExitedChild(t *testing.T) {
	child := exec.Command("/usr/bin/true")
	if err := child.Run(); err != nil {
		t.Fatalf("run child: %v", err)
	}
	wanted := []childRef{{pid: child.Process.Pid, parent: os.Getpid()}}
	if got := lookupChildNames(wanted); len(got) != 0 {
		t.Fatalf("lookupChildNames() = %v, want none", got)
	}
}

func BenchmarkChildNames(b *testing.B) {
	children := make([]*exec.Cmd, 5)
	for i := range children {
		children[i] = exec.Command("/bin/sleep", "60")
		if err := children[i].Start(); err != nil {
			b.Fatalf("start child %d: %v", i, err)
		}
		b.Cleanup(func() {
			children[i].Process.Kill()
			children[i].Wait()
		})
	}

	for _, count := range []int{1, 2, 5} {
		wanted := make([]childRef, count)
		pids := make([]string, count)
		for i, child := range children[:count] {
			wanted[i] = childRef{pid: child.Process.Pid, parent: os.Getpid()}
			pids[i] = strconv.Itoa(child.Process.Pid)
		}
		b.Run(fmt.Sprintf("native/%d", count), func(b *testing.B) {
			for b.Loop() {
				if got := lookupChildNames(wanted); len(got) != count {
					b.Fatalf("named %d children, want %d", len(got), count)
				}
			}
		})
		for _, tc := range []struct {
			name string
			flag string
		}{
			{name: "ps", flag: "-o"},
			{name: "ps-x", flag: "-xo"},
		} {
			b.Run(fmt.Sprintf("%s/%d", tc.name, count), func(b *testing.B) {
				for b.Loop() {
					out, err := exec.Command("ps", tc.flag, "pid=,ppid=,args=", "-p", strings.Join(pids, ",")).Output()
					if err != nil {
						b.Fatal(err)
					}
					if rows := len(strings.Split(strings.TrimSpace(string(out)), "\n")); rows != count {
						b.Fatalf("ps returned %d rows, want %d", rows, count)
					}
				}
			})
		}
	}
}
