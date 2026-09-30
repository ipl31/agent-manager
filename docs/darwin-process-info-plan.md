# Darwin native child identity development plan

This plan takes the validated design for issue
[#530](https://github.com/YoanWai/agent-manager/issues/530) from implementation
through review and release readiness. The outcome is deliberately narrow:
Darwin stops launching the slow multi-PID `ps` command used only to name a
pane's direct children, while process-tree metrics, Linux and WSL2 behavior,
the public API, and user configuration remain unchanged.

The implementation and Apple Silicon validation are complete on the feature
branch. No pull request is open. The remaining work is to close or explicitly
document the platform confidence gaps, perform the final review pass, and
prepare one focused pull request when authorized.

The architectural rationale is in
[`darwin-process-info.md`](darwin-process-info.md). Reproduction methodology,
raw measurements, checksums, and live verification are in
[`research/darwin-process-info`](research/darwin-process-info/README.md).

## Delivery decision

Use the existing gopsutil dependency as a private Darwin adapter inside
`internal/sysstat`:

1. Build the process tree and metrics with the existing machine-wide `ps`
   pass.
2. Pass each direct-child PID and its sampled parent into a platform-specific
   identity lookup.
3. On Darwin, read the current parent with `Ppid` and the argument vector with
   `CmdlineSlice`, retaining only `argv[0]`.
4. On Linux and WSL2, retain the existing scoped `ps` command and parser.
5. Accept a name only when the observed parent still matches the sampled
   parent.

Do not add a Darwin fallback to `ps`. A fallback would restore the incident
under load and conceal a native lookup defect. A failed lookup omits identity
for that child until the next poll; metrics and liveness continue to come from
the first pass.

## Scope boundaries

The change includes the shared identity contract, Darwin implementation,
unchanged Linux implementation behind a platform file, regression tests,
target benchmarks, live application verification, and documentation.

It does not include:

- replacing the machine-wide metrics collector;
- changing CPU or memory accounting;
- changing poll scheduling or detached-client behavior;
- electing one poller across manager processes;
- exporting a reusable process library;
- adding configuration, feature flags, schema changes, or tool definitions;
- changing any agent CLI's status rules.

These exclusions keep the patch within the Thin Wrapper Principle and make a
performance rollback independent of unrelated polling or product decisions.

## Acceptance criteria

| Area | Required result | Evidence |
|---|---|---|
| Darwin process launches | A live poll launches the machine-wide metrics `ps` and no scoped `ps -p` child-name command | Trace the argv of every `ps` call during a disposable live run |
| Child identity | Direct children return their exact `argv[0]`, including absolute paths and values containing spaces | Darwin process test and live relaunch test |
| PID safety | A child is accepted only when its observed parent matches the parent in the sampled process tree | Shared wrong-parent test and Darwin wrong-parent test |
| Failure behavior | Exited, inaccessible, or empty-command children are skipped without invalidating tree metrics or liveness | Shared policy tests and an exited-child Darwin test |
| Ordering | `ProcStat.Children` retains the direct-child order from the sampled tree | Shared unit test |
| Linux and WSL2 | The existing command, parser, selection behavior, and output contract remain unchanged | Linux parser and tree tests; WSL2 uses the Linux build path |
| API and configuration | `Trees`, `ProcStat`, config, store schema, and tool definitions do not change | Diff review |
| Privacy | Only `argv[0]` leaves the Darwin platform file; arguments and environment values are neither retained nor logged | Code review and evidence-file inspection |
| Performance | Native lookup is materially faster for one, two, and five direct children and removes the two-PID discontinuity from agent-manager | Bounded target benchmark against current `ps` and Darwin `ps -x` |
| Build matrix | CGO-disabled Darwin arm64 and amd64 builds compile; Linux build, race suite, vet, and formatting pass | Repository completion commands |

Performance results should remain evidence rather than a timing assertion in
CI. Absolute timings vary by macOS version and host activity; structural tests
for the absence of the second process launch are deterministic.

## Work sequence

### Freeze the behavior being replaced

Status: complete.

- Record that the second lookup supplies only direct-child identity for tool
  relaunch detection.
- Confirm that CPU, RSS, process count, cumulative CPU time, and liveness come
  from the first pass.
- Reproduce the one-to-two-PID cost discontinuity with a bounded sequential
  harness.
- Compare the current call, Darwin `-x`, and native lookup without creating a
  large process population.

Exit condition: the old contract and performance trigger are captured in raw,
checksummed evidence.

### Separate shared policy from platform mechanics

Status: complete.

- Add internal `childRef` and `namedChild` transport types in
  `internal/sysstat/sysstat.go`.
- Keep candidate selection and application to `ProcStat` in shared code.
- Move command execution and parsing out of shared code.
- Keep parent validation in shared policy even when Darwin rejects a mismatch
  early.

Exit condition: callers of `Trees` are unchanged, and shared code contains no
Darwin branch.

### Implement both platform adapters

Status: complete.

- In `internal/sysstat/child_names_darwin.go`, query `Ppid` before
  `CmdlineSlice`, process candidates serially, and return only `argv[0]`.
- Construct a lightweight `process.Process` handle directly so
  `NewProcess` does not add redundant existence and creation-time queries.
- In `internal/sysstat/child_names_linux.go`, preserve the existing scoped
  `ps` invocation and parser byte-for-byte in behavior.
- Add no fallback, cache, goroutine fan-out, setting, or new dependency.

Exit condition: Darwin has no child-name process launch and Linux produces the
same `ProcStat.Children` values as before.

### Add regression and platform tests

Status: complete.

- Test shared parent validation, empty identities, child ordering, and
  unchanged tree metrics.
- Test the Linux parser independently from shared policy.
- On Darwin, test a real child, exact `argv[0]` with spaces, wrong-parent
  rejection, and a child that has already exited.
- Keep the comparative Darwin benchmark beside the implementation tests so it
  can be rerun with a fixed iteration count.

Exit condition: production behavior is covered without making ordinary CI
depend on host timing.

### Validate on an isolated Apple Silicon target

Status: complete.

- Run the bounded baseline and comparative benchmark.
- Build with `CGO_ENABLED=0` and run the complete `internal/sysstat` suite on
  macOS.
- Run the full manager with a throwaway `HOME`, short dedicated
  `TMUX_TMPDIR`, explicitly named outer socket, and disposable store.
- Exercise two pane roots with direct children and start a real supported CLI
  without submitting a model prompt.
- Trace every external `ps` argv and prove that only the machine-wide metrics
  form remains.
- Stop the CLI, run a different recognized `argv[0]` in the same pane, and
  verify that the stored tool changes on the next poll.
- Stop both explicit tmux servers and verify that no test process remains.

Exit condition: process accounting, liveness, status detection, and relaunch
detection work in the real application with zero scoped `ps -p` calls.

### Close platform confidence gaps

Status: remaining before final review, or explicitly disclosed in the pull
request Scope section.

- Ask the issue reporter to run the fixed build on the affected macOS 26.x
  host and capture the same bounded benchmark. This confirms the absolute
  regression is gone where it originally measured about 135 ms.
- Run a Darwin amd64 smoke test on Intel hardware if available. The amd64
  binary already cross-compiles; runtime coverage is the remaining gap.
- Run the focused Linux tree and parser tests under WSL2 if a host is
  available. WSL2 takes the unchanged Linux path, so lack of a host must be
  stated rather than hidden.

These are confidence checks, not reasons to widen the implementation. The
structural result—Darwin no longer executes the problematic command—does not
depend on reproducing a particular absolute latency.

### Prepare one reviewable pull request

Status: pending authorization.

- Keep this as one pull request. Splitting the shared contract from its two
  platform implementations would create an incomplete build or an
  unvalidated intermediate behavior.
- Fill the pull request template's Scope section with the intended behavior,
  explicit non-goals, tested platforms, and any remaining runtime gaps.
- Lead the description with the user-visible result: macOS polling no longer
  launches the pathological multi-PID child-name `ps` command.
- Include the benchmark table and live `ps` trace summary, linking to raw data
  rather than pasting all samples into the description.
- State that Linux and WSL2 intentionally retain the existing implementation.
- Do not claim that the entire process collector is native; the machine-wide
  metrics pass remains `ps` by design.

Exit condition: a reviewer can distinguish intended scope from incidental
refactoring and can reproduce every important claim.

## File responsibilities

| File | Responsibility |
|---|---|
| `internal/sysstat/sysstat.go` | Select direct-child candidates, define the private transport types, validate parent identity, and update `ProcStat.Children` |
| `internal/sysstat/child_names_darwin.go` | Read PPID and `argv[0]` through gopsutil's Darwin-native methods |
| `internal/sysstat/child_names_linux.go` | Preserve the scoped `ps` command and parser for Linux and WSL2 |
| `internal/sysstat/sysstat_test.go` | Cover shared ordering, parent validation, and metrics invariants |
| `internal/sysstat/child_names_darwin_test.go` | Cover native behavior and hold the bounded comparative benchmark |
| `internal/sysstat/child_names_linux_test.go` | Lock the existing parser behavior |
| `internal/ui/poller.go` | Update the stale comment describing the platform identity lookup; no logic change |
| `docs/darwin-process-info.md` | Record the architecture, tradeoffs, and failure model |
| `docs/research/darwin-process-info/` | Preserve the harness, raw results, live trace, target tests, checksums, and limitations |

No changes belong in `go.mod`, `internal/config`, `internal/store`, tool
definitions, settings UI, or the poller's scheduling loop.

## Safe target validation procedure

The target harness must make loss of SSH responsiveness unlikely and ensure
that cleanup does not depend on the manager remaining healthy.

1. Record OS build, architecture, logical CPUs, memory, process count, load,
   and memory pressure before starting.
2. Refuse to start above a target-specific load threshold. The validated
   24-CPU host used a one-minute-load ceiling of 12.
3. Run lookups serially with a two-second command timeout and a total deadline.
4. Use existing stable PIDs for the `ps` baseline. Do not synthesize hundreds
   of processes to reproduce a trigger that occurs at two PIDs.
5. Limit the Go benchmark to five sleeping children, fixed iterations, and a
   test timeout. Ensure children expire even if cleanup is interrupted.
6. Run the application only under explicit disposable tmux sockets and a
   throwaway home. Never address the default tmux socket.
7. Capture only timing, counts, load, test output, and `ps` argv. Do not record
   process command lines, prompts, environment variables, or credentials.
8. Stop the named test servers, verify all test PIDs are gone, recheck load and
   memory pressure, then remove generated toolchains and binaries only after
   evidence has been copied.

Do not reproduce the thirteen-manager incident directly. Removing and tracing
the exact expensive call, combined with a bounded microbenchmark, proves the
solution without risking the machine-wide feedback loop described in the
issue.

## Repository verification

Run these gates from a clean worktree with the repository's exact Go version:

```bash
go build ./...
mkdir -p /tmp/amtest && env -u TMUX TMUX_TMPDIR=/tmp/amtest go test -race ./...
gofmt -l .
go vet ./...
```

Also cross-compile the production binary and focused test binary with
`CGO_ENABLED=0` for `darwin/arm64` and `darwin/amd64`. On a Mac, run the full
`internal/sysstat` test binary and the isolated live application procedure.

The work is release-ready only when formatting prints nothing, build and vet
are clean, the race suite passes, both Darwin architectures compile, target
tests pass, and every untested runtime is named in the pull request.

## Risks and mitigations

| Risk | Mitigation | Residual limitation |
|---|---|---|
| Child exits or PID is reused between samples | Query and validate the observed parent before retaining `argv[0]`; fail closed on every lookup error | Process sampling is not atomic; the next poll corrects transient identity loss |
| Native query is denied | Omit only that child's identity; keep metrics and liveness from the first pass | Relaunch detection waits for a later successful sample |
| Darwin behavior changes in gopsutil | Use its existing public process API and pinned module version; keep the adapter private and small | A future dependency update still requires Darwin tests |
| Linux behavior drifts during extraction | Isolate it in a Linux file and lock the existing parser with tests | WSL2 runtime confidence still depends on host availability |
| Timing tests become flaky | Keep performance measurements out of ordinary CI and test the absence of the process launch structurally | Release notes should quote the measured host and OS, not promise a universal latency |
| The affected macOS release differs from the available host | Test the reporter's host when possible and preserve the limitation in evidence | Current target proves the discontinuity and solution, not the original 135 ms absolute value |
| A fallback silently restores load | Do not implement one; surface persistent native failures through tests and issue reports | Emergency rollback is a code revert, not an automatic runtime switch |

## Rollout and rollback

Ship the change without a feature flag. The behavior is internal, has no data
migration, and fails by temporarily withholding optional identity rather than
breaking metrics or session liveness.

After release, watch issue reports for incorrect tool relaunch detection or
missing child identities on Darwin. Do not add command-line logging to the
poller as telemetry; arguments can contain sensitive user data.

If a Darwin regression appears, revert the native child-identity commit. A
Darwin-only `ps -x` variant is an acceptable short-lived emergency patch
because it avoids the known slow path there, but it must not be applied to
Linux and must not become a silent fallback in the native implementation.

## Definition of done

The feature is done when all acceptance criteria pass, the affected-host and
platform confidence results are either recorded or explicitly scoped out, the
pull request describes the narrow Darwin-only behavior accurately, and review
finds no unrelated product or polling changes. Opening that pull request is a
separate authorized action.
