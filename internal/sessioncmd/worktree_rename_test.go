package sessioncmd

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/YoanWai/agent-manager/internal/git"
	"github.com/YoanWai/agent-manager/internal/store"
	"github.com/YoanWai/agent-manager/internal/testenv"
)

// renameStore is one session row. save runs before the branch is stored
// and can fail it, standing in for a store that refuses the write.
type renameStore struct {
	mu   sync.Mutex
	row  store.Session
	save func(string) error
}

func (s *renameStore) ListSessions(bool) ([]store.Session, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return []store.Session{s.row}, nil
}

func (s *renameStore) RenameSessionWorktreeBranch(_ string, branch string) error {
	s.mu.Lock()
	save := s.save
	s.mu.Unlock()
	if save != nil {
		if err := save(branch); err != nil {
			return err
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.row.WorktreeBranch = branch
	return nil
}

func (s *renameStore) branch() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.row.WorktreeBranch
}

type renameRepo struct {
	driver     *git.Driver
	root, path string
	tip        string
	sess       store.Session
}

func newRenameRepo(t *testing.T, name string) renameRepo {
	t.Helper()
	driver, err := git.New()
	if err != nil {
		t.Skip("git unavailable")
	}
	root := t.TempDir()
	gitMust(t, root, "init", "-q", "-b", "main")
	gitMust(t, root, "-c", "user.name=t", "-c", "user.email=t@t", "commit", "-q", "--allow-empty", "--no-gpg-sign", "-m", "seed")
	path, branch, err := driver.AddWorktree(root, "old", "")
	if err != nil {
		t.Fatal(err)
	}
	gitMust(t, root, "config", "branch.am/old.description", "original")
	return renameRepo{
		driver: driver, root: root, path: path, tip: gitMust(t, root, "rev-parse", "HEAD"),
		sess: store.Session{ID: "rename", Name: name, Cwd: path, WorktreeRepo: root, WorktreeBranch: branch},
	}
}

func gitMust(t *testing.T, dir string, args ...string) string {
	t.Helper()
	out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v: %s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

func gitRead(dir string, args ...string) string {
	out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

func (r renameRepo) ref(name string) string {
	return gitRead(r.root, "rev-parse", "--verify", "-q", "refs/heads/"+name)
}

// I3: git could not delete the old ref after HEAD moved. The rename
// happened, so the row records it, and the caller hears what was left.
func TestRenameWorktreeBranchRecordsARenameThatLeftTheOldRef(t *testing.T) {
	testenv.SkipPOSIXShell(t)
	r := newRenameRepo(t, "old")
	hooks := t.TempDir()
	script := `#!/bin/sh
[ "$1" = prepared ] || exit 0
unset GIT_DIR GIT_WORK_TREE GIT_INDEX_FILE GIT_COMMON_DIR GIT_PREFIX
[ "$(git -C '` + r.path + `' symbolic-ref -q HEAD)" = refs/heads/am/new ] || exit 0
while read -r old new ref; do
	[ "$ref" = refs/heads/am/old ] && [ "$new" = 0000000000000000000000000000000000000000 ] && exit 1
done
exit 0
`
	if err := os.WriteFile(filepath.Join(hooks, "reference-transaction"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	gitMust(t, r.root, "config", "core.hooksPath", hooks)
	st := &renameStore{row: r.sess}
	err := RenameWorktreeBranch(r.driver, st, &r.sess, "new")
	head := gitRead(r.path, "symbolic-ref", "HEAD")
	if err == nil || !strings.Contains(err.Error(), "am/old") || r.sess.WorktreeBranch != "am/new" || st.branch() != "am/new" ||
		head != "refs/heads/am/new" || r.ref("am/new") != r.tip || r.ref("am/old") != r.tip {
		t.Fatalf("err=%v session=%q stored=%q HEAD=%q old=%q new=%q", err, r.sess.WorktreeBranch, st.branch(), head, r.ref("am/old"), r.ref("am/new"))
	}
}

// I4: the store refuses the new branch, and by then another checkout
// has taken it. Renaming it back would delete a branch in use.
func TestRenameWorktreeBranchRollbackLeavesABranchAnotherCheckoutTook(t *testing.T) {
	r := newRenameRepo(t, "old")
	st := &renameStore{row: r.sess, save: func(string) error {
		gitMust(t, r.root, "checkout", "-q", "--ignore-other-worktrees", "am/new")
		return errors.New("store write rejected")
	}}
	err := RenameWorktreeBranch(r.driver, st, &r.sess, "new")
	head := gitRead(r.path, "symbolic-ref", "HEAD")
	mainHead := gitRead(r.root, "symbolic-ref", "HEAD")
	if err == nil || !strings.Contains(err.Error(), "store write rejected") || !strings.Contains(err.Error(), "am/new") ||
		head != "refs/heads/am/new" || mainHead != "refs/heads/am/new" || r.ref("am/new") != r.tip ||
		gitRead(r.root, "rev-parse", "--verify", "-q", "HEAD") == "" {
		t.Fatalf("err=%v stored=%q HEAD=%q main HEAD=%q old=%q new=%q", err, st.branch(), head, mainHead, r.ref("am/old"), r.ref("am/new"))
	}
}

// I4: the rollback after a refused store write goes back to the branch
// the row records, which need not be the one the display name maps to.
func TestRenameWorktreeBranchRollbackRestoresTheRecordedBranch(t *testing.T) {
	r := newRenameRepo(t, "Display Name")
	st := &renameStore{row: r.sess, save: func(string) error { return errors.New("store write rejected") }}
	err := RenameWorktreeBranch(r.driver, st, &r.sess, "new")
	head := gitRead(r.path, "symbolic-ref", "HEAD")
	if err == nil || !strings.Contains(err.Error(), "store write rejected") || head != "refs/heads/am/old" ||
		r.ref("am/old") != r.tip || r.ref("am/new") != "" || r.ref("am/Display-Name") != "" ||
		st.branch() != "am/old" || r.sess.WorktreeBranch != "am/old" ||
		gitRead(r.root, "config", "--get", "branch.am/old.description") != "original" {
		t.Fatalf("err=%v stored=%q HEAD=%q old=%q new=%q display=%q", err, st.branch(), head, r.ref("am/old"), r.ref("am/new"), r.ref("am/Display-Name"))
	}
}

// I6: the UI and the mailbox rename the same session at once, the second
// from a row read before the first landed. They apply one after the
// other, so the branch, the row and the caller end on the last name.
func TestRenameWorktreeBranchSerializesOverlappingRenames(t *testing.T) {
	r := newRenameRepo(t, "old")
	stale := r.sess
	saving, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	st := &renameStore{row: r.sess}
	st.save = func(branch string) error {
		if branch == "am/winner" {
			once.Do(func() { close(saving) })
			<-release
		}
		return nil
	}
	first := make(chan error, 1)
	go func() { first <- RenameWorktreeBranch(r.driver, st, &r.sess, "winner") }()
	<-saving
	second := make(chan error, 1)
	go func() { second <- RenameWorktreeBranch(r.driver, st, &stale, "stale") }()
	close(release)
	firstErr, secondErr := <-first, <-second
	head := gitRead(r.path, "symbolic-ref", "HEAD")
	if firstErr != nil || secondErr != nil || head != "refs/heads/am/stale" || st.branch() != "am/stale" ||
		stale.WorktreeBranch != "am/stale" || r.ref("am/stale") != r.tip ||
		r.ref("am/winner") != "" || r.ref("am/old") != "" ||
		gitRead(r.root, "config", "--get", "branch.am/stale.description") != "original" {
		t.Fatalf("first=%v second=%v HEAD=%q stored=%q stale caller=%q old=%q winner=%q stale=%q",
			firstErr, secondErr, head, st.branch(), stale.WorktreeBranch, r.ref("am/old"), r.ref("am/winner"), r.ref("am/stale"))
	}
}
