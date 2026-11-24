package git

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"time"
)

// BranchCleanupError comes back, with the new branch, from a rename that
// moved HEAD but could not retire the old branch or its config. The
// rename has happened; Err names what was left behind.
type BranchCleanupError struct {
	Branch string
	Err    error
	// KeptTip is the commit the old branch was left at when the rename
	// could not delete it. Only this rename's caller knows the branch is
	// its own, so only it hands KeptTip back to RestoreWorktreeBranch.
	KeptTip string
}

func (e *BranchCleanupError) Error() string {
	return fmt.Sprintf("worktree is on %s, but %v", e.Branch, e.Err)
}

func (e *BranchCleanupError) Unwrap() error { return e.Err }

// refLockTimeout bounds each wait on git while a rename holds ref locks.
// Git takes or refuses a lock at once, so a longer wait is a stalled hook
// or a git that answers nothing until its input ends.
const refLockTimeout = 5 * time.Second

// RenameWorktreeBranch gives a managed worktree the branch a session of
// newName would have received at spawn without moving its live directory.
// The recorded branch is returned untouched when the worktree directory or
// branch no longer exists, or the worktree has left the recorded branch
// (switched to another, or detached mid-rebase), which is what a worktree
// renamed, removed or repointed by hand looks like. A *BranchCleanupError
// comes with the new branch when the rename happened but the old branch
// stayed; any other error means HEAD did not move.
func (d *Driver) RenameWorktreeBranch(root, path, branch, newName string) (string, error) {
	name := sanitizeWorktreeName(newName)
	if name == "" {
		return "", fmt.Errorf("session name %q leaves nothing usable for a worktree branch", newName)
	}
	return d.moveWorktreeBranch(root, path, branch, "am/"+name, "")
}

// RestoreWorktreeBranch moves a worktree that RenameWorktreeBranch put on
// branch back to original, by that exact name, with the same guarantees.
// keptTip is the BranchCleanupError.KeptTip of the rename being undone,
// or "". An original that rename kept at keptTip is taken back while it
// still holds that commit, the one branch is on, and the copy goes. Any
// other existing original, even at the same commit, belongs to someone
// else and is refused as a taken name.
func (d *Driver) RestoreWorktreeBranch(root, path, branch, original, keptTip string) (string, error) {
	return d.moveWorktreeBranch(root, path, branch, original, keptTip)
}

func (d *Driver) moveWorktreeBranch(root, path, branch, newBranch, keptTip string) (string, error) {
	if newBranch == branch {
		return branch, nil
	}
	if _, err := os.Stat(path); err != nil {
		if !os.IsNotExist(err) {
			return "", err
		}
		return branch, nil
	}
	currentBranch, err := d.run(path, "symbolic-ref", "--short", "-q", "HEAD")
	if err != nil {
		// -q exits 1 on a detached HEAD; anything else is a real failure.
		var exitErr *exec.ExitError
		if !errors.As(err, &exitErr) || exitErr.ExitCode() != 1 {
			return "", fmt.Errorf("read worktree branch: %w", err)
		}
		return branch, nil
	}
	// A rename whose store write failed and could not be undone leaves the
	// worktree already on newBranch; the caller records it this time.
	if currentBranch == newBranch {
		return newBranch, nil
	}
	if currentBranch != branch {
		return branch, nil
	}
	oldRef, newRef := "refs/heads/"+branch, "refs/heads/"+newBranch
	tip, err := d.refTip(root, oldRef)
	if err != nil || tip == "" {
		return branch, err
	}
	kept, err := d.refTip(root, newRef)
	if err != nil {
		return "", err
	}
	if kept != "" && (kept != keptTip || kept != tip) {
		return "", fmt.Errorf("branch already exists: %s", newBranch)
	}
	if kept != "" {
		if n, err := d.checkouts(root, newRef); err != nil {
			return "", err
		} else if n > 0 {
			return "", fmt.Errorf("branch %s is checked out in another worktree", newBranch)
		}
	}
	// Git lets a branch be checked out twice (--ignore-other-worktrees), and
	// deleting it would leave the other checkout on a missing branch.
	if n, err := d.checkouts(root, oldRef); err != nil {
		return "", err
	} else if n > 1 {
		return "", fmt.Errorf("branch %s is also checked out in another worktree", branch)
	}
	symref, err := d.refTransactions(path, oldRef)
	if err != nil {
		return "", err
	}
	if err := d.moveCheckedOutBranch(root, path, branch, newBranch, tip, symref, kept == ""); err != nil {
		var cleanup *BranchCleanupError
		if errors.As(err, &cleanup) {
			return newBranch, err
		}
		return "", err
	}
	return newBranch, nil
}

// moveCheckedOutBranch renames the branch checked out at path while git
// commands keep running there. `git branch -m` deletes the old ref before
// it repoints HEAD, so a reader in that gap finds HEAD naming a missing
// branch and fails. Copying first, then repointing HEAD, then deleting the
// old ref keeps HEAD on a branch that exists at every step. Both refs stay
// locked at tip, read from the source before the copy existed, while HEAD
// moves: a commit racing the rename lands on one branch or fails to lock,
// and a destination another writer moved or deleted stops the rename.
// Without publish, newBranch already exists at tip (an original a failed
// cleanup kept) and stays if HEAD cannot move onto it.
func (d *Driver) moveCheckedOutBranch(root, path, branch, newBranch, tip string, symref, publish bool) error {
	oldRef, newRef := "refs/heads/"+branch, "refs/heads/"+newBranch
	undo := func() error { return nil }
	if publish {
		undo = func() error { return d.dropBranch(root, newBranch, tip) }
		// -c carries the branch config and reflog, as -m does. It can copy
		// the ref and then fail on the config, which leaves a copy to undo.
		if _, err := d.run(root, "branch", "-c", branch, newBranch); err != nil {
			if copied, lookErr := d.refTip(root, newRef); lookErr != nil || copied == "" {
				return errors.Join(err, lookErr)
			}
			return errors.Join(err, undo())
		}
	}
	release, err := d.holdRefs(root, tip, oldRef, newRef)
	if err != nil {
		return errors.Join(fmt.Errorf("branch %s or %s changed or is in use by another git command: %w", branch, newBranch, err), undo())
	}
	err = d.pointHead(path, oldRef, newRef, symref)
	releaseErr := release()
	if err != nil {
		return errors.Join(err, releaseErr, undo())
	}
	// HEAD is on the new branch, so the rename has happened and the caller
	// must record it, along with anything the cleanup had to leave.
	if err := errors.Join(releaseErr, d.dropBranch(root, branch, tip)); err != nil {
		cleanup := &BranchCleanupError{Branch: newBranch, Err: err}
		if left, lookErr := d.refTip(root, oldRef); lookErr == nil && left == tip {
			cleanup.KeptTip = tip
		}
		return cleanup
	}
	return nil
}

// pointHead moves the worktree's HEAD from oldRef to newRef. With symref
// the write happens only if HEAD still names oldRef, so a switch, detach
// or rebase that started after the rename looked keeps its HEAD. Git
// before 2.46 has no conditional symref write, so HEAD is read again as
// late as possible instead.
func (d *Driver) pointHead(path, oldRef, newRef string, symref bool) error {
	message := "branch: renamed " + oldRef + " to " + newRef
	if symref {
		_, err := d.runBounded(path, "start\nsymref-update HEAD "+newRef+" ref "+oldRef+"\nprepare\ncommit\n",
			"update-ref", "--no-deref", "-m", message, "--stdin")
		return err
	}
	if current, err := d.run(path, "symbolic-ref", "-q", "HEAD"); err != nil || current != oldRef {
		return errors.Join(fmt.Errorf("worktree left %s during the rename", oldRef), err)
	}
	_, err := d.run(path, "symbolic-ref", "-m", message, "HEAD", newRef)
	return err
}

// refTransactions reports whether git can move HEAD on condition
// (update-ref's symref verbs, Git 2.46). A git without update-ref
// transactions at all (before 2.27) cannot hold a ref while HEAD moves,
// so the rename is refused before anything is written.
func (d *Driver) refTransactions(path, oldRef string) (bool, error) {
	out, err := d.runBounded(path, "start\nsymref-verify HEAD "+oldRef+"\nabort\n", "update-ref", "--no-deref", "--stdin")
	if !strings.HasPrefix(out, "start: ok") {
		return false, errors.Join(errors.New("git cannot hold a branch while HEAD moves (update-ref --stdin transactions need Git 2.27 or newer)"), err)
	}
	return strings.Contains(out, "abort: ok"), nil
}

// checkouts counts the worktrees whose HEAD names ref.
func (d *Driver) checkouts(root, ref string) (int, error) {
	out, err := d.run(root, "worktree", "list", "--porcelain")
	if err != nil {
		return 0, err
	}
	n := 0
	for line := range strings.SplitSeq(out, "\n") {
		if line == "branch "+ref {
			n++
		}
	}
	return n, nil
}

// dropBranch deletes a branch the rename made or left: the copy when HEAD
// could not move, the old branch once it did. It is the rename's to
// delete only while it still holds tip and no worktree has it checked
// out; otherwise it stays and the error names it.
func (d *Driver) dropBranch(root, branch, tip string) error {
	ref := "refs/heads/" + branch
	if n, err := d.checkouts(root, ref); err != nil {
		return fmt.Errorf("kept branch %s: %w", branch, err)
	} else if n > 0 {
		return fmt.Errorf("kept branch %s: a worktree has it checked out", branch)
	}
	if _, err := d.run(root, "update-ref", "-d", ref, tip); err != nil {
		return fmt.Errorf("kept branch %s: %w", branch, err)
	}
	// Git does not stop a delete of a branch another worktree checked out
	// after the count above. Put it back, create-only, for that checkout,
	// which could find it missing only between the delete and this write.
	// Its reflog went with the delete.
	if n, err := d.checkouts(root, ref); err != nil || n > 0 {
		_, createErr := d.run(root, "update-ref", ref, tip, "")
		return errors.Join(fmt.Errorf("kept branch %s: a worktree checked it out while it was deleted", branch), err, createErr)
	}
	if err := d.dropBranchConfig(root, branch); err != nil {
		return fmt.Errorf("kept the config of deleted branch %s: %w", branch, err)
	}
	return nil
}

// refTip returns the commit ref points at, "" when it does not exist.
func (d *Driver) refTip(root, ref string) (string, error) {
	tip, err := d.run(root, "rev-parse", "--verify", "--quiet", ref)
	if err != nil {
		// --verify --quiet exits 1 when the ref is absent; anything else is a real failure.
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) && exitErr.ExitCode() == 1 {
			return "", nil
		}
		return "", err
	}
	return tip, nil
}

// boundedCommand is git that ctx can stop. Stopping signals git's whole
// process group, hooks included, so git removes its lock files on the
// way out instead of leaving them for the next command to trip on.
func (d *Driver) boundedCommand(ctx context.Context, dir string, args ...string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, d.bin, args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0", "SSH_ASKPASS_REQUIRE=never")
	detachTerminal(cmd)
	cmd.Cancel = func() error { return interruptGit(cmd) }
	cmd.WaitDelay = time.Second
	return cmd
}

// runBounded runs git on complete input within refLockTimeout and returns
// its stdout, which it keeps on failure too.
func (d *Driver) runBounded(dir, input string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), refLockTimeout)
	defer cancel()
	cmd := d.boundedCommand(ctx, dir, args...)
	cmd.Stdin = strings.NewReader(input)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	if ctx.Err() != nil {
		err = fmt.Errorf("no answer within %s: %w", refLockTimeout, err)
	}
	if err != nil {
		err = fmt.Errorf("git %s: %w: %s", args[0], err, strings.TrimSpace(stderr.String()))
	}
	return stdout.String(), err
}

// holdRefs locks refs, which must all point at tip, until release is
// called. Taking the locks and letting go of them are each bounded, and a
// git that does not answer in time is stopped and reaped.
func (d *Driver) holdRefs(root, tip string, refs ...string) (func() error, error) {
	ctx, cancel := context.WithCancel(context.Background())
	cmd := d.boundedCommand(ctx, root, "update-ref", "--stdin")
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	stdin, err := cmd.StdinPipe()
	if err != nil {
		cancel()
		return nil, err
	}
	out, in := io.Pipe()
	cmd.Stdout = in
	if err := cmd.Start(); err != nil {
		cancel()
		return nil, err
	}
	exited := make(chan error, 1)
	go func() {
		err := cmd.Wait()
		_ = in.Close()
		exited <- err
	}()
	replies := make(chan string, 4)
	go func() {
		scanner := bufio.NewScanner(out)
		for scanner.Scan() {
			select {
			case replies <- scanner.Text():
			default:
			}
		}
		close(replies)
	}()
	finish := func(stop bool) error {
		if stop {
			cancel()
		}
		_ = stdin.Close()
		timer := time.NewTimer(refLockTimeout)
		defer timer.Stop()
		var err error
		select {
		case err = <-exited:
		case <-timer.C:
			cancel()
			err = errors.Join(fmt.Errorf("did not exit within %s", refLockTimeout), <-exited)
		}
		cancel()
		if err != nil {
			return fmt.Errorf("git update-ref: %w: %s", err, strings.TrimSpace(stderr.String()))
		}
		return nil
	}
	input := "start\n"
	for _, ref := range refs {
		input += "verify " + ref + " " + tip + "\n"
	}
	if _, err := io.WriteString(stdin, input+"prepare\n"); err != nil {
		return nil, errors.Join(err, finish(true))
	}
	locked := fmt.Errorf("could not lock %s at %s", strings.Join(refs, " and "), tip)
	timer := time.NewTimer(refLockTimeout)
	defer timer.Stop()
	for _, want := range []string{"start: ok", "prepare: ok"} {
		select {
		case got, ok := <-replies:
			if !ok || got != want {
				return nil, errors.Join(locked, finish(false))
			}
		case <-timer.C:
			return nil, errors.Join(locked, fmt.Errorf("no answer within %s", refLockTimeout), finish(true))
		}
	}
	return func() error {
		if _, err := io.WriteString(stdin, "abort\n"); err != nil {
			return errors.Join(err, finish(true))
		}
		return finish(false)
	}, nil
}

// dropBranchConfig removes branch.<name>.* when it exists, as
// `git branch -D` would.
func (d *Driver) dropBranchConfig(root, branch string) error {
	if _, err := d.run(root, "config", "--get-regexp", `^branch\.`+regexp.QuoteMeta(branch)+`\.`); err != nil {
		// --get-regexp exits 1 when nothing matches; anything else is a real failure.
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) && exitErr.ExitCode() == 1 {
			return nil
		}
		return err
	}
	_, err := d.run(root, "config", "--remove-section", "branch."+branch)
	return err
}
