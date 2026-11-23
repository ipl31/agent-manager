//go:build !windows

package ui

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/YoanWai/agent-manager/internal/git"
	uirail "github.com/YoanWai/agent-manager/internal/ui/rail"
	uilaunch "github.com/YoanWai/agent-manager/internal/ui/spawn/launch"
)

func TestBackgroundFetchDoesNotHoldMainLaneAndCannotStartAfterQuit(t *testing.T) {
	m := buildModel(t)
	dir := t.TempDir()
	started, release := filepath.Join(dir, "started"), filepath.Join(dir, "release")
	t.Setenv("AM_TEST_FETCH_STARTED", started)
	t.Setenv("AM_TEST_FETCH_RELEASE", release)
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	script := `#!/bin/sh
if [ "$1" = fetch ]; then
 : > "$AM_TEST_FETCH_STARTED"
 while [ ! -f "$AM_TEST_FETCH_RELEASE" ]; do sleep 0.01; done
 exit 0
fi
case "$*" in
 *--symbolic-full-name*) echo refs/remotes/origin/main;;
 *symbolic-ref*) echo refs/remotes/origin/main;;
 *remote*) echo origin;;
 *) echo main;;
esac
`
	if err := os.WriteFile(filepath.Join(dir, "git"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	driver, err := git.New()
	if err != nil {
		t.Fatal(err)
	}
	m.services.gitDrv = driver
	key := uilaunch.BaseFetchKey{Dir: dir, Override: "origin/main"}
	m.ledger.spawner.ReserveBaseFetch(key, time.Now())
	fetch := m.recordBaseFetch(uilaunch.BaseFetchedMsg{Key: key})
	finished := make(chan struct{})
	go func() { fetch(); close(finished) }()
	defer func() { _ = os.WriteFile(release, nil, 0o600); <-finished }()
	deadline := time.Now().Add(5 * time.Second)
	for {
		if _, err := os.Stat(started); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("fetch did not reach barrier")
		}
		time.Sleep(5 * time.Millisecond)
	}
	m.queueRail([]uirail.Mutation{{Kind: uirail.SaveCollapsed, Collapsed: []string{"independent"}}}, 0, false)
	if m.nextEffectCmd() == nil || m.effects.Main.Active == nil {
		t.Fatal("background fetch blocked independent main-lane work")
	}
	completed := m.effects.Main.Active.Command()
	m.Update(completed)
	if m.effects.Main.Active != nil {
		t.Fatal("main lane could not complete while fetch blocked")
	}
	m.requestQuit()
	late := m.recordBaseFetch(uilaunch.BaseFetchedMsg{Key: key})
	// The generated leaf remains guarded even when its completion arrived late.
	if late != nil {
		late()
	}
	if m.effects.background.Begin() {
		m.effects.background.Done()
		t.Fatal("quit left background admission open")
	}
}
