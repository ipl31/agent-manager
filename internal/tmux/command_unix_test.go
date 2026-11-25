//go:build darwin || linux

package tmux

import (
	"strings"
	"syscall"
	"testing"
	"testing/synctest"
	"time"
)

func TestKillProcessGroupWaitsForLeaderBeforeTrustingEPERM(t *testing.T) {
	const leader = 123
	type call struct {
		pid    int
		signal syscall.Signal
		err    error
	}
	group := func(err error) call { return call{-leader, syscall.SIGKILL, err} }
	for name, script := range map[string][]call{
		"leader gone":          {group(nil), group(syscall.EPERM), {leader, 0, syscall.ESRCH}},
		"leader still present": {group(nil), group(syscall.EPERM), {leader, 0, nil}, group(syscall.ESRCH)},
	} {
		t.Run(name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				index := 0
				err := killProcessGroup(leader, func(pid int, signal syscall.Signal) error {
					if index == len(script) {
						t.Fatalf("unexpected signal %d to %d after script", signal, pid)
					}
					want := script[index]
					if pid != want.pid || signal != want.signal {
						t.Fatalf("call %d: signal %d to %d, want %d to %d", index, signal, pid, want.signal, want.pid)
					}
					index++
					return want.err
				})
				if err != nil || index != len(script) {
					t.Fatalf("killProcessGroup = %v after %d of %d calls", err, index, len(script))
				}
			})
		})
	}
}

func TestKillProcessGroupBoundsRetriesWhileLeaderIsPresent(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		const leader = 123
		started := time.Now()
		probes := 0
		initial := true
		err := killProcessGroup(leader, func(pid int, signal syscall.Signal) error {
			switch {
			case pid == leader && signal == 0:
				probes++
				return nil
			case pid == -leader && signal == syscall.SIGKILL:
				if initial {
					initial = false
					return nil
				}
				return syscall.EPERM
			default:
				t.Fatalf("unexpected signal %d to %d", signal, pid)
				return nil
			}
		})
		if err == nil || !strings.Contains(err.Error(), "still has members") || probes < 2 {
			t.Fatalf("killProcessGroup = %v after %d leader probes", err, probes)
		}
		if elapsed := time.Since(started); elapsed < 100*time.Millisecond || elapsed > 101*time.Millisecond {
			t.Fatalf("retry window = %v, want 100ms plus at most one retry interval", elapsed)
		}
	})
}
