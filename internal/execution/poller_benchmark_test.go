package execution

import (
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"github.com/YoanWai/agent-manager/internal/hooks"
	"github.com/YoanWai/agent-manager/internal/notify"
	"github.com/YoanWai/agent-manager/internal/sessioncmd"
	"github.com/YoanWai/agent-manager/internal/status"
	"github.com/YoanWai/agent-manager/internal/store"
	"github.com/YoanWai/agent-manager/internal/testenv"
	"github.com/YoanWai/agent-manager/internal/tmux"
)

func BenchmarkPollStepVisibleFleet(b *testing.B) {
	testenv.SkipPOSIXShell(b)
	for _, count := range []int{1, 4, 11} {
		b.Run(fmt.Sprintf("sessions%d", count), func(b *testing.B) {
			driver, err := tmux.NewWithSocket(testSocket)
			if err != nil {
				b.Skip(err)
			}
			st, err := store.Open(filepath.Join(b.TempDir(), "state.db"))
			if err != nil {
				b.Fatal(err)
			}
			b.Cleanup(func() { st.Close() })
			p := observationRunner(b)
			p.store, p.tmux, p.hooks = st, driver, hooks.NewManager(b.TempDir())
			p.inboxOwner = sessioncmd.NewInboxOwner(st)
			p.reportStyles = map[string]string{"claude": "none"}
			p.prevTreeCPU = map[int]float64{}
			p.followFailed = map[string]string{}
			p.notifyFn = func(notify.Event) {}
			p.commandFn = func(notify.Event, string, error) {}
			pane := "❯ user request\n\n⏺ " + strings.Repeat("\x1b[31mA colored reply with words.\x1b[39m\n", 30) + "\n❯ \n"
			for i := range count {
				id := fmt.Sprintf("reusebench%d-%d", count, i)
				dir := b.TempDir()
				command := "printf '%s' " + tmux.ShellQuote(pane) + "; exec cat"
				if err := driver.Create(id, dir, command, nil, 100, 50); err != nil {
					b.Fatal(err)
				}
				b.Cleanup(func() { driver.Kill(id) })
				if err := st.CreateSession(store.Session{ID: id, Name: id, Tool: "claude", Cwd: dir, Group: "bench", Status: status.Idle}); err != nil {
					b.Fatal(err)
				}
			}
			if result := p.Step(); result.Err != nil {
				b.Fatal(result.Err)
			}
			b.ReportAllocs()
			b.ResetTimer()
			for range b.N {
				if result := p.Step(); result.Err != nil {
					b.Fatal(result.Err)
				}
			}
		})
	}
}
