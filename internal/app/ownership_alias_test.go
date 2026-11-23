package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func anchorKeyForTest(t *testing.T, server string) string {
	t.Helper()
	matches, err := filepath.Glob(server + ".manager-*.lock")
	if err != nil || len(matches) != 1 {
		t.Fatalf("anchors=%v err=%v", matches, err)
	}
	key, err := databaseFileIdentity(matches[0])
	if err != nil {
		t.Fatal(err)
	}
	return key
}

func assertSameFile(t *testing.T, first, alias string) {
	t.Helper()
	a, err := os.Stat(first)
	if err != nil {
		t.Fatal(err)
	}
	b, err := os.Stat(alias)
	if err != nil || !os.SameFile(a, b) {
		t.Skip("filesystem does not resolve this case/symlink alias to the same file")
	}
}

func assertAliasTakeover(t *testing.T, first *ownerProcess, profile, server string) *ownerProcess {
	t.Helper()
	second := startOwnerProcess(t, "tui", profile, server)
	first.say(t, "changed")
	first.expect(t, "CHANGED another manager took over")
	select {
	case line := <-second.lines:
		t.Fatalf("alias started before owner release: %s", line)
	case <-time.After(100 * time.Millisecond):
	}
	first.say(t, "release")
	first.expect(t, "RELEASED")
	second.expect(t, "READY")
	return second
}

func TestOwnershipCaseAliasProfileUsesSameLockAndSQLKey(t *testing.T) {
	root := t.TempDir()
	profile := filepath.Join(root, "Profile")
	alias := filepath.Join(root, "profile")
	server := filepath.Join(t.TempDir(), "endpoint")
	first := startOwnerProcess(t, "tui", profile, server)
	first.expect(t, "READY")
	assertSameFile(t, filepath.Join(profile, "state.db"), filepath.Join(alias, "state.db"))
	second := assertAliasTakeover(t, first, alias, server)
	second.say(t, "release")
	second.expect(t, "RELEASED")
}

func TestOwnershipCaseAliasEndpointBeforeAndAfterCreation(t *testing.T) {
	profile := t.TempDir()
	dir := t.TempDir()
	server := filepath.Join(dir, "Endpoint")
	alias := filepath.Join(dir, "endpoint")
	first := startOwnerProcess(t, "tui", profile, server)
	first.expect(t, "READY")
	matches, _ := filepath.Glob(server + ".manager-*.lock")
	if len(matches) != 1 {
		t.Fatalf("anchors=%v", matches)
	}
	aliasAnchor := alias + strings.TrimPrefix(matches[0], server)
	assertSameFile(t, matches[0], aliasAnchor)
	originalKey := anchorKeyForTest(t, server)
	second := assertAliasTakeover(t, first, profile, alias)
	// Ordinary server creation does not change the persistent endpoint anchor.
	if err := os.WriteFile(server, []byte("endpoint fixture"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := anchorKeyForTest(t, server); got != originalKey {
		t.Fatalf("server creation changed identity %s -> %s", originalKey, got)
	}
	third := assertAliasTakeover(t, second, profile, server)
	third.say(t, "release")
	third.expect(t, "RELEASED")
}

func TestOwnershipCaseDistinctEndpointsRemainIndependent(t *testing.T) {
	profile := t.TempDir()
	dir := t.TempDir()
	one, two := filepath.Join(dir, "Endpoint"), filepath.Join(dir, "endpoint")
	if err := os.WriteFile(one, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(two, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	a, _ := os.Stat(one)
	b, _ := os.Stat(two)
	if os.SameFile(a, b) {
		t.Skip("filesystem does not support distinct case-sensitive endpoints")
	}
	first := startOwnerProcess(t, "tui", profile, one)
	first.expect(t, "READY")
	second := startOwnerProcess(t, "tui", profile, two)
	second.expect(t, "READY")
	if anchorKeyForTest(t, one) == anchorKeyForTest(t, two) {
		t.Fatal("different endpoints share SQL key")
	}
	first.say(t, "release")
	first.expect(t, "RELEASED")
	second.say(t, "release")
	second.expect(t, "RELEASED")
}

func TestOwnershipSymlinkedProfileAndEndpointParentShareAuthority(t *testing.T) {
	root := t.TempDir()
	profile := filepath.Join(root, "profile")
	aliasProfile := filepath.Join(root, "alias-profile")
	if err := os.Mkdir(profile, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(profile, aliasProfile); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	serverDir := filepath.Join(root, "server")
	aliasDir := filepath.Join(root, "alias-server")
	if err := os.Mkdir(serverDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(serverDir, aliasDir); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	first := startOwnerProcess(t, "tui", profile, filepath.Join(serverDir, "endpoint"))
	first.expect(t, "READY")
	second := assertAliasTakeover(t, first, aliasProfile, filepath.Join(aliasDir, "endpoint"))
	second.say(t, "release")
	second.expect(t, "RELEASED")
}
