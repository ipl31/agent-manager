package execution

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/YoanWai/agent-manager/internal/testenv"
)

// A branch rename that moved HEAD but could not delete the old branch has
// happened: the agent hears its name was applied, the row follows, and
// the leftover branch is reported once by the pass that applied it.
func TestPendingRenameReportsABranchItLeftBehind(t *testing.T) {
	testenv.SkipPOSIXShell(t)
	m := buildModel(t)
	repo := seedRepo(t)
	spawned := createWorktreeSession(t, m, "claude-7a72", repo)
	hooks := t.TempDir()
	script := `#!/bin/sh
[ "$1" = prepared ] || exit 0
unset GIT_DIR GIT_WORK_TREE GIT_INDEX_FILE GIT_COMMON_DIR GIT_PREFIX
[ "$(git -C '` + spawned.Cwd + `' symbolic-ref -q HEAD)" = refs/heads/am/audit-the-poller ] || exit 0
while read -r old new ref; do
	[ "$ref" = refs/heads/` + spawned.WorktreeBranch + ` ] && [ "$new" = 0000000000000000000000000000000000000000 ] && exit 1
done
exit 0
`
	if err := os.WriteFile(filepath.Join(hooks, "reference-transaction"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command("git", "-C", repo, "config", "core.hooksPath", hooks).CombinedOutput(); err != nil {
		t.Fatalf("hooks: %v: %s", err, out)
	}
	request := writeName(t, m, spawned.ID, "audit the poller")

	sess := spawned
	err := m.poller.applyPendingRename(&sess)
	if err == nil || !strings.Contains(err.Error(), spawned.WorktreeBranch) {
		t.Fatalf("leftover branch not reported: %v", err)
	}
	verdict, found, readErr := m.hooks.ReadNameResult(spawned.ID, request)
	if readErr != nil || !found || verdict.Refusal != nil || verdict.Applied != "audit the poller" {
		t.Fatalf("verdict = %+v, %v, %v; want the name applied", verdict, found, readErr)
	}
	stored, getErr := m.store.Get(spawned.ID)
	if getErr != nil || stored.Name != "audit the poller" || stored.WorktreeBranch != "am/audit-the-poller" {
		t.Fatalf("stored = %+v, %v", stored, getErr)
	}
	if err := m.poller.applyPendingRename(&sess); err != nil {
		t.Fatalf("the leftover was reported again: %v", err)
	}
}
