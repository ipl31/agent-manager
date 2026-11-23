//go:build darwin || linux

package store

import (
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"
)

func TestDanglingStoreSymlinkModeHelper(t *testing.T) {
	for i, arg := range os.Args {
		if arg != "store-symlink-mode" {
			continue
		}
		syscall.Umask(0o022)
		dir := os.Args[i+1]
		path := filepath.Join(dir, "state.db")
		target := filepath.Join(dir, "fresh.db")
		if err := os.Symlink("fresh.db", path); err != nil {
			t.Fatal(err)
		}
		st, err := Open(path)
		if err != nil {
			t.Fatal(err)
		}
		defer st.Close()
		if st.path != target {
			t.Fatalf("database/sidecars still use link name: %s", st.path)
		}
		for _, name := range []string{target, target + "-wal", target + "-shm"} {
			info, err := os.Stat(name)
			if err != nil {
				t.Fatal(err)
			}
			if info.Mode().Perm() != 0o600 {
				t.Fatalf("%s mode=%o want 0600", name, info.Mode().Perm())
			}
		}
		return
	}
}

func TestOpenDanglingRelativeSymlinkCreatesPrivateTargetAndSidecars(t *testing.T) {
	cmd := exec.Command(os.Args[0], "-test.run=^TestDanglingStoreSymlinkModeHelper$", "--", "store-symlink-mode", t.TempDir())
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("dangling target mode helper: %v\n%s", err, out)
	}
}

func TestOpenRejectsStoreSymlinkCycleAndUnreachableTarget(t *testing.T) {
	dir := t.TempDir()
	one, two := filepath.Join(dir, "one.db"), filepath.Join(dir, "two.db")
	if err := os.Symlink("two.db", one); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("one.db", two); err != nil {
		t.Fatal(err)
	}
	if st, err := Open(one); err == nil {
		st.Close()
		t.Fatal("symlink cycle accepted")
	}
	unreachable := filepath.Join(dir, "state.db")
	if err := os.Symlink(filepath.Join("missing", "fresh.db"), unreachable); err != nil {
		t.Fatal(err)
	}
	if st, err := Open(unreachable); err == nil {
		st.Close()
		t.Fatal("unreachable target accepted")
	}
}

func TestStoreSymlinkTraversalModeHelper(t *testing.T) {
	for i, arg := range os.Args {
		if arg != "store-symlink-traversal" {
			continue
		}
		syscall.Umask(0o022)
		root := os.Args[i+1]
		profile := filepath.Join(root, "profile")
		other := filepath.Join(root, "other")
		sub := filepath.Join(other, "subdirectory")
		if err := os.MkdirAll(profile, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.MkdirAll(sub, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(sub, filepath.Join(profile, "hop")); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink("hop/../fresh.db", filepath.Join(profile, "state.db")); err != nil {
			t.Fatal(err)
		}
		decoy := filepath.Join(profile, "fresh.db")
		content := []byte("lexical decoy must stay untouched")
		if err := os.WriteFile(decoy, content, 0o644); err != nil {
			t.Fatal(err)
		}
		st, err := Open(filepath.Join(profile, "state.db"))
		if err != nil {
			t.Fatal(err)
		}
		defer st.Close()
		actual := filepath.Join(other, "fresh.db")
		if st.path != actual {
			t.Fatalf("wrong target: %s want %s", st.path, actual)
		}
		raw, err := os.ReadFile(decoy)
		if err != nil || string(raw) != string(content) {
			t.Fatalf("decoy changed: %q %v", raw, err)
		}
		info, err := os.Stat(decoy)
		if err != nil || info.Mode().Perm() != 0o644 {
			t.Fatalf("decoy permissions changed: %v %v", info, err)
		}
		for _, name := range []string{actual, actual + "-wal", actual + "-shm"} {
			info, err := os.Stat(name)
			if err != nil || info.Mode().Perm() != 0o600 {
				t.Fatalf("target not private: %s %v %v", name, info, err)
			}
		}
		return
	}
}

func TestOpenStoreSymlinkTraversesDirectoryBeforeDotDot(t *testing.T) {
	cmd := exec.Command(os.Args[0], "-test.run=^TestStoreSymlinkTraversalModeHelper$", "--", "store-symlink-traversal", t.TempDir())
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("physical target helper: %v\n%s", err, out)
	}
}

func TestOpenRejectsDirectoryAndDirectoryLinkWithoutChangingMode(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "original")
	if err := os.Mkdir(dir, 0o750); err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(dir, "untouched")
	if err := os.WriteFile(marker, []byte("unchanged"), 0o640); err != nil {
		t.Fatal(err)
	}
	before, err := os.Stat(dir)
	if err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(t.TempDir(), "state.db")
	if err := os.Symlink(dir, link); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{dir, link} {
		if st, err := Open(path); err == nil {
			st.Close()
			t.Fatalf("directory accepted: %s", path)
		}
		after, err := os.Stat(dir)
		if err != nil || after.Mode() != before.Mode() {
			t.Fatalf("directory mode changed: %v -> %v (%v)", before, after, err)
		}
		raw, err := os.ReadFile(marker)
		if err != nil || string(raw) != "unchanged" {
			t.Fatalf("directory contents changed: %q %v", raw, err)
		}
	}
}
