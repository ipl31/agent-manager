package tmux

import (
	"fmt"
	"os"
	"strings"
	"testing"
	"time"
)

// This checks the actual pty path, including tmux's 1024-byte send-keys
// boundary. cat records what pane 0 receives without interpreting a CLI.
func TestPasteKeepsBytesAndTargetsPaneZero(t *testing.T) {
	driver := requireTmux(t)
	id := "transport"
	received := t.TempDir() + "/received"
	ready := received + ".ready"
	command := "stty raw -echo; printf ready > " + ShellQuote(ready) + "; cat > " + ShellQuote(received)
	if err := driver.Create(id, t.TempDir(), command, nil, 100, 30); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = driver.Kill(id) })
	if err := awaitFile(ready, "ready", 3*time.Second); err != nil {
		t.Fatal(err)
	}
	if err := driver.SendCommand("split-window", "-t", windowTarget(id), "sleep 30"); err != nil {
		t.Fatal(err)
	}
	if err := driver.SendCommand("select-pane", "-t", windowTarget(id)+".1"); err != nil {
		t.Fatal(err)
	}
	text := strings.Repeat("quoted ' text ☃\nsecond line\t", 60)
	// tmux sends an LF as the terminal's Enter byte (CR) to a raw pty.
	want := strings.ReplaceAll(text, "\n", "\r")
	if err := driver.Paste(id, text); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		got, err := os.ReadFile(received)
		if err == nil && len(got) >= len(want) {
			if string(got) != want {
				at := firstDifferent(got, []byte(want))
				t.Fatalf("pane 0 received %d bytes, want %d; first difference at %d: got %q, want %q", len(got), len(want), at, got[max(0, at-8):min(len(got), at+16)], want[max(0, at-8):min(len(want), at+16)])
			}
			message := "submit ☃\nsecond line"
			if err := driver.SendText(id, message); err != nil {
				t.Fatal(err)
			}
			want += strings.ReplaceAll(message, "\n", "\r") + "\r"
			if err := awaitFile(received, want, 3*time.Second); err != nil {
				t.Fatal(err)
			}
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	got, _ := os.ReadFile(received)
	t.Fatalf("pane 0 received %d/%d bytes before deadline", len(got), len(want))
}

func awaitFile(path, want string, limit time.Duration) error {
	deadline := time.Now().Add(limit)
	for time.Now().Before(deadline) {
		got, err := os.ReadFile(path)
		if err == nil && string(got) == want {
			return nil
		}
		time.Sleep(10 * time.Millisecond)
	}
	return fmt.Errorf("%s did not contain %q before deadline", path, want)
}

func firstDifferent(a, b []byte) int {
	for i := 0; i < len(a) && i < len(b); i++ {
		if a[i] != b[i] {
			return i
		}
	}
	if len(a) != len(b) {
		return min(len(a), len(b))
	}
	return -1
}
