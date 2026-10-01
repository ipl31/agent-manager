# CLI handling checks

The fast contract suite lives in the repository so it can exercise the same
status, focus, and tmux code as agent-manager. It uses only Go and tmux; the
installed agent CLIs are needed only for the optional live capture.

```sh
go run ./cmd/tui-check quick
go run ./cmd/tui-check fuzz
go run ./cmd/tui-check -seconds 30 fuzz
go run ./cmd/tui-check live
go run ./cmd/tui-check -current-home live
```

`quick` replays the pane corpus, fuzz seeds, selected session and UI regressions,
and the real tmux paste, bracketed paste, and submit contracts.
`fuzz` runs seven bounded Go fuzz targets with two workers, for 5 seconds per
target by default. A failure is reproducible with the command Go prints;
commit a minimized failing input or a named regression fixture. The fast tests
do not need network access or agent accounts. Both modes give tmux a short,
temporary socket directory and unset `TMUX`.

`live` starts the selected installed tools in separate, empty working
directories on a private tmux socket and saves their wide and narrow startup
frames as `.ansi` files, with a `.json` status report beside each one. It uses
isolated CLI homes by default. `-current-home` lets installed accounts and
settings participate; the runner still sends no prompt and approves no dialog.
The command finds executables through `PATH`. Use `-tools` to choose a subset
and `-output` to choose the capture directory. Captures can contain account or
workspace text, so inspect and sanitize them before adding a useful frame to
`internal/status/testdata/panes.json`.

The live report says `composer`, `dialog`, or `unclassified` for each frame.
An unclassified frame, failed launch, or missing executable makes the command
fail. A dialog is an observed interactive screen, not proof that the CLI is
authenticated or ready for a prompt. These checks cover startup rendering;
they do not exercise agent-manager's launch assembly, a submitted model turn,
or upstream behavior on every platform. A UI, poller, or status change still
needs a real managed-session check before claiming live compatibility.

The corpus lists every built-in tool from `config.Default`, including the
shell. It checks status and selected reply, echo, and draft behavior. The two
status fuzz targets mutate bounded pane content and probe all parser APIs.
UI targets generate short selection, scroll, watcher, and status-poll sequences
and verify key-byte encoding. Session targets check candidate ordering, recapture
snapshots, and OpenCode metadata parser stability. The tmux test verifies a
paste longer than 1024 bytes lands intact in pane 0 while pane 1 is active,
then checks that `SendText` submits only after the message bytes. Discovery
time is explicit so it can be run often.

## Known detections on this branch

The standalone `am/tui-fuzzer` branch deliberately leaves production status
code at its `origin/main` base (`87569e4`). `quick` currently fails on
`codex-working-draft` and `codex-queued-followup`. The first
`FuzzPaneDraftIsolation` seeds fail for the same reply bug. Those failures
detect two Codex defects, not test setup failures. `fuzz` continues to the other targets and reports all
failures at the end. The separate `fix/codex-queued-status-reply` branch carries
the production changes and focused regressions. With both branches applied,
the corpus and fuzz seeds pass.
