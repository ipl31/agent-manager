package terminal

import (
	"context"
	"errors"
	"github.com/YoanWai/agent-manager/internal/ui/effects"
	"io"
	"slices"
	"strings"
	"testing"
	"time"
)

func stubLinkCopy(t *testing.T, copyErr error) *string {
	t.Helper()
	original := copyToClipboard
	copied := new(string)
	copyToClipboard = func(target string) error {
		*copied = target
		return copyErr
	}
	t.Cleanup(func() { copyToClipboard = original })
	return copied
}

func runLinkPage(t *testing.T, target, input string) string {
	t.Helper()
	var out strings.Builder
	page := &LinkPage{URL: target}
	page.SetStdin(strings.NewReader(input))
	page.SetStdout(&out)
	page.SetStderr(io.Discard)
	if err := page.Run(); err != nil {
		t.Fatalf("Run() = %v", err)
	}
	return out.String()
}

// The URL stands alone on its row, so the terminal's own link detection and
// a triple-click selection both take exactly the URL.
func TestLinkPagePrintsTheURLAndCopiesIt(t *testing.T) {
	copied := stubLinkCopy(t, nil)
	target := "https://github.com/wainmann/agent-manager/issues/new?template=bug_report.yml&version=0.38.0"
	out := runLinkPage(t, target, "\n")
	if !slices.Contains(strings.Split(out, "\n"), target) {
		t.Fatalf("the URL has no row of its own:\n%s", out)
	}
	if *copied != target {
		t.Fatalf("copied %q, want %q", *copied, target)
	}
}

func TestLinkPageNamesACopyFailure(t *testing.T) {
	stubLinkCopy(t, errors.New("terminal clipboard unavailable"))
	out := runLinkPage(t, "https://example.com/manual", "\n")
	if !strings.Contains(out, "terminal clipboard unavailable") {
		t.Fatalf("the copy failure was dropped:\n%s", out)
	}
}

type brokenTerminal struct{}

func (brokenTerminal) Write([]byte) (int, error) { return 0, errors.New("input/output error") }

// A terminal that took none of the page, as after the ssh link dropped, is a
// failure for the manager to report, not a page the user saw.
func TestLinkPageReportsAFailedWrite(t *testing.T) {
	stubLinkCopy(t, nil)
	page := &LinkPage{URL: "https://example.com/manual"}
	page.SetStdin(strings.NewReader("\n"))
	page.SetStdout(brokenTerminal{})
	if err := page.Run(); err == nil || !strings.Contains(err.Error(), "input/output error") {
		t.Fatalf("Run() = %v, want the write failure", err)
	}
}

// Ctrl+D at the prompt closes the input rather than sending Enter. The user
// is just as done with the page.
func TestLinkPageClosedInputReturns(t *testing.T) {
	stubLinkCopy(t, nil)
	runLinkPage(t, "https://example.com/manual", "")
}

func TestOwnedLinkPageDeniedBeforeActualRunTouchesNothing(t *testing.T) {
	lifetime := &effects.Lifetime{}
	copied := stubLinkCopy(t, nil)
	reader := &linkReadSpy{}
	var output strings.Builder
	command := ownedLinkPage(lifetime, "https://example.com/late")
	command.SetStdin(reader)
	command.SetStdout(&output)
	command.SetStderr(io.Discard)
	lifetime.CloseAdmission()
	if err := command.Run(); !errors.Is(err, ErrExecStopped) {
		t.Fatalf("late page = %v", err)
	}
	if *copied != "" || reader.calls != 0 || output.Len() != 0 {
		t.Fatal("denied page copied, printed or read input")
	}
}

type linkReadSpy struct{ calls int }

func (r *linkReadSpy) Read([]byte) (int, error) { r.calls++; return 0, io.EOF }

func TestOwnedLinkPageJoinsAdmittedPageThroughEnter(t *testing.T) {
	lifetime := &effects.Lifetime{}
	copied := stubLinkCopy(t, nil)
	entered, release := make(chan struct{}), make(chan struct{})
	command := ownedLinkPage(lifetime, "https://example.com/admitted")
	command.SetStdin(&blockedLinkRead{entered: entered, release: release})
	var output strings.Builder
	command.SetStdout(&output)
	command.SetStderr(io.Discard)
	finished := make(chan error, 1)
	go func() { finished <- command.Run() }()
	<-entered
	lifetime.CloseAdmission()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Millisecond)
	defer cancel()
	if err := lifetime.Join(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("interactive page falsely drained: %v", err)
	}
	close(release)
	if err := <-finished; err != nil {
		t.Fatal(err)
	}
	if err := lifetime.Join(context.Background()); err != nil {
		t.Fatal(err)
	}
	if *copied != "https://example.com/admitted" || !strings.Contains(output.String(), "https://example.com/admitted") {
		t.Fatal("normal page did not copy/print URL")
	}
}

type blockedLinkRead struct{ entered, release chan struct{} }

func (r *blockedLinkRead) Read(p []byte) (int, error) {
	close(r.entered)
	<-r.release
	p[0] = '\n'
	return 1, nil
}
