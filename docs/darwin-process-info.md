# Darwin-native child process identity

Status: implemented and target-validated on the feature branch for
[#530](https://github.com/YoanWai/agent-manager/issues/530).

Raw measurements and the bounded reproduction harness are in
[`research/darwin-process-info`](research/darwin-process-info/README.md).

## Decision

On Darwin, replace the multi-PID `ps` invocation that names a pane's direct
children with native per-PID process queries. Keep the process-tree metrics
pass and every Linux behavior unchanged.

The implementation belongs in `internal/sysstat`. It is not a public package,
a new daemon, or a general-purpose process library. It adds no dependency or
setting: agent-manager already depends on `gopsutil`, whose Darwin process
methods use `sysctl` for the parent PID and command line without starting
`ps`.

This is intentionally smaller than replacing all process sampling. The first
`ps -axo pid=,ppid=,pcpu=,rss=,time=` pass is not the call that triggers #530,
and it supplies the current first-sample `%CPU` fallback as well as the whole
process tree. A native replacement for that pass must first define equivalent
first-sample CPU semantics and prove its full-table cost on Intel and Apple
Silicon Macs.

## Problem path

Before this change, `sysstat.Trees` performed two process launches per sample:

1. One machine-wide `ps` pass supplies PID, parent PID, CPU, RSS, and
   cumulative CPU time. `Trees` builds the child graph and sums each requested
   pane's complete process tree.
2. `nameChildren` collects the direct children of the requested pane roots and
   passes all of their PIDs to one `ps -p` invocation. Only the parent PID and
   the first command token survive that call.

The second call is used to detect a user quitting one agent CLI and launching
another in the same pane. It contributes nothing to CPU, memory, process
count, or liveness.

On macOS, the second call becomes slow as soon as its `-p` selection contains
more than one entry. Adding `-x` avoids that cost on Darwin but changes the
selection rules on Linux. Native lookup removes the problematic command
without introducing a platform flag branch into shared code.

## Platform boundary

| Runtime | Process-tree metrics | Direct-child identity |
|---|---|---|
| Darwin amd64/arm64 | Existing machine-wide `ps` pass | Native per-PID lookup |
| Linux amd64/arm64 | Existing machine-wide `ps` pass | Existing scoped `ps` pass |
| WSL2 | Linux path | Linux path |

Use build-constrained files, not `runtime.GOOS` conditionals:

- `internal/sysstat/child_names_darwin.go` owns the native lookup.
- `internal/sysstat/child_names_linux.go` owns the existing scoped `ps`
  invocation and parsing.
- `internal/sysstat/sysstat.go` owns candidate selection, parent validation,
  and application to `ProcStat`.

The public `Trees` and `ProcStat` APIs do not change. Callers in the poller,
preview, and session commands remain unaware of the platform implementation.

## Data flow

```text
tmux pane PIDs
      |
      v
existing machine-wide metrics pass
      |
      +--> process graph --> tree CPU/RSS/count
      |
      +--> direct child candidates (pid, expected parent)
                         |
                         v
              platform child lookup
                Darwin: native
                Linux:  scoped ps
                         |
                         v
              verified argv[0] by PID
                         |
                         v
                 ProcStat.Children
                         |
                         v
              relaunched-tool detection
```

The shared layer should pass both PID and expected parent to the platform
lookup:

```go
type childRef struct {
	pid    int
	parent int
}

type namedChild struct {
	pid     int
	parent  int
	command string
}
```

These are internal transport types, not new domain models. The expected parent
is part of the request because a PID can disappear and be reused between the
machine-wide sample and the identity lookup.

## Darwin lookup

For each candidate, the Darwin implementation:

1. Constructs a lightweight `gopsutil/process.Process` handle for the
   candidate PID. It deliberately avoids `NewProcess`, which performs
   redundant existence and creation-time queries.
2. Reads its parent PID through `Ppid`.
3. Stops if the process disappeared, access was denied, or its parent no
   longer matches the sampled pane root.
4. Reads `CmdlineSlice`, retaining only `argv[0]`.
5. Returns the PID, observed parent, and command to the shared layer.

Parent lookup comes before command-line lookup so a reparented or recycled PID
does not pay the more expensive query. Lookups remain serial. A pane normally
has one direct agent child, and adding goroutines would complicate ordering and
error handling without removing the process-table scan that dominates the rest
of `Trees`.

Only `argv[0]` leaves the platform file. Arguments can contain prompts, paths,
or credentials and must not be stored or logged. The current `ps` path reads
the complete argument string into memory and then discards everything after
the first token; the native path narrows the retained result and correctly
preserves an executable path containing spaces.

No Darwin fallback starts `ps`. Falling back on the slow command would make
the original failure return under load and would hide native lookup defects.

## Alternatives considered

| Option | Evidence | Decision |
|---|---|---|
| Add Darwin-only `-x` | Cuts the two-PID median on the target from 3.705 ms to 1.743 ms, but Linux `-x` widens selection to every process | Useful emergency patch, not the final design |
| Native lookup for known children | 0.032 ms for two children and 0.080 ms for five; 47-115x faster than the current call | Implemented |
| Skip an overlapping poll | `poller.run` calls `refreshOnce` synchronously, then waits on its ticker or poke channel; two passes from one manager cannot overlap | Does not address this cause |
| One watcher for all managers | Could eliminate duplicate full polls but introduces ownership, failure recovery, freshness, and schema work across processes | Separate feature; unnecessary for this fix |
| Back off when no client is attached | Would reduce unattended cost but changes status, inbox, and notification freshness | Separate product decision |

The target runs macOS 15.5 rather than the macOS 26.5.1 version that produced
the issue's approximately 135 ms measurement. It reproduces the same shape—a
step in cost from one PID to two—and proves the native path independently of
the size of the operating-system regression. Full results and limitations are
recorded with the raw data.

## Consistency and failures

Process sampling is not atomic. A child can exit, exec, reparent, or have its
PID reused between the process-table pass and the identity lookup. The feature
preserves the existing safety rule: a name is accepted only when the observed
parent still equals the root sampled for that PID.

`Trees` currently has no error return, so lookup failures remain per-process
and fail closed:

- A vanished or inaccessible child contributes no entry to `Children`.
- CPU, RSS, process count, and liveness still come from the first pass.
- Relaunched-tool detection receives less evidence and keeps the stored tool;
  it must never guess from an unverified command.
- The next poll retries from a fresh process graph.

This matches the current behavior when the scoped `ps` exits nonzero. Managed
pane children run as the same user as agent-manager, so a persistent permission
failure is a defect to investigate rather than a reason to add a fallback.

## Why this stays local

The API is shaped around agent-manager's exact need: verify the direct children
of already-known tmux pane roots and return only their executable identity.
General process libraries must solve field selection, cache lifetime, PID
identity, every supported operating system, and broad process permissions.
That work has remained open in gopsutil's
[`ProcessesWithFields` proposal](https://github.com/shirou/gopsutil/pull/890).

Keeping this adapter internal provides a small maintenance surface:

- no exported API commitment;
- no new module or release lifecycle;
- no model-, tool-, or version-specific behavior;
- no user configuration;
- one Darwin file and one unchanged Linux counterpart.

If another project later needs the same contract, extraction should follow
measured reuse rather than precede it.

## Verification

Unit coverage keeps platform mechanics separate from shared policy.

Shared tests:

- accept a command only when the returned parent matches the sampled parent;
- discard vanished, inaccessible, empty-command, and reparented candidates;
- preserve direct-child order in `ProcStat.Children`;
- leave tree CPU, RSS, count, and `OK` unchanged when every name lookup fails;
- preserve tool detection for absolute executable paths and paths containing
  spaces.

Linux tests:

- retain the current `ps` parser cases and all existing `Trees` behavior.

Darwin tests and checks:

- exercise a real child process and confirm its PID, PPID, and `argv[0]`;
- confirm a child that exits during lookup is skipped without failing the
  sample;
- compile with `CGO_ENABLED=0` for both `darwin/amd64` and `darwin/arm64`;
- benchmark one, two, and five direct children against the current scoped
  `ps` implementation and the Darwin-only `-x` alternative;
- run the built manager on an isolated tmux socket with a throwaway home,
  launch real agent sessions, capture the TUI frame, and verify process stats,
  liveness, and relaunch detection;
- repeat with at least two live pane roots, the case that crosses macOS's slow
  multi-PID `ps` path.

Repository completion checks remain:

```bash
go build ./...
mkdir -p /tmp/amtest && env -u TMUX TMUX_TMPDIR=/tmp/amtest go test -race ./...
gofmt -l .
go vet ./...
```

The PR description must state that Darwin child identity moved to native
queries while Linux and WSL2 deliberately retain their existing implementation.

## Out of scope

This feature does not:

- replace the machine-wide process metrics pass;
- change CPU or memory accounting;
- change poll scheduling or add detached-client backoff;
- elect one poller across multiple manager processes;
- expose a process API outside `internal/sysstat`;
- add a setting, environment variable, or user-maintained file;
- change any agent CLI definition or status rule.

Those concerns have independent behavior and verification requirements. In
particular, a future fully native metrics collector must settle first-sample
CPU behavior before removing the remaining `ps` call.
