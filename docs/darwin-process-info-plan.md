# Darwin native child identity development plan

Status: not release-ready. The branch contains an initial implementation and
useful Apple Silicon measurements, but correctness, privacy, safe reproduction,
and evidence gates remain open. No pull request is open. The checkpoints below
separate historical observations from requirements that the final revision
must satisfy; a documented runtime gap does not waive a blocking safety gate.

The intended outcome for issue
[#530](https://github.com/YoanWai/agent-manager/issues/530) remains narrow:
Darwin stops launching the slow multi-PID `ps` command used to name a pane's
direct children. Process-tree metrics, Linux and WSL2 behavior, the public
`Trees`/`ProcStat` contract, and user configuration remain unchanged.

This is the canonical readiness checklist. The initial architectural rationale
is in [`darwin-process-info.md`](darwin-process-info.md), and historical data is
in [`research/darwin-process-info`](research/darwin-process-info/README.md).
Their broad completion and failure-safety claims require reconciliation with
this checklist before release. Existing raw measurements remain historical
evidence and must not be rewritten to imply that pending tests have passed.

## Evidence baseline

The implementation is in commit `efd1462`; this plan was initially added in
`37f3089`. Review found a clean worktree, matching checksums for all five
published evidence files, and an unmodified module cache (`go mod verify`).
Those checks establish artifact integrity, not completion of the new gates.

Keep these three measurements distinct:

| Source | Host and observation | What it supports |
|---|---|---|
| Issue reporter | macOS 26.5.2; about 1,800 processes; the scoped call reportedly took 3.1 seconds amid 13 managers | The original operational incident; native runtime validation on this host is pending |
| [Maintainer reproduction](https://github.com/YoanWai/agent-manager/issues/530#issuecomment-5735758235) | macOS 26.5.1; about 900 processes; five-PID medians of 135.1 ms for the original call and 9.8 ms with `-x` | The separate reproduction and Darwin-only flag alternative |
| Checked-in target run | macOS 15.5 (24F74), M2 Ultra, 24 logical CPUs; original two-PID benchmark median 3.705 ms, native 0.032 ms | A faster host reproduces the one-to-two-PID cost discontinuity and measures the initial native adapter |

The first two figures are reported in the issue, not measurements from this
branch. Multiplying lookup duration by managers and polls estimates aggregate
lookup wall time. It does not measure CPU saturation or prove a self-sustaining
load feedback loop. Do not claim that removing this call resolves every cost
of multiple managers; scheduling and duplicate polling remain out of scope.

## Architecture and scope

Retain the platform boundary already implemented in `internal/sysstat`:

1. Build process-tree metrics with the existing machine-wide
   `ps -axo pid=,ppid=,pcpu=,rss=,time=` pass.
2. Select direct-child candidates and retain their sampled roots in shared
   code. Keep shared validation and application to `ProcStat.Children` there.
3. Resolve Darwin identities serially through a private native adapter. The
   existing gopsutil dependency is the starting point, subject to the identity
   and privacy design gates below.
4. Keep the existing scoped `ps` command and parser on Linux and WSL2.
5. Publish identities for a Darwin root only after every sampled candidate
   for that root passes completeness, generation, parent, and argv checks.

There is no automatic Darwin `ps` fallback, cache, goroutine fan-out, feature
flag, new setting, or tool-specific discovery rule. CPU, RSS, cumulative CPU
time, process count, and liveness continue to use the first pass. Native lookup
failure must withhold the affected root's entire identity set, leaving other
roots and all tree metrics intact.

Scope includes the private identity contract, adapter corrections, regression
and consumer tests, target validation, evidence, and a prepared recovery patch.
It excludes replacing the metrics collector, changing accounting or poll
scheduling, electing a shared poller, exposing a process library, adding schema
or configuration, and changing agent CLI definitions or status rules.

Review confirmed several concerns do not require additional implementation:

- The Linux extraction preserves the previous command, parser, filtering,
  and ordering. Broader native Linux work is unnecessary for this fix.
- Darwin and Linux are the released OS targets; Windows runs through WSL2.
  Missing native Windows or FreeBSD adapters are not release-matrix defects.
- `process.Process{Pid: ...}` is sufficient for the pinned dependency's `Ppid`
  and `CmdlineSlice` methods. They do not require `NewProcess` initialization.
  Whether the completed design needs an additional generation query is a
  separate decision; its cost must be measured.
- `refreshOnce` and `runMu` serialize polls within a manager. Adding overlapping
  poll suppression would not address this incident.

## Blocking identity design gates

### Root completeness and consumer-state preservation

Status: pending; required before release.

The current adapter returns successful children even when another child's
query fails. This is unsafe for `detectRelaunchedTool`: if the current tool's
child is missing but another recognized child remains, the poller can switch
tools, remove hooks, and clear the conversation ID. A later good sample cannot
automatically restore that ID. Calling this per-child behavior "fail closed"
or merely a delay is incorrect.

Make Darwin identity all-or-nothing per sampled root. A query error, missing
candidate, empty or ambiguous command, parent mismatch, or generation mismatch
invalidates that root's identity sample. Pass no partial list to relaunch
detection. Preserve the public API by enforcing completeness in the private
transport/application boundary, with explicit platform ownership so Linux
behavior does not change incidentally.

Required deterministic tests cover:

- the current tool and another recognized tool both present, with either the
  current child's parent query or command query failing;
- an all-failed root and a complete neighboring root in the same sample;
- empty, ambiguous, vanished, and reparented candidates;
- unchanged stored tool, conversation ID, and hook contents after each
  incomplete sample and after a later successful sample of the original tool;
- a complete genuine relaunch still changing the tool and clearing only the
  old tool's metadata as intended;
- unchanged `OK`, `PCPU`, `CPUSeconds`, `CPUPercent`, `RSS`, `RamPercent`, and
  process count when identity is withheld, plus direct-child order when valid.

The implementation must settle where completeness is represented and checked.
Tests must cross the adapter/shared-policy boundary and exercise the poller's
persistent side effects, rather than only testing an empty returned slice.

### Exact argv identity and privacy

Status: pending design decision and target tests; required before release.

The pinned gopsutil v4.26.8 Darwin `parseCmdline` implementation documents that
an empty `argv[0]` is indistinguishable from padding and can leak an environment
entry into its result. Skipping initial empty chunks can also promote a later
argument into position zero. Checking that the returned first string is
nonempty does not prove it is the process's actual `argv[0]`.

Add Darwin tests with exact absolute paths and spaces, empty `argv[0]` with
remaining arguments, empty `argv[0]` without remaining arguments, and a
zero-argument process where the OS permits it. Use only synthetic argument and
environment sentinels. Establish what the OS and dependency return; require
that no sentinel becomes `ProcStat.Children`, a log entry, or a tool change.
An unsupported fixture shape must be explained with target evidence, not
silently counted as covered.

Choose and document a native mechanism that can establish exact `argv[0]` or
reliably reject ambiguous identities. A caller cannot safely infer an empty
original argv from the dependency's already-shifted result. If the pinned API
cannot meet the contract, record the necessary adapter or dependency change
and revise scope before implementing it; do not silently weaken the guarantee
or substitute an executable basename with different semantics. Keep the gate
open until the chosen mechanism and its failure behavior are tested.

Only a verified `argv[0]` may leave the platform boundary. Native argument
buffers necessarily exist transiently; non-identity arguments and environment
values must not be retained in application state or logged. Existing artifact
inspection found no raw command lines or credentials, but that does not prove
the missing empty-argv behavior is safe.

### Process generation across native queries

Status: pending design decision and deterministic tests; required before release.

The current parent read precedes the command read. A PID can exit, be reaped,
and be reused between those operations, pairing the old parent with an
unrelated process's command. Shared validation then accepts a pair that was
never observed together. The existing wrong-parent and already-exited tests do
not exercise this interval.

Establish that the native reads used for an accepted identity refer to one
process generation and the sampled root. Select a native generation token or
equivalent mechanism, document its resolution and comparison semantics, and
validate it across the command query. A second PPID read can detect some
changes, but cannot prove generation identity or exclude same-parent PID reuse.
Do not treat `NewProcess` or a cached creation time as proof without inspecting
the actual native queries. Verify whether generation continuity can also be
established from the original tree sample; do not claim that guarantee if the
sample contains only PID and PPID.

Use a deterministic query seam to model exit, reparenting, exec, reuse under a
different parent, and same-parent reuse between reads. Reject a mixed-generation
result and invalidate its root before consumer side effects. Supplement these
tests with bounded real-process churn on Darwin, without forcing PID exhaustion.
Document the remaining window after validation and distinguish same-generation
exec from PID reuse. Process sampling is not atomic: any residual false-positive
risk requires an explicit design decision and mitigation, not a claim that the
next poll only corrects temporary identity loss.

## Acceptance criteria

Every pending blocking criterion must pass on the final revision. Historical
passes remain useful baselines, not substitutes for rerunning changed code.

| Area | Required result and evidence | Current disposition |
|---|---|---|
| Root completeness | All-or-nothing Darwin identity per root; consumer tests preserve tool, conversation ID, and hooks on incomplete samples | Blocking, pending |
| PID consistency | Validated process generation across native reads, sampled-parent checks, and deterministic mid-lookup race tests | Blocking, pending |
| Identity and privacy | Exact verified `argv[0]`; ambiguous/empty cases cannot expose arguments or environment values | Blocking, pending design and target tests |
| Metrics and ordering | All identity failures preserve every tree field; accepted direct children retain sampled order | Partial historical coverage; expanded tests pending |
| Darwin process launches | Automated launch-count regression test plus live argv trace: one metrics `ps`, no scoped child-name `ps` | Historical trace available; automated gate and final live run pending |
| Linux and WSL2 | Existing command, parser, selection, and output behavior remain unchanged | Extraction reviewed; final tests and runtime dispositions below |
| API and configuration | `Trees`, `ProcStat`, config, schema, and tool definitions unchanged | Initial diff satisfies boundary; recheck final diff |
| Performance | Bounded comparison for one, two, and five children using the completed safe adapter | Initial Apple Silicon data available; final measurements pending |
| Validation safety | Per-command deadlines, overall deadlines, load checks, independent cleanup, and no default tmux socket | Harness changes and documented live procedure pending |
| Build matrix | CGO-disabled production and focused test binaries compile for all four release targets; repository checks pass | Final logs pending |
| Evidence and recovery | Reproducible live/state artifacts and a verified Darwin-only recovery patch | Blocking, pending |

Performance measurements are evidence, not CI timing assertions. Add the
deterministic no-scoped-launch test explicitly; it does not exist merely because
the current source and historical trace contain no such launch.

## Claim-to-artifact evidence checklist

Record source revision, clean/dirty status, Go and dependency versions, build
commands, binary SHA-256, OS build, architecture, and relevant CLI/tmux/terminal
versions with each final run. Store sanitized artifacts and a manifest with
checksums. Hashes alone do not connect an output file to the claimed binary.

| Claim | Retained evidence | Missing evidence / next gate |
|---|---|---|
| Initial `ps` discontinuity | `2026-09-30-macos-15.5-ps.jsonl`: 120 samples and six summaries; harness and checksum match | Retain as baseline; record safe rerun commands and target metadata |
| Initial native speed | `2026-09-30-macos-15.5-go-bench.txt`: 90 benchmark results; checksum matches | Exact invocation/build provenance and final-adapter rerun pending |
| Initial Darwin sysstat tests pass | `2026-09-30-macos-15.5-sysstat-tests.txt`: verbose passing output; checksum matches | Newly required error, race, privacy, and consumer tests are absent; final run pending |
| Live run had no scoped `ps` | `2026-09-30-macos-15.5-live-ps-calls.txt`: 87 metrics-only argv records; checksum matches | Wrapper, executable resolution, run script, build provenance, and final trace pending |
| Live process stats, responsiveness, waiting state, and liveness | README narrative only | Sanitized TUI frames and session-state snapshots before/after CLI exit pending |
| Relaunch changes the correct row | README describes real Claude followed by `sleep` with `argv[0]=codex` | Before/after session JSON and reproducible commands pending; this is synthetic identity plumbing, not a real Codex run |
| Failures preserve session state | No retained consumer failure evidence | Deterministic tool/conversation/hook assertions and sanitized results pending |
| Test processes and sockets were cleaned up | README narrative only | Owned PID/socket manifest, cleanup transcript, final process/load/memory checks pending |
| All release architectures compile and repository gates pass | Prior build claims, without retained completion logs in this evidence directory | Final revision logs for each build and repository gate pending |
| Recovery is deployable | No prepared recovery artifact | Reviewed patch, build hash, validation results, and application instructions pending |

Rerun missing live evidence rather than reconstructing old frames or state.
Keep synthetic fixtures clearly labeled and never capture real prompts,
credentials, environment dumps, or arbitrary process command lines. A sanitized
fixture frame/session snapshot is allowed evidence; redact it before publishing.

## Coverage matrix

Refresh these inventories against `.goreleaser.yaml` and `builtinTools` before
the PR. "Unchanged" describes reviewed code behavior, not a runtime test pass.
The PR must name untested values and what testing them requires. A maintainer
may accept a stated runtime confidence gap; the blocking identity/privacy,
consumer-state, harness-safety, and recovery gates cannot be waived by omitting
their platform or tool from the matrix.

### Platforms and architectures

| Runtime | Current disposition | Remaining gate or explicit runtime gap |
|---|---|---|
| Darwin arm64, macOS 15.5 | Tested initial sysstat/native benchmark; live behavior partly narrative | Final CGO-disabled build, expanded tests, complete live artifacts, and benchmark |
| Darwin, macOS 26.5.1 / 26.5.2; architecture to record | Untested fixed build on the maintainer/reporter hosts | Bounded affected-host validation; identify actual architecture when a host is available, otherwise disclose |
| Darwin amd64 | Cross-compilation reported; no retained log or Intel runtime evidence | Final CGO-disabled production/test builds; Intel native smoke/runtime test or named gap |
| Linux amd64 | Identity extraction unchanged by review; final check logs pending | Production/test build, race suite, parser/tree tests, consumer tests, and scoped runtime evidence |
| Linux arm64 | Identity extraction unchanged by review; build/runtime evidence absent | Explicit CGO-disabled production/test builds; runtime smoke test or named gap |
| WSL2, Linux amd64/arm64 as applicable | Linux identity path unchanged; runtime untested | Focused tree/parser and live-session checks on available WSL2 architecture; name remaining architecture/runtime gaps |

WSL2 host-stat interop is outside this identity change, but an unchanged Linux
build path alone is not WSL2 runtime evidence. No native Windows or FreeBSD
release build is required by the current release configuration.

### Tools

| `builtinTools` entry | Current disposition | Required evidence or disclosure |
|---|---|---|
| `claude` | Real startup at theme chooser reported; live artifacts pending | Reproduce startup, steady identity, exit, and relaunch with state artifacts |
| `opencode` | Untested native identity runtime | Real installed CLI launch/identity/relaunch smoke test or explicit gap |
| `codex` | Real CLI untested; only synthetic `sleep` named `codex` reported | Real Codex smoke test; keep synthetic plumbing result separate |
| `muse` | Untested native identity runtime | Real CLI smoke test or explicit gap |
| `grok` | Untested native identity runtime | Real CLI smoke test or explicit gap |
| `gemini` | Untested native identity runtime | Real CLI smoke test or explicit gap |
| `hermes` (`hermes --cli`) | Untested native identity runtime | Real CLI smoke test with shipped invocation or explicit gap |
| `terminal` | Tool retyping inapplicable; shell rows must remain shell rows | Root stats/liveness and no-retyping regression; live shell artifact pending |
| `pi` | Untested native identity runtime | Real CLI smoke test or explicit gap |
| `command-code` (`cmd`) | Untested native identity runtime | Real CLI smoke test with shipped invocation or explicit gap |

Record the installed invocation shape and version without hardcoding a catalog
of upstream process names. Cover native binaries and encountered interpreter or
wrapper launches; existing interpreter ambiguity must keep the stored tool.
An unsupported identification shape is a limitation to disclose, not a reason
to guess or add provider-specific heuristics. Healthy unchanged-tool samples
must preserve conversation IDs and hooks as well as the displayed tool.

### Terminals, transport, tmux, and input

| Surface | Current disposition | Required evidence or applicability statement |
|---|---|---|
| Plain local terminal, any emulator | Terminal protocol path unchanged; representative live artifact pending | Record actual emulator/TERM and capture a local disposable run; disclose untested variants |
| Terminal over SSH | No SSH-specific identity branch; runtime artifacts pending | Record client/server placement and capture an SSH-driven run; identity is read on the manager host |
| Mosh/reconnect scenario from the issue | Untested; transport/scheduling behavior unchanged | Disclose gap; no thirteen-manager reproduction or claim to fix abandoned-manager polling |
| tmux 3.1 minimum and newer supported tmux | No new tmux API or version gate | Record tested version; run minimum-version smoke test or name the gap |
| Keyboard | Input implementation unchanged; no new action | Confirm existing live session navigation/relaunch flow; record exercised keys |
| Mouse | Input implementation unchanged; no new clickable surface | Confirm equivalent existing live navigation flow; record exercised controls |
| New key/mouse bindings, clipboard, links, notifications, themes | Inapplicable: this change adds none | State applicability in the PR; do not claim exhaustive terminal feature testing |

## Safe target validation procedure

Status: pending harness corrections. The following is the required procedure,
not a claim that the current harness implements every safeguard.

1. Record the target metadata, process count, load, and memory pressure. Set a
   host-specific load ceiling before launch; the historical 24-CPU host used
   12. Do not carry that ceiling unchanged to a smaller host. Refuse a run above
   the ceiling and check it during each benchmark and live phase.
2. Enforce a two-second deadline on every external benchmark command, including
   initial `/bin/ps -axo pid=` discovery. The Python harness currently leaves
   that discovery unbounded, and the Go benchmark uses `exec.Command` without
   a deadline: correct both before remote reruns. Limit each timeout to the
   remaining overall deadline and abort on timeout, nonzero exit, or unexpected
   row count; do not summarize failed/incomplete samples as a successful run.
3. Use stable existing PIDs for baseline selection and validate their continued
   availability. Run commands serially. Do not create a large process population
   or reproduce the thirteen-manager incident.
4. Start slow-host runs small: three samples per standalone case, and three
   fixed iterations repeated three times per Go sub-benchmark. Give each run a
   30-second overall deadline enforced by an external supervisor. Once the
   safeguards above exist, the intended Go arguments are
   `-run '^$' -bench '^BenchmarkChildNames$' -benchtime=3x -count=3 -timeout=30s`.
   The standalone arguments are `--iterations 3 --timeout 2 --deadline 30`,
   plus the explicitly selected `--max-load` for that host. Record exact
   commands and run order; do not publish these as safe commands for the
   unmodified benchmark code.
5. Keep at most five benchmark sleepers, with a 60-second natural lifetime.
   An independent supervisor owns the deadline and tracked child PIDs; cleanup
   must still run after test timeout, manager failure, or SSH disconnect.
   `go test -timeout` by itself does not reap an external `ps`. Verify ownership
   and process generation before terminating tracked PIDs, use bounded
   escalation, reap owned processes, and verify none remain. Keep comparison
   order recorded; interleave or rotate repetitions before making precise
   speedup claims, and retain individual timings and failures.
6. Run one manager under a throwaway home/store/work directory and pre-created
   short `TMUX_TMPDIR`, unset `TMUX`, and name both the outer and manager sockets
   explicitly. Verify actual socket paths before starting or cleaning up. Limit
   the live phase to two pane roots, disposable children, and one real CLI
   startup at a time, with a separately enforced 60-second phase deadline.
7. Use disposable CLI startup without submitting a model prompt. Capture the
   metrics-only `ps` trace, sanitized fixture frames/state transitions, test
   assertions, timings, and resource counts. Do not log native command buffers,
   arbitrary process command lines, prompts, credentials, or environment values.
8. Stop both explicitly owned tmux servers, reap tracked helpers, verify their
   PIDs and sockets are gone, and recheck load/memory pressure. Copy and checksum
   evidence before removing validated generated binaries or toolchains. Retain
   the cleanup transcript alongside the run manifest.

Do not reuse the historical `100x` / ten-repeat / 60-second recipe on an
affected host: at 135 ms, just the two multi-PID original-`ps` cases would take
about 270 seconds and outlive the sleepers. A three-second old-path call should
hit the two-second safety deadline and be reported as censored/aborted evidence;
do not lengthen deadlines or retry it repeatedly to obtain a median. Continue
native/recovery measurements only as a separately bounded run after cleanup and
a fresh load check. Any larger sample needs a new budget based on the safe
pilot, not an increase in process concurrency.

## Work sequence and file responsibilities

1. Retain the reviewed platform extraction and historical baseline. This part
   is complete for the initial revision, with the evidence limits above.
2. Resolve root completeness, exact argv/privacy, and process-generation
   decisions. Implement the smallest private adapter/policy changes and their
   consumer tests. These are open implementation gates.
3. Correct benchmark deadlines, supervision, validation, and cleanup; prepare
   the reproducible live script and evidence manifest. These are pending.
4. Run repository checks and target tests on the completed revision, then the
   bounded benchmark and live procedure. Update the artifact and coverage
   matrices from observed results. Historical runs do not close these gates.
5. Prepare and verify the recovery patch. Reconcile the architecture/research
   prose with this plan, retaining original raw data and accurate attribution.
6. Prepare one focused PR when authorized. Runtime gaps may be presented for
   maintainer judgment; do not present unresolved safety gates as ready to ship.

| File | Responsibility |
|---|---|
| `internal/sysstat/sysstat.go` | Candidate/root mapping, private transport, validated application and completeness policy with unchanged public API |
| `internal/sysstat/child_names_darwin.go` | Native generation/parent/argv validation; no partial root identity or fallback |
| `internal/sysstat/child_names_linux.go` | Existing scoped command and parser, with no incidental Linux policy change |
| Adjacent sysstat tests | Complete/all-failed roots, ordering, every metrics field, native query failures/races/privacy, launch-count regression, bounded benchmark |
| `internal/ui/panetool_test.go` and relevant poller tests | Persistent tool, conversation ID, hook, and status-consumer invariants |
| `internal/ui/poller.go` / `panetool.go` | Review identity consumer contract; scheduling and status rules stay out of scope |
| `docs/darwin-process-info.md` | Final architecture, design decisions, consistency limits, and failure model |
| `docs/research/darwin-process-info/` | Safe harness, exact commands, build provenance, raw results, sanitized live/state artifacts, cleanup, checksums, and coverage limits |

No production/test changes are implied to have been completed by this document
revision. No config, store schema, settings UI, tool definition, or scheduling
change belongs in this work. A necessary dependency/native-mechanism change
must be resolved explicitly at the privacy/generation decision gate rather
than concealed by the initial "no new dependency" scope assumption.

## Repository and PR verification

Run these gates from a clean worktree using the Go version in `go.mod`, with
tmux installed; skipped tmux-dependent tests are not a pass for live coverage:

```bash
go build ./...
mkdir -p /tmp/amtest && env -u TMUX TMUX_TMPDIR=/tmp/amtest go test -race ./...
gofmt -l .
go vet ./...
```

Cross-compile the production binary and focused test binary with
`CGO_ENABLED=0` for `darwin/arm64`, `darwin/amd64`, `linux/arm64`, and
`linux/amd64`; retain commands and outputs for each. Run the complete sysstat
suite and focused consumer tests on Darwin, then the isolated live procedure.
No timing threshold belongs in the regular test suite. Recheck dependency
integrity and sensitive-output handling after the final adapter changes.

The PR must fill Scope with required behavior, the chosen identity mechanisms,
explicit non-goals, every tested matrix value, and each gap plus the work needed
to close it. Lead with removal of Darwin's expensive child-name process launch;
state that the metrics pass remains `ps` and Linux/WSL2 retain their existing
identity implementation. Link the benchmark and claim-to-artifact matrix, with
source revisions, rather than presenting narrative claims as test results.

Complete the template's Visual evidence section. Retain sanitized before/after
frames when showing session identity/status behavior; explain any unavailable
before state. The syscall/performance mechanism itself may have no meaningful
visual difference, in which case give that explicit applicability explanation
and link traces/timings. A missing visual artifact cannot be treated as proof
that a live frame was inspected.

## Rollout and recovery

Status: recovery patch not prepared; release remains blocked.

Ship without a feature flag only after the blocking gates pass. Monitor issue
reports for missed or incorrect Darwin relaunch detection using sanitized
reproducers and state assertions; never add command-line or environment logging
as telemetry. A native failure may withhold a complete root identity only after
the consumer-state preservation contract is implemented and verified.

Before release, prepare a reviewed Darwin-only recovery patch using the scoped
`ps -xo pid=,ppid=,args= -p ...` form. Record its source/base revision, application
instructions, reproducible build and checksum. Validate PID/row selection,
exit/error behavior, bounded latency, and root/consumer-state preservation on
Darwin; verify Linux's command remains unchanged. Test and disclose any identity
limitations of the recovery parser, including paths with spaces and ambiguous
commands, withholding unsafe identity rather than accepting a false tool name.
Run the same isolated trace and cleanup checks on the recovery build.

For a native regression, the operational recovery is this verified patch/build,
not a bare revert that restores the known pathological scoped command. It is an
explicit short-lived replacement, never an automatic runtime fallback. Revert
alone reinstates the reported performance risk and is not the default procedure.

Neither a recovery build nor a later good poll automatically restores a cleared
conversation ID or removed hook state. The recovery runbook must state that
limitation and provide a supported, non-destructive recovery path if metadata
was already changed; do not promise to reconstruct unknown prior state. Test
recovery with disposable session metadata and verify that applying it causes
no further loss. Prevention of that loss remains the primary release gate.

## Definition of done

The feature is release-ready only when all blocking criteria pass on the final
revision, the design decisions are recorded, repository and architecture checks
are clean, live claims have reproducible artifacts, and the recovery build is
verified. Each tool/platform/terminal confidence gap must be closed or explicitly
named with its required follow-up and accepted as part of the PR scope. Missing
safety tests, privacy proof, or consumer-state preservation are not confidence
gaps that can be scoped out.

Final review must confirm the narrow platform boundary and no unrelated product
or scheduling changes, reconcile historical documentation with the final result,
and complete the PR's Scope and Visual evidence sections. Opening a PR, sending
requests to the issue participants, merging, and releasing remain separately
authorized actions. This document revision performs none of them.
