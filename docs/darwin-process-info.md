# Darwin-native child process identity

Status: proposed architecture for
[#530](https://github.com/YoanWai/agent-manager/issues/530).

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

## Current path

`sysstat.Trees` currently performs two process launches per sample:

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

1. Opens a `gopsutil/process.Process` for the candidate PID.
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

Unit coverage should keep platform mechanics separate from shared policy.

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
- benchmark one, two, and approximately twenty direct children against the
  current scoped `ps` implementation;
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
