package git

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/YoanWai/agent-manager/internal/testenv"
)

// renameFixture is a managed worktree on am/old at tip A, with branch
// config of its own and a second branch "other" at the same commit.
type renameFixture struct {
	driver *Driver
	root   string
	path   string
	a      string
	b      string
}

func newRenameFixture(t *testing.T) renameFixture {
	t.Helper()
	driver, root := testRepo(t)
	write(t, root, "file", "A")
	commit(t, root, "A")
	path, branch, err := driver.AddWorktree(root, "old", "")
	if err != nil || branch != "am/old" {
		t.Fatalf("add: %q %v", branch, err)
	}
	gitIn(t, root, "config", "branch.am/old.description", "original")
	gitIn(t, root, "branch", "other", "am/old")
	a := revParse(t, driver, root, "am/old")
	write(t, root, "file", "B")
	commit(t, root, "B")
	return renameFixture{driver: driver, root: root, path: path, a: a, b: revParse(t, driver, root, "main")}
}

// renameState is everything a rename can change, read straight from git.
type renameState struct {
	Head, MainHead          string
	Old, New, Other         string
	OldConfig, NewConfig    string
	MainResolves, PathLocks bool
}

func (f renameFixture) state(t *testing.T) renameState {
	t.Helper()
	read := func(dir string, args ...string) string {
		out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).Output()
		if err != nil {
			return ""
		}
		return strings.TrimSpace(string(out))
	}
	head := func(dir string) string {
		if ref := read(dir, "symbolic-ref", "-q", "HEAD"); ref != "" {
			return ref
		}
		return "detached:" + read(dir, "rev-parse", "HEAD")
	}
	locks, _ := filepath.Glob(filepath.Join(f.root, ".git", "refs", "heads", "am", "*.lock"))
	return renameState{
		Head:         head(f.path),
		MainHead:     head(f.root),
		Old:          read(f.root, "rev-parse", "--verify", "-q", "refs/heads/am/old"),
		New:          read(f.root, "rev-parse", "--verify", "-q", "refs/heads/am/new"),
		Other:        read(f.root, "rev-parse", "--verify", "-q", "refs/heads/other"),
		OldConfig:    read(f.root, "config", "--get", "branch.am/old.description"),
		NewConfig:    read(f.root, "config", "--get", "branch.am/new.description"),
		MainResolves: read(f.root, "rev-parse", "--verify", "-q", "HEAD") != "",
		PathLocks:    len(locks) > 0,
	}
}

// wrapGit puts a shell wrapper in front of the driver's git. At the
// command boundary named by mode it runs compete once, as another git
// user would, and records in the returned trace whether that competitor
// succeeded. Every other invocation goes straight to git.
func (f renameFixture) wrapGit(t *testing.T, mode, compete string) string {
	t.Helper()
	testenv.SkipPOSIXShell(t)
	dir := t.TempDir()
	trace := filepath.Join(dir, "trace")
	t.Setenv("RENAME_REAL_GIT", f.driver.bin)
	t.Setenv("RENAME_TRACE", trace)
	t.Setenv("RENAME_MODE", mode)
	t.Setenv("RENAME_COMPETE", compete)
	t.Setenv("RENAME_ROOT", f.root)
	t.Setenv("RENAME_PATH", f.path)
	t.Setenv("RENAME_A", f.a)
	t.Setenv("RENAME_B", f.b)
	script := `#!/bin/sh
G="$RENAME_REAL_GIT"
compete() {
	[ -e "$RENAME_TRACE" ] && return 0
	if (unset GIT_DIR GIT_WORK_TREE GIT_INDEX_FILE GIT_COMMON_DIR GIT_PREFIX; eval "$RENAME_COMPETE") >/dev/null 2>&1
	then echo "competed at $*" >>"$RENAME_TRACE"
	else echo "competitor refused at $*" >>"$RENAME_TRACE"
	fi
}
cmd="$*"
case "$cmd" in "-c core.quotepath=false "*) cmd=${cmd#-c core.quotepath=false } ;; esac
if [ -n "${RENAME_LEGACY-}" ] && [ "$cmd" = "update-ref --no-deref --stdin" ]; then
	# Git 2.27 to 2.45: transactions, but no symref verbs.
	cat >/dev/null; echo "start: ok"; echo "fatal: unknown command: symref-verify" >&2; exit 128
fi
case "$RENAME_MODE:$cmd" in
after-copy:"branch -c "*)
	"$G" "$@"; status=$?
	[ $status -eq 0 ] && compete "$cmd"
	exit $status ;;
at-lock:"update-ref --stdin")
	compete "$cmd" ;;
at-head:"symbolic-ref -m "*|at-head:"update-ref --no-deref -m "*)
	compete "$cmd" ;;
eof:update-ref*--stdin*)
	# Git 2.26 reads all of stdin before it parses a command, and knows
	# no transaction verbs. The watchdog only bounds the old code.
	exec timeout 12 sh -c 'cat >/dev/null; echo "fatal: unknown command: start" >&2; exit 128' ;;
esac
exec "$G" "$@"
`
	wrapper := filepath.Join(dir, "git")
	if err := os.WriteFile(wrapper, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	f.driver.bin = wrapper
	return trace
}

func readTrace(t *testing.T, trace string) string {
	t.Helper()
	got, err := os.ReadFile(trace)
	if err != nil {
		t.Fatalf("the competing git command never ran: %v", err)
	}
	return strings.TrimSpace(string(got))
}

// installRefHook installs a reference-transaction hook for the repo.
func (f renameFixture) installRefHook(t *testing.T, body string) {
	t.Helper()
	testenv.SkipPOSIXShell(t)
	hooks := t.TempDir()
	script := "#!/bin/sh\nunset GIT_DIR GIT_WORK_TREE GIT_INDEX_FILE GIT_COMMON_DIR GIT_PREFIX\n" + body
	if err := os.WriteFile(filepath.Join(hooks, "reference-transaction"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	gitIn(t, f.root, "config", "core.hooksPath", hooks)
	t.Cleanup(func() { _ = exec.Command("git", "-C", f.root, "config", "--unset", "core.hooksPath").Run() })
}

// I1: another writer moves the copy before the rename reads a tip from
// it. The copy is no longer the rename's to delete.
func TestRenameWorktreeBranchKeepsADestinationAnotherWriterAdvanced(t *testing.T) {
	f := newRenameFixture(t)
	trace := f.wrapGit(t, "after-copy",
		`"$RENAME_REAL_GIT" -C "$RENAME_ROOT" update-ref refs/heads/am/new "$RENAME_B" &&
		 "$RENAME_REAL_GIT" -C "$RENAME_ROOT" config branch.am/new.description competing`)
	branch, err := f.driver.RenameWorktreeBranch(f.root, f.path, "am/old", "new")
	got := f.state(t)
	witness := readTrace(t, trace)
	if !strings.HasPrefix(witness, "competed") {
		t.Fatalf("competitor did not move the copy: %s", witness)
	}
	if err == nil || got.Head != "refs/heads/am/old" || got.Old != f.a || got.OldConfig != "original" ||
		got.New != f.b || got.NewConfig != "competing" {
		t.Fatalf("branch=%q err=%v\nstate=%+v\nA=%s B=%s", branch, err, got, f.a, f.b)
	}
}

// I2: the destination is deleted or moved after the rename settled on a
// tip and before HEAD moves.
func TestRenameWorktreeBranchNeverPointsHeadAtAChangedDestination(t *testing.T) {
	for _, tc := range []struct{ name, mode, compete string }{
		{"deleted before the lock", "at-lock", `"$RENAME_REAL_GIT" -C "$RENAME_ROOT" update-ref -d refs/heads/am/new`},
		{"moved before the lock", "at-lock", `"$RENAME_REAL_GIT" -C "$RENAME_ROOT" update-ref refs/heads/am/new "$RENAME_B"`},
		{"deleted at the HEAD write", "at-head", `"$RENAME_REAL_GIT" -C "$RENAME_ROOT" update-ref -d refs/heads/am/new`},
		{"moved at the HEAD write", "at-head", `"$RENAME_REAL_GIT" -C "$RENAME_ROOT" update-ref refs/heads/am/new "$RENAME_B"`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newRenameFixture(t)
			trace := f.wrapGit(t, tc.mode, tc.compete)
			branch, err := f.driver.RenameWorktreeBranch(f.root, f.path, "am/old", "new")
			got := f.state(t)
			witness := readTrace(t, trace)
			report := fmt.Sprintf("branch=%q err=%v witness=%q\nstate=%+v\nA=%s B=%s", branch, err, witness, got, f.a, f.b)
			competed := strings.HasPrefix(witness, "competed")
			switch {
			case competed:
				// The competitor won: HEAD stays on the source, which keeps
				// its tip, and whatever the competitor left at the
				// destination is still there.
				wantNew := ""
				if strings.Contains(tc.name, "moved") {
					wantNew = f.b
				}
				if err == nil || got.Head != "refs/heads/am/old" || got.Old != f.a || got.OldConfig != "original" || got.New != wantNew {
					t.Fatal(report)
				}
			default:
				// The rename held the destination and the competitor was
				// refused: the rename completes at A.
				if err != nil || branch != "am/new" || got.Head != "refs/heads/am/new" || got.New != f.a || got.Old != "" {
					t.Fatal(report)
				}
			}
		})
	}
}

// I3: the old ref cannot be deleted after HEAD moved. The rename has
// happened, and the caller must hear that something was left behind.
func TestRenameWorktreeBranchReportsAnOldRefItCouldNotDelete(t *testing.T) {
	f := newRenameFixture(t)
	f.installRefHook(t, `[ "$1" = prepared ] || exit 0
[ "$(git -C '`+f.path+`' symbolic-ref -q HEAD)" = refs/heads/am/new ] || exit 0
while read -r old new ref; do
	if [ "$ref" = refs/heads/am/old ] && [ "$new" = 0000000000000000000000000000000000000000 ]; then
		echo rejected >>'`+filepath.Join(f.root, ".git", "hook-trace")+`'
		exit 1
	fi
done
`)
	branch, err := f.driver.RenameWorktreeBranch(f.root, f.path, "am/old", "new")
	got := f.state(t)
	if _, readErr := os.Stat(filepath.Join(f.root, ".git", "hook-trace")); readErr != nil {
		t.Fatalf("hook never rejected the delete: %v (err=%v state=%+v)", readErr, err, got)
	}
	if err == nil || branch != "am/new" || got.Head != "refs/heads/am/new" || got.New != f.a || got.Old != f.a {
		t.Fatalf("branch=%q err=%v\nstate=%+v", branch, err, got)
	}
	if !strings.Contains(err.Error(), "am/old") {
		t.Fatalf("residual not named: %v", err)
	}
}

// I5: the user switches or detaches after the rename checked HEAD and
// before it writes HEAD. The user's checkout wins.
func TestRenameWorktreeBranchKeepsACheckoutMadeDuringTheRename(t *testing.T) {
	for _, tc := range []struct{ name, compete, head string }{
		{"switch at the same commit", `"$RENAME_REAL_GIT" -C "$RENAME_PATH" checkout -q other`, "refs/heads/other"},
		{"detach", `"$RENAME_REAL_GIT" -C "$RENAME_PATH" checkout -q --detach`, "detached:"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newRenameFixture(t)
			trace := f.wrapGit(t, "at-head", tc.compete)
			branch, err := f.driver.RenameWorktreeBranch(f.root, f.path, "am/old", "new")
			got := f.state(t)
			witness := readTrace(t, trace)
			want := tc.head
			if want == "detached:" {
				want += f.a
			}
			if !strings.HasPrefix(witness, "competed") || err == nil || got.Head != want ||
				got.Old != f.a || got.OldConfig != "original" || got.New != "" || got.NewConfig != "" {
				t.Fatalf("branch=%q err=%v witness=%q\nstate=%+v", branch, err, witness, got)
			}
		})
	}
}

// I7: the old branch is also checked out in the main worktree, which
// git allows with --ignore-other-worktrees. Deleting it would leave that
// checkout on a missing branch.
func TestRenameWorktreeBranchRefusesABranchCheckedOutElsewhere(t *testing.T) {
	f := newRenameFixture(t)
	gitIn(t, f.root, "checkout", "-q", "--ignore-other-worktrees", "am/old")
	branch, err := f.driver.RenameWorktreeBranch(f.root, f.path, "am/old", "new")
	got := f.state(t)
	if err == nil || got.Head != "refs/heads/am/old" || got.MainHead != "refs/heads/am/old" || !got.MainResolves ||
		got.Old != f.a || got.OldConfig != "original" || got.New != "" || got.NewConfig != "" {
		t.Fatalf("branch=%q err=%v\nstate=%+v", branch, err, got)
	}
}

// I8: Git before 2.27 reads update-ref --stdin to EOF and has no
// transaction verbs. The rename must not wait on a reply that never
// comes, and must not publish a copy it cannot hand HEAD to.
func TestRenameWorktreeBranchRefusesQuicklyWithoutRefTransactions(t *testing.T) {
	if _, err := exec.LookPath("timeout"); err != nil {
		t.Skip("needs timeout(1) to bound the unfixed code")
	}
	f := newRenameFixture(t)
	f.wrapGit(t, "eof", "true")
	start := time.Now()
	branch, err := f.driver.RenameWorktreeBranch(f.root, f.path, "am/old", "new")
	elapsed := time.Since(start)
	got := f.state(t)
	if elapsed > 8*time.Second || err == nil || got.Head != "refs/heads/am/old" || got.Old != f.a ||
		got.OldConfig != "original" || got.New != "" || got.NewConfig != "" {
		t.Fatalf("branch=%q err=%v elapsed=%s\nstate=%+v", branch, err, elapsed, got)
	}
}

// I8: a peer that stalls while holding the ref lock (here a
// reference-transaction hook) bounds the rename, which leaves no lock
// behind and keeps the worktree on its branch.
func TestRenameWorktreeBranchBoundsAStalledRefLock(t *testing.T) {
	f := newRenameFixture(t)
	f.installRefHook(t, `[ "$1" = prepared ] || exit 0
grep -q ' refs/heads/am/old$' || exit 0
exec sleep 12
`)
	start := time.Now()
	branch, err := f.driver.RenameWorktreeBranch(f.root, f.path, "am/old", "new")
	elapsed := time.Since(start)
	got := f.state(t)
	if elapsed > 9*time.Second || err == nil || got.Head != "refs/heads/am/old" || got.Old != f.a ||
		got.OldConfig != "original" || got.PathLocks {
		t.Fatalf("branch=%q err=%v elapsed=%s\nstate=%+v", branch, err, elapsed, got)
	}
}

// Git before 2.46 has no conditional symref write. The rename still keeps
// HEAD on a live branch, carries the config, and leaves a checkout made
// before it re-reads HEAD alone.
func TestRenameWorktreeBranchWithoutSymrefTransactions(t *testing.T) {
	t.Run("rename", func(t *testing.T) {
		f := newRenameFixture(t)
		f.wrapGit(t, "none", "true")
		t.Setenv("RENAME_LEGACY", "1")
		branch, err := f.driver.RenameWorktreeBranch(f.root, f.path, "am/old", "new")
		got := f.state(t)
		if err != nil || branch != "am/new" || got.Head != "refs/heads/am/new" || got.New != f.a || got.Old != "" ||
			got.NewConfig != "original" || got.OldConfig != "" {
			t.Fatalf("branch=%q err=%v\nstate=%+v", branch, err, got)
		}
	})
	t.Run("switch before the HEAD write", func(t *testing.T) {
		f := newRenameFixture(t)
		trace := f.wrapGit(t, "at-lock", `"$RENAME_REAL_GIT" -C "$RENAME_PATH" checkout -q other`)
		t.Setenv("RENAME_LEGACY", "1")
		branch, err := f.driver.RenameWorktreeBranch(f.root, f.path, "am/old", "new")
		got := f.state(t)
		witness := readTrace(t, trace)
		if !strings.HasPrefix(witness, "competed") || err == nil || got.Head != "refs/heads/other" ||
			got.Old != f.a || got.OldConfig != "original" || got.New != "" || got.NewConfig != "" {
			t.Fatalf("branch=%q err=%v witness=%q\nstate=%+v", branch, err, witness, got)
		}
	})
}
