package sessioncmd

import (
	"errors"
	"fmt"
	"sync"

	"github.com/YoanWai/agent-manager/internal/git"
	"github.com/YoanWai/agent-manager/internal/store"
)

type worktreeRenameStore interface {
	ListSessions(bool) ([]store.Session, error)
	RenameSessionWorktreeBranch(string, string) error
}

// worktreeRenames runs one branch rename at a time. The UI and the
// mailbox poller rename from the same manager process, and a rename that
// starts while another is between its git change and its store write
// would otherwise start from a branch that is already gone.
var worktreeRenames sync.Mutex

// RenameWorktreeBranch keeps a managed branch aligned with its
// session's display name without moving the directory of a live process.
// It starts from the branch the store records, not the caller's copy of
// the row, which can predate a rename that ran first. A
// *git.BranchCleanupError means the rename happened and was recorded, and
// names the old branch or config it could not remove.
func RenameWorktreeBranch(gitDrv *git.Driver, st worktreeRenameStore, sess *store.Session, name string) error {
	if gitDrv == nil || sess.WorktreeRepo == "" || sess.WorktreeBranch == "" {
		return nil
	}
	worktreeRenames.Lock()
	defer worktreeRenames.Unlock()
	sessions, err := st.ListSessions(true)
	if err != nil {
		return err
	}
	for _, row := range sessions {
		if row.ID == sess.ID {
			sess.Cwd, sess.WorktreeRepo, sess.WorktreeBranch = row.Cwd, row.WorktreeRepo, row.WorktreeBranch
		}
	}
	if sess.WorktreeRepo == "" || sess.WorktreeBranch == "" {
		return nil
	}
	for _, other := range sessions {
		if other.ID != sess.ID && other.Cwd == sess.Cwd {
			return fmt.Errorf("worktree is shared with session %q", other.Name)
		}
	}
	original := sess.WorktreeBranch
	branch, err := gitDrv.RenameWorktreeBranch(sess.WorktreeRepo, sess.Cwd, original, name)
	var cleanup *git.BranchCleanupError
	if err != nil && !errors.As(err, &cleanup) {
		return err
	}
	if branch == original {
		return err
	}
	if saveErr := st.RenameSessionWorktreeBranch(sess.ID, branch); saveErr != nil {
		// The restore is itself a guarded rename: it moves the worktree back
		// only while it is still on branch and no other checkout has taken
		// it, and otherwise reports where git and the store now differ.
		restored, restoreErr := gitDrv.RestoreWorktreeBranch(sess.WorktreeRepo, sess.Cwd, branch, original)
		if restoreErr != nil && !errors.As(restoreErr, &cleanup) {
			return fmt.Errorf("%w; the worktree stays on git branch %s, the session records %s: %w", saveErr, branch, original, restoreErr)
		}
		if restored != original {
			return fmt.Errorf("%w; git branch rollback returned %s instead of %s", saveErr, restored, original)
		}
		return errors.Join(saveErr, restoreErr)
	}
	sess.WorktreeBranch = branch
	return err
}
