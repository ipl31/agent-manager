package sysstat

import (
	"slices"
	"testing"
)

func TestParseChildNames(t *testing.T) {
	output := "  101   100 /opt/homebrew/bin/codex --resume 7\n  102   999 /usr/bin/vim notes.txt\n"
	want := []namedChild{
		{pid: 101, parent: 100, command: "/opt/homebrew/bin/codex"},
		{pid: 102, parent: 999, command: "/usr/bin/vim"},
	}
	if got := parseChildNames(output); !slices.Equal(got, want) {
		t.Fatalf("parseChildNames() = %v, want %v", got, want)
	}
}
