# Darwin native child identity development plan

Status: design decisions made; implementation and release verification pending.
The feature is not release-ready. D1-D10 below are decided; there is currently
no item whose decision is blocked. Each record states its implementation
contract, rejected alternatives, reopening condition, required evidence, and
accepted limitation. A decision is not a claim that its code or tests exist.

Use these states consistently: **decided** for architecture, **execution pending**
for unimplemented or unverified requirements, **historical evidence** for the
initial branch's recorded results, and **disclosed confidence gap** only for the
runtime combinations D8 permits a maintainer to accept without a new run. A
**decision blocked** item would require a named missing fact and a bounded
investigation; none remains after source review in this revision.

The intended outcome for issue
[#530](https://github.com/YoanWai/agent-manager/issues/530) remains narrow:
Darwin stops launching the slow multi-PID `ps` command used to name a pane's
direct children. Process-tree metrics, Linux and WSL2 behavior, exported Go
signatures/fields, and user configuration remain unchanged. D1 clarifies the
Darwin meaning of nil versus complete-empty `Children` without adding API fields.

This is the canonical plan. The initial rationale in
[`darwin-process-info.md`](darwin-process-info.md) and historical
[`research evidence`](research/darwin-process-info/README.md) must be reconciled
with these decisions before release. Preserve their original raw measurements;
they do not validate the revised adapter. No production or test change, remote
load, commit, PR, or release is performed by this document revision.

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

Retain shared candidate selection/application in `internal/sysstat`, the existing
machine-wide `ps -axo pid=,ppid=,pcpu=,rss=,time=` metrics pass, and platform files
for child identity. The final Darwin adapter uses the already-pinned `x/sys` and
`purego` dependencies directly for the native calls and a small local parser.
It does not use gopsutil's ambiguous `CmdlineSlice` for child identity. There is
no new dependency or module-version change; gopsutil remains in use elsewhere.

Identity lookup is serial and all-or-nothing per sampled Darwin root. Metrics
and liveness remain independent. No process-identity cache, automatic `ps`
fallback, goroutine fan-out, provider-specific rule, config/schema change,
feature flag, or scheduling change is added. An immutable libc function binding
may be initialized once; this is not a cache of process data. The first pass's
command and five-field parser do not gain start time or executable fields.

Scope includes the private transport, Darwin parser/native-call corrections,
small consumer wiring required by D1, tests, bounded harness, evidence, and the
D9 recovery patch. Full native metrics, a shared watcher, detached-client
backoff, and a public process library remain out of scope. Native work is
proportional to roots, children, and argument-buffer sizes; remove the poller's
stale claim that total polling cost stays flat as sessions are added.

Reviewed concerns that do not require new implementation:

- Linux's extracted command, parser, filtering, and ordering match the previous
  behavior. Native Windows and FreeBSD are not release targets; WSL2 uses Linux.
- The pinned `process.Process{Pid: ...}` handle was valid for `Ppid` and
  `CmdlineSlice`; constructor initialization was not the defect. D2/D3 replace
  these calls to obtain a safe parser and uncached native timestamp precision.
- `refreshOnce` and `runMu` already serialize polls within one manager.
- The five historical evidence checksums match; no credentials or raw process
  command lines were found in those artifacts. Missing evidence remains missing.

## Native identity decisions (D1-D5)

### D1 — Private completeness metadata; no new public field

Decision: decided. Execution: pending.

Chosen contract: extend the private lookup result to
`childNameBatch{named []namedChild, completeByRoot map[int]bool}`. The Darwin
adapter receives the full sampled root set as well as candidate `(pid,parent)`
pairs and supplies exactly one completeness entry per root. Linux returns a
nil completeness map, explicitly selecting its existing partial-result policy.
This documented platform result shape is not a speculative nil fallback.

For Darwin, every expected child must yield exactly one valid identity. Any
missing/duplicate child, native error, invalid argv, parent mismatch, or D3
consistency failure makes that root incomplete and discards all of its names.
Other roots remain eligible. Shared application independently checks membership,
uniqueness, parent, and nonempty command before publishing names in sampled
order; a failed shared check invalidates the root too. Roots with zero sampled
children still undergo the root checks in D3; remove the Darwin early return
that would prevent their complete-empty result.

Publish nil `ProcStat.Children` for an incomplete Darwin root and an allocated
zero-length slice for a complete-empty root. A complete nonempty root has its
ordered names. Keep `Trees` and `ProcStat` signatures/fields unchanged. The
poller checks `Children != nil` before calling `applyRelaunchedTool`; a complete
empty list still makes detection return no change. Existing Linux nil/empty
results retain their prior no-op effects and are not relabeled as proven
complete. No CLI/MCP response currently exports this field.

A partial list can otherwise hide the current tool while exposing another
recognized child, causing `UpdateTool` to clear the conversation ID and
`hooks.Remove` to delete the status mailbox. Here "hook state" means that
mailbox, not deletion of upstream hook registration/configuration.

Rejected: per-child best effort permits that state loss; a public completeness
field is unnecessary for the only consumer; overloading `ProcStat.OK` would
break metrics/liveness; applying strict completeness to Linux would change the
explicitly unchanged platform. Reopen if another consumer needs richer failure
semantics or Linux completeness becomes an explicitly scoped feature.

Required evidence: D4 tests distinguish nil from complete-empty, reject partial
roots, preserve all metrics and ordering, and prove the stored tool,
conversation ID, and mailbox survive failures. Limitation: completeness covers
the sampled candidate set, not a frozen live process graph.

### D2 — Local aligned argv parser through libc sysctl

Decision: decided. Execution: pending, including Darwin ABI/fixture validation.

Chosen mechanism: read `kern.procargs2` locally; do not parse gopsutil's string
slice or substitute executable identity. Use `unix.SysctlUint32("kern.argmax")`
once per lookup batch. Resolve libc `sysctl` with the existing purego
`Dlsym(RTLD_DEFAULT, "sysctl")` and `RegisterFunc`, using the fixed signature
`func(*int32, uint32, *byte, *uintptr, *byte, uintptr) int32`. Cache only that
immutable binding/result. Initialize argument-query setup only when there are
candidates; binding/argmax failure makes every candidate-bearing root incomplete,
while zero-child roots still use D3's metadata-only checks. Pass MIB
`{CTL_KERN, KERN_PROCARGS2, pid}`, a local `argmax+4` byte buffer, its length,
and nil/zero write arguments. Reject zero argmax or a value that cannot fit
`int` plus the four-byte argc header before allocating. Nonzero return is a
query failure; no errno
classification is needed, so do not introduce thread-local errno handling.
Keep buffers/MIB alive through the call and bounds-check sizes before allocation
and slicing. Clear the transient argument buffer on every exit; copy only the
verified first argument to a Go string before clearing it. Do not log raw bytes.

Use a full maximum-size read, not the process-sized two-call `SysctlRaw` helper:
[XNU's short-buffer path](https://github.com/apple-oss-distributions/xnu/blob/xnu-11417.121.6/bsd/kern/kern_sysctl.c#L1553)
can return a suffix rather than a prefix after an argument area grows. Require
`4 <= returnedLength < bufferCapacity`; a saturated buffer is rejected, even
if a valid process happens to fill it exactly. Reject native errors and invalid
lengths without retrying or starting `ps`.

The parser accepts only the native format, with this exact algorithm:

1. Read signed native-endian 32-bit argc from bytes 0-3. Reject argc <= 0.
2. Find the executable-path NUL starting at byte 4. Reject missing/empty path.
   Let `n` be its byte length including NUL. Obtain target pointer width `w`
   from the checked KinfoProc `P_LP64` flag: 8 when set, otherwise 4.
3. Compute `argvStart = 4 + alignUp(n, w)` with checked arithmetic. Require all
   bytes between the path terminator and `argvStart` to be zero; do not skip
   any additional zero bytes. Reject an out-of-range start.
4. A zero at `argvStart` is an empty actual argv[0]: reject the candidate/root.
   Otherwise find its NUL and retain exactly those bytes, including spaces,
   without trimming, tokenizing, decoding lossily, or taking a basename.
5. Walk exactly argc NUL-terminated arguments within the returned byte range
   for structural validation, accepting empty later arguments and allocating
   no strings for them. Bound argc by available bytes. Do not walk, decode,
   retain, or emit envp. The raw sysctl buffer can contain environment bytes
   until it is cleared; this is not a claim that the kernel never copies them.

This offset follows Apple's
[exec string alignment](https://github.com/apple-oss-distributions/xnu/blob/xnu-11417.121.6/bsd/kern/kern_exec.c#L5938):
the hidden `executable_path=` prefix is 16 bytes (a multiple of 4 and 8) and is
stripped by sysctl. The same rule is present in
[XNU 12377](https://github.com/apple-oss-distributions/xnu/blob/xnu-12377.1.9/bsd/kern/kern_exec.c#L6056).
It is an ABI rule, not an OS-version switch. Use header constants with source
references; do not infer width from the manager's architecture or a path.

Rejected: pinned gopsutil parsing can shift args/env into argv[0]; `Exe` changes
invocation semantics for wrappers, aliases, and custom argv[0]; `Name` is
truncated and can call the same parser; rejecting every padded result would
hide ordinary tools. A dependency update/upstream fix or libgetargv would be
valid long-term options, but no already-verified replacement is available and
an extra library/CGO requirement is unnecessary for this small adapter. Raw
Darwin syscall traps are rejected because the pinned x/sys source states that
direct syscalls are unsupported. Existing purego uses the supported libc ABI.
Reopen when a verified upstream API provides this exact contract, or a supported
Darwin version changes the layout/binding behavior.

Required evidence: D4 fixtures for every path-length residue modulo 4/8, real
empty argv[0] with/without later arguments and synthetic env sentinels, argc=0,
spaces, later empty args, malformed/truncated/saturated input, and binding
failure; fuzz the pure parser. An OS-rejected exec shape needs a recorded OS
error plus the parser fixture, not a fabricated runtime pass. Limitation: the
kernel copies mutable process memory; this is not a security/authentication
identity and cannot defend against a process deliberately forging its argv or
stack layout. At-limit or malformed results deliberately lose optional identity.

### D3 — Uncached birth-time keys and a pre-metrics cutoff

Decision: decided. Execution: pending. Do not add a start-time column to `ps`.

Use `unix.SysctlKinfoProc("kern.proc.pid", pid)` to read PID, PPID, `P_LP64`,
and the full `P_starttime` seconds/microseconds tuple together. The generation
key is `(pid, startSeconds, startMicroseconds)`; reject invalid/zero timestamps.
Do not use gopsutil `CreateTime`: it caches and truncates to milliseconds.
[XNU initializes birth time and preserves it across exec](https://github.com/apple-oss-distributions/xnu/blob/xnu-12377.1.9/bsd/kern/kern_fork.c#L1123).
This is a practical generation key, not a guaranteed unique kernel handle.

Use a private platform epoch hook before the existing metrics command. On
Darwin it records paired wall/monotonic time and each requested root's birth
key. A root query failure marks only that root unavailable. On Linux it is a
no-op. Record the wall cutoff immediately before the epoch's first native read.
Require root and child birth timestamps to be strictly earlier than that cutoff
at microsecond precision; defer processes born in the cutoff microsecond or
later to the next poll. This prevents a normal newly reused PID from being
accepted as a candidate from the subsequent `ps` graph without adding fields
to the graph itself.

After the unchanged metrics pass, for each sampled child read KinfoProc A,
read/parse argv once, then read fresh KinfoProc B. Require both PID and PPID to
match the candidate, A/B generation keys and target-width flags to match, and
birth to pass the cutoff. Read the root again after its whole candidate set;
its generation must match the pre-metrics root key. Any failed comparison makes
the root incomplete. Even a zero-child root needs matching before/after keys.
Capture the epoch and batch-end clocks with `time.Now()`, retaining their
monotonic components. At batch end compare their Unix-microsecond difference
with `end.Sub(start)`; invalidate all identity when they differ by more than
1 ms. Retain metrics. The clock check and cutoff are safety
checks on external clock changes, not user settings.

Accepted residuals: birth time is not in the metrics row, so continuity relies
on the earlier-than-cutoff test and ordinary clock behavior; a sub-millisecond
or canceling clock step can evade the clock guard, and timestamp/PID collisions
are not mathematically excluded. Same-generation exec and argv rewrites are not
identified as a new birth; the result may be stale around exec. Children can
appear/disappear after the sampled graph or final check. This design rejects
observed inconsistency; it does not make sampling atomic or promise that every
possible race only causes a temporary omission. Do not add a relaunch debounce
or process-control mechanism in this patch to imply such a guarantee.

Rejected: PPID-only bracketing misses same-parent reuse; cached/millisecond
creation time loses precision; `ps lstart` is formatted and second-resolution,
adds parsing, and cannot deliver an atomic argv snapshot. A second full native
process-table scan expands the collector; task ports/process suspension change
permissions or interfere with user processes. Unique-ID/exec-generation flavors
in `proc_info_private.h` are a potentially stronger option but add a private
ABI dependency and still do not freeze argv; defer them. Reopen on a reproduced
false relaunch despite these checks, relevant clock-step evidence, or a stable
public atomic identity API.

Required evidence: D4 scripted same/different-parent reuse, root reuse, exit,
exec, timestamp precision, cutoff and clock-change cases; a bounded 30-cycle
spawn/exec/exit smoke test with at most two helpers, never PID exhaustion.
Record the accepted residuals in the PR. These are deliberate limits, not an
unresolved choice of generation mechanism.

### D4 — Local function seams and persistent consumer assertions

Decision: decided. Execution: pending.

Pass a private immutable operations struct into the Darwin lookup core: native
KinfoProc reader, caller-sized args reader, argmax reader, and paired clock.
Production supplies real functions; tests supply scripted per-call results.
Keep the pure byte parser separate. Shared tree/application helpers take a
private metrics-reader/lookup dependency value behind unchanged `Trees`.
Add an instance-local tree-sampler function to the poller, initialized to
`sysstat.Trees`; UI tests can supply complete/incomplete `ProcStat` samples.
Do not introduce exported testing hooks or mutable package-global fake readers.

The table-driven tests must cover all cases in D1-D3, two roots with one failure,
all failures, nil versus complete-empty, duplicate/missing/foreign identities,
ordered complete output, and every tree metrics field. UI tests use the real
store and hook manager with a fixture conversation ID and status mailbox; after
an incomplete or complete-empty sample assert unchanged tool/ID/mailbox bytes,
then a healthy original-tool sample, then a complete real relaunch which alone
may update tool and clear old metadata. Also assert the row/status rules follow
the successful change. Test shell/interpreter ambiguity and all current built-in
mappings derived from `config.Default()`, not a second maintained tool list.

Real sysstat fixtures must place the sampled shell root beside the test runner,
not use the runner as that root: its transient metrics `ps` would itself be a
sampled child and correctly invalidate a strict-completeness sample. Use a
bounded pipe handshake for fixture readiness, explicit child reaping, and no
sleep intended to provoke a race. Fuzz malformed bytes; do not stress PID reuse.

Rejected: race-by-timing tests are nondeterministic; testing only returned names
misses store/mailbox effects; global function replacement creates test races;
a public process-provider abstraction is unnecessary. Reopen if a real failure
cannot be expressed by these local seams or a second production consumer needs
them. Required artifacts: verbose targeted output, parser fuzz smoke output,
full race-suite result, and Darwin real-fixture output. Limitation: scripts
prove policy under modeled ordering; only target runs validate native ABI calls.

### D5 — Exercise real Trees with a denying ps shim

Decision: decided. Execution: pending.

Add a Darwin integration test in `internal/sysstat`: an isolated subprocess
runs real `Trees` against a separate real fixture root with two direct children.
Its private PATH starts with a `ps` shim that records argv, permits only the
exact five-field metrics command, emits controlled rows for those live fixture
PIDs, and rejects every other invocation. Assert one logged metrics invocation,
correct ordered native identities, and no scoped lookup. Include complete-empty
and native-error cases; errors still produce no fallback launch. Keep the shim
out of the parent's PATH and retain the log on failure. Linux gets a separate
expectation for its unchanged metrics-plus-scoped command.

Rejected: inspecting source text alone does not execute the path; mocking the
native adapter would miss a process-launch regression; timings cannot prove
absence of a subprocess. Reopen if the command path changes so PATH interception
no longer observes it. Required artifacts: passing structural test and D7's
real live trace with a wrapper that delegates metrics to `/bin/ps`. Limitation:
a PATH shim cannot intercept a newly hardcoded absolute executable; review must
also reject process-launch code/imports in the native adapter and inspect its
chosen dependencies. It is not system-wide tracing of unrelated software.

## Acceptance criteria

These are execution gates, not undecided architecture. All hard gates are
execution pending for D1-D10; historical passes do not validate the new parser,
extra native reads, completeness semantics, or recovery build.

| Gate | Pass condition | Required record |
|---|---|---|
| G1 Identity policy | D1-D4 tests pass for complete/incomplete/empty roots, exact argv, modeled races, every metrics field, and store/mailbox preservation | Unit/fuzz/race and Darwin fixture logs |
| G2 Launch boundary | D5 observes one metrics command and zero identity/fallback processes on Darwin; Linux retains its old invocation | Structural tests and final live argv trace |
| G3 Performance and safety | D6 supervisor self-tests pass; final adapter measurements complete within bounds and median native cost is below both completed `ps` comparisons for 1/2/5 children | Raw timings, allocations, aborts and cleanup logs; censored old-path data is not a fabricated median |
| G4 Reproducible live behavior | D7 artifacts demonstrate metrics, liveness, stable-tool behavior and a valid relaunch, tied to the final binary | Manifest, frame/state snapshots and assertions |
| G5 Release matrix | D8 hard build/runtime/tool/input gates pass; allowed gaps are named for maintainer acceptance | Four build logs, Linux race/vet/format logs, target logs and coverage inventory |
| G6 Recovery | D9 identity-off patch builds and passes no-mutation/metrics tests; runbook is reviewed | Patch/base revision, binary hash, smoke and cleanup evidence |
| G7 Scope and publication | D10 no-flag design, unchanged Linux behavior, no new dependencies/config/schema, accurate companion prose and PR Scope/Visual evidence | Final diff and documentation review |

Timing is a target decision gate, never a CI timing assertion. If a safe old
baseline times out, record it as censored and compare against the completed
`ps -x` run; a timeout is not evidence of a particular speedup. If the corrected
native path loses its performance advantage, reopen D2/D3 implementation cost
before shipping. No fixed universal millisecond claim is required.

## D6 — Bounded serial benchmark and independent supervisor

Decision: decided. Execution: pending; do not run the current unsupervised Go
benchmark on the affected host using its historical settings.

Implement one Python-stdlib supervisor in the research directory, with a worker
process group per phase. Build the target Go test/manager binaries on the build
host first and verify their hashes on the target; compilation is outside the
timed target workload. The supervisor starts separately from the worker's group,
ignores SSH hangup while finishing cleanup, installs termination handlers, and
owns a hard wall deadline. Keep a worker group leader alive until cleanup is
acknowledged, so its group identity cannot be recycled during normal cleanup.
All benchmark children inherit that group. Live tmux daemons are additionally
tracked by actual socket path, PID, and generation because they daemonize.

Chosen limits, with no per-user setting:

- At most one lookup/`ps` at a time, five benchmark sleepers, two live pane
  roots, and one real CLI startup at a time. Sleepers expire naturally at 60 s.
- Load ceiling `max(1, logicalCPUs/2)` for the one-minute average: refuse above
  it, check before each command and from the supervisor every second, and abort
  a phase if exceeded. Record memory pressure before/after; do not create load.
- Each one-shot external query (`ps`, initial discovery, diagnostics, tmux
  control) has a 2 s deadline capped by the remaining phase budget. Long-lived
  fixtures and the test/manager worker instead inherit the phase lifetime:
  baseline/benchmark phases have 30 s wall deadlines; live phases have 60 s.
  Native calls are synchronous and cannot be canceled by Go context; the
  external worker deadline bounds a stuck test.
- On failure, TERM only the owned worker group/verified tmux servers, wait at
  most 1 s, KILL still-owned processes if needed, and reap. Recheck PID generation
  before individual PID termination. Cleanup has a separate 5 s budget; inability
  to prove cleanup is a failed run, not permission to kill an unrelated socket.

For standalone `ps`, use three interleaved samples per case, fixed seed 530,
1/2/5 existing stable PIDs, and arguments
`--iterations 3 --timeout 2 --deadline 30 --max-load <computed-ceiling>`.
Bound the initial `/bin/ps -axo pid=` discovery as well. Verify requested PID set,
row count, and exit status for every sample without retaining command strings.
Abort at the first timeout/nonzero/mismatch and mark the run incomplete.

Revise `BenchmarkChildNames` to run three explicitly named rounds, rotating
native/original-ps/ps-x order and the 1/2/5 child counts each round. Use three
fixed operations per sub-benchmark, not Go's duration calibration, with:
`-test.run '^$' -test.bench '^BenchmarkChildNames$' -test.benchtime=3x
-test.count=1 -test.timeout=30s -test.benchmem` on the prebuilt test binary.
This is 27 sub-benchmarks/81 operations in one supervised phase, with five
sleepers total. Each native operation includes the D3 epoch/root/child checks and
D2 argmax/buffer work; only the common metrics pass is excluded. Report that the
microbenchmark uses one root, and use the two-root live run for integration.
Retain each round and allocations; do not reuse the initial adapter's speedup.

The Go `ps` commands use `exec.CommandContext`, a 2 s timeout, bounded pipe wait,
and process reaping; the supervisor is still required because Go's test timeout
alone does not clean descendants. Supervisor self-tests use disposable commands
that time out, fail, report wrong rows, exceed a mocked load, or lose the worker;
assert serial execution, abort status and no owned processes/sockets remaining.
No large process population or PID-exhaustion test is allowed.

For the live phase, pre-create a short private `TMUX_TMPDIR`, unset `TMUX`, use
a throwaway home/store/work directory, name both outer and manager sockets, and
verify actual socket paths before use/cleanup. Capture only D7's sanitized
fixtures and argv trace; stop/reap all owned helpers, recheck resources, and copy
checksummed evidence before removing generated artifacts. Use no default socket.

Rejected: 100 operations times ten repetitions cannot fit the affected latency
(about 270 s for the two multi-PID original-ps cells alone); concurrent benchmarks
change load and risk the host; test timeout without supervision can orphan `ps`;
raising timeouts to capture the reporter's 3.1 s call defeats the safety bound.
A timeout is censored evidence. Run native/ps-x alone only in a fresh separately
bounded phase after cleanup and load recheck. Reopen limits only after a safe
pilot demonstrates a necessary, explicitly budgeted change. Required artifacts:
supervisor self-tests, raw per-round output, resource samples and cleanup log.
Limitation: a process stuck in an uninterruptible kernel wait may resist signals;
stop testing and report cleanup failure instead of claiming guaranteed recovery.

## D7 — One JSON manifest and a minimum live evidence bundle

Decision: decided. Execution: pending.

For each new run create a directory in `docs/research/darwin-process-info/` with
`manifest.json` using `schema_version: 1`. Required fields are `run_id`,
`started_utc`, `source_commit`, `worktree_clean`, `go_version`, `go_sum_sha256`,
`builds` (GOOS/GOARCH/CGO, exact build argv, binary hash), `host` (OS/build,
architecture, CPU count, memory, initial/final process count/load/pressure),
`tools` (built-in name, version, observed launch shape, disposition),
`terminal` (transport, client/server placement, emulator/TERM, tmux version),
`commands` (argv arrays, timestamps, duration, exit/timeout), `claims`
(claim ID, pass/fail/censored/not-run, artifact paths), and `artifacts`
(relative path, SHA-256, redaction note). Record only approved fixture paths and
nonsecret environment selections such as CGO/GOOS/GOARCH; never an env dump.

Minimum retained files: exact harness/wrapper source and invocation; verbose
tests/build checks; raw Go benchmark text plus supervisor-normalized JSONL
with allocations; metrics-only `ps` trace;
plain TUI frames at healthy, exited, and relaunched states; sanitized session
JSON before/after those states; consumer-test mailbox/ID assertions; owned
socket/PID manifest and cleanup transcript. Real startup and synthetic identity
fixtures must be labeled separately. Record actual input events exercised.
Validate JSON, relative artifact paths, hashes, referenced claim coverage, and
redaction before calling the run complete. Preserve historical files unchanged.

Rejected: README narrative or hashes without a source/binary link cannot prove
execution; full process/environment dumps violate the privacy requirement;
video is unnecessary for the static identity/status checks. Reopen if a required
claim cannot be assessed from the minimal bundle, or artifact size forces a
reviewable external archive with stable hashes. Required evidence: a manifest
checker report plus the bundle. Limitation: a self-recorded manifest establishes
reproducibility and integrity, not independent attestation that a host ran it.

### Historical claim-to-artifact inventory

The following remains historical evidence. Every final claim needs a new D7
record; do not reconstruct missing old frames or label them as observed.

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
| Identity-off recovery is deployable | No prepared recovery artifact | D9 patch, build hash, validation results, and application instructions pending |

Rerun missing live evidence rather than reconstructing old frames or state.
Keep synthetic fixtures clearly labeled and never capture real prompts,
credentials, environment dumps, or arbitrary process command lines. A sanitized
fixture frame/session snapshot is allowed evidence; redact it before publishing.

## D8 — Hard gates versus disclosed confidence gaps

Decision: decided. Execution: pending.

Hard gates are: CGO-disabled production and focused test builds for all four
release targets; the full Linux amd64 race/build/vet/format checks with tmux;
Darwin arm64 native/parser/consumer and structural tests; a safe completed final
benchmark; and one disposable live manager with two roots, real Claude and real
Codex startup/steady/exit/relaunch observations, no model prompt, and both
keyboard and mouse navigation exercised. Real Codex is required because the
prior substitute proved only plumbing. Parser/policy fixtures cover interpreter
and wrapper ambiguity without claiming an unrun CLI works. Unit compatibility
coverage is derived from every current `builtinTools` entry. G1-G7 safety and
evidence requirements are hard gates and cannot be marked not applicable.

Intel Darwin runtime, affected macOS 26.x hosts, Linux arm64 runtime, WSL2
architectures, other real built-in CLI runs, minimum tmux 3.1 runtime, and
additional local/SSH/mosh terminal combinations are disclosed confidence gaps
when unavailable. Record the tested live transport and version. An SSH-only
live run does not become a local-terminal pass, or vice versa. The PR explicitly
names every missing value and the smoke test needed; maintainer acceptance is
recorded in scope/review. A disclosed gap cannot conceal a failed test.

No new action, terminal escape, configuration surface, or version gate is added;
new-keybinding and terminal-feature parity are inapplicable. Existing mouse and
keyboard smoke coverage remains required for the live validation. No change to
upstream defaults or maintained process-name catalog is permitted.

Rejected: testing every Cartesian-product combination before review is not
required by the repository's disclose-and-review policy; cross-compilation alone
cannot replace Darwin runtime testing; one fake `argv[0]` cannot stand in for
all installed tools. Reopen a confidence gap as a hard gate when a change depends
on that ABI/transport or a relevant runtime failure is reported. Required
evidence: manifest dispositions below plus PR scope acceptance. Limitation:
untested combinations remain untested and release claims must say so.

Refresh the following inventories from `.goreleaser.yaml` and `builtinTools`
before the PR. "Unchanged" describes reviewed code, not a runtime pass.

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

## Work sequence and file responsibilities

All steps below are execution pending. The initial platform extraction and
measurements remain historical evidence; no implementation decision is deferred.

1. Implement D1's private completeness contract and D4's local seams/consumer
   assertions, keeping Linux's explicit legacy result policy unchanged.
2. Implement D2's libc binding, maximum-size reader and pure aligned parser;
   implement D3's epoch/birth/parent checks. Run deterministic/parser tests and
   cross-compile before any target run. No dependency update is needed.
3. Add D5's subprocess regression test and D6's supervisor/self-tests. Build
   and hash target binaries locally. Do not deploy a harness that lacks cleanup.
4. Execute G1-G5 under D6 limits, record D7 manifests, and update each D8 matrix
   disposition. A failure keeps the relevant execution gate open and can trigger
   only the specific decision's recorded reopening condition.
5. Prepare D9's identity-off recovery patch/build, execute G6, and review its
   metadata-loss runbook. Keep recovery separate from the normal shipping build.
6. Reconcile companion architecture/research prose, preserve raw historical
   files, complete G7 and PR Scope/Visual evidence, and present one focused PR
   when authorized. No new PR is needed for the design records themselves.

| File | Final responsibility |
|---|---|
| `internal/sysstat/sysstat.go` | Private epoch/lookup wiring, completeness validation and nil/empty publication; unchanged metrics command/parser and exported API |
| `internal/sysstat/child_names_darwin.go` | Root/child native consistency checks and all-or-nothing batch policy |
| `internal/sysstat/procargs_darwin.go` (new) | Local libc binding, bounded raw argument read and aligned pure parser |
| `internal/sysstat/child_names_linux.go` | Unchanged scoped command/parser; explicit legacy completeness result and no-op epoch |
| Adjacent sysstat tests | D1-D5 policy/parser/race/launch checks and D6 bounded benchmark |
| `internal/ui/poller.go` / adjacent tests | Instance-local tree sampler and nil-identity guard; accurate cost comment; no scheduling change |
| `internal/ui/panetool_test.go` | Stored tool, conversation ID, status mailbox and row/status invariants; existing detection rules remain unchanged |
| `docs/darwin-process-info.md` | Final D1-D3 architecture, consistency limits and identity-off recovery semantics |
| `docs/research/darwin-process-info/` | D6 supervisor, D7 manifests/artifacts/checker, historical data, final coverage inventory and recovery runbook |

No production/test changes are completed by this documentation revision. No
`go.mod`/`go.sum`, config, store schema, settings UI, tool definition, or poll
scheduling change is selected. Keep native binding details private and confined
to Darwin files, with no runtime OS switch in shared policy.

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

## D9 — Identity-off operational recovery and metadata-loss response

Decision: decided. Execution: pending recovery patch/build/runbook verification.

Prepare a minimal reviewed patch against the final release revision that makes
Darwin's epoch hook a no-op and returns `completeByRoot[root]=false` with no
names for every root. It must issue no native identity queries and no additional
`ps`. Keep the existing metrics command, CPU/RSS/count accounting and liveness
unchanged; keep Linux untouched. D1 makes every recovered Darwin `Children` nil,
so relaunch detection performs no tool/ID/mailbox mutation. Other normal status
or mailbox updates remain normal application behavior; recovery does not freeze
the whole store. This is a separate recovery build, never an automatic fallback
or runtime setting. Record base revision, patch, build commands, checksum and
installation/reversion instructions in its manifest.

Consequences are explicit: manually switching CLI inside an existing pane no
longer retypes its manager row while this recovery build is in use. The stored
tool and its rules remain in force, potentially showing the wrong status after
a manual swap. Users should open a new row for a different tool. Metrics and
agent/pane liveness continue; the problematic scoped `ps` never returns.

For an incident, stop affected manager UI processes without killing agent tmux
sessions, install the verified recovery build through the normal release/update
path, and preserve a consistent diagnostic store backup plus sanitized session
metadata. Do not automatically edit database rows, restore an entire old database,
or replay hook status files. If a prior conversation ID was cleared, retain the
old row/pane and let the user open the correct tool in a new manager row and use
that tool's existing history/resume picker to select the known conversation.
If the upstream tool cannot recover it, report the loss; do not guess from the
latest conversation in a directory. New authentic hook events can create new
status mailbox state, but cannot reconstruct removed historical contents.

Rejected: bare revert restores the slow call. Darwin `ps -x` is a valid measured
performance workaround, but its flattened `args` cannot prove exact argv[0]
for empty/space-containing arguments; using it for automatic identity recovery
would bypass D2. Preserve it only as historical/diagnostic comparison. A recovery
switch adds permanent configuration and a second shipping path. Reopen the
identity-off choice only when a replacement has passed the full exact-identity
and state-preservation gates, not merely a latency benchmark.

Required evidence: recovery production/test builds for both Darwin architectures,
Darwin arm64 live smoke/trace showing metrics and liveness, nil-identity tests
showing no further tool/ID/mailbox deletions, Linux unchanged checks, and cleanup.
Include already-damaged disposable metadata in a recovery test and verify the
build neither invents old IDs nor causes further identity-driven changes.
Limitation: recovery deliberately disables relaunch detection and never repairs
already-lost metadata automatically; existing agent sessions must be preserved.

## D10 — Ship without a flag after hard gates pass

Decision: decided. Execution: pending G1-G7; currently no-go for release.

Ship one default Darwin-native path and the unchanged Linux path. Add no hidden
environment toggle, config entry, Settings row, schema, or runtime fallback.
Open one focused PR with the acceptance table, D8 gap dispositions, measured
final-adapter results and D9 recovery reference; merge/release only after G1-G7
pass and allowed confidence gaps are explicitly accepted in review. Monitor
reported missed/wrong relaunches using sanitized fixture reproductions and
manifest data, never process-argument/environment telemetry. No outgoing issue
message, PR, merge, or release is authorized by editing this plan.

Rejected: a permanent flag/configurable adapter doubles support paths for an
internal performance fix and conflicts with the product's Settings ownership
without a demonstrated user choice; automatic fallback conceals failure and can
restore load; shipping now would ignore known identity-state defects. Reopen
only if validated platform evidence requires a durable user-visible choice,
with an explicitly scoped UI/config design. Required evidence: final diff and
G1-G7 records plus recovery/runbook review. Limitation: emergency switching
requires installing the prepared recovery binary, so preparation is a hard gate.

## Accepted risks and reopening triggers

| Risk | Chosen boundary/mitigation | Reopen when |
|---|---|---|
| Empty argv or padded native data | D2 computes alignment and rejects empty/malformed/saturated results; no names inferred from later arguments | A supported kernel/CLI invalidates the layout or exact-argv tests |
| PID reuse, root replacement, clock change | D3 fresh microsecond keys, cutoff and clock guard; D1 rejects observed inconsistent roots | Reproduced false relaunch survives these checks; consider a stronger public native mechanism |
| Exec/argv changes or new children after sampling | Accepted non-atomic observation limit; no security-identity claim | Real harmful transient requires a separately justified consumer consistency change |
| Native query denied or ABI binding fails | Entire affected root's identity withheld; metrics and stored identity metadata preserved | A supported same-user CLI persistently cannot be identified |
| Native cost scales with roots/buffer sizes | Final-adapter benchmark includes all added work, allocations and two-root live trace | Bounded data shows native no longer improves the relevant lookup |
| Unsupported runtime combination | D8 disclosure and explicit maintainer acceptance; failed tests never become gaps | A dependency or bug makes that combination relevant to the changed ABI |
| Regression after release | Prepared identity-off recovery; no old pathological command or further identity-driven deletion | Safe exact-identity replacement is validated |

## Definition of done

Release-ready means G1-G7 pass on the final implementation, their D7 records are
complete, and every D8 confidence gap is closed or explicitly accepted in PR
scope/review. The normal and recovery builds must be identified and verified.
A decided algorithm, source citation, green historical run, or disclosed gap
does not substitute for an execution gate. The current feature remains no-go
until those tests, artifacts and recovery work are complete.

Final review confirms the platform boundary, Linux parity, no unrelated product
or scheduling changes, accurate companion documentation, and complete PR Scope
and Visual evidence sections. If new evidence invalidates a chosen contract,
reopen that named record and state the missing fact and bounded experiment;
do not silently weaken a gate or turn ordinary pending execution into an
unspecified architecture question. No decision is blocked as of this revision.

## Source review supporting the decisions

The design used the repository code and pinned module sources, plus Apple's
published source. These are design evidence; the target-runtime tests remain
execution pending.

- [XNU exec path and alignment](https://github.com/apple-oss-distributions/xnu/blob/xnu-11417.121.6/bsd/kern/kern_exec.c#L5938)
  and [the later implementation](https://github.com/apple-oss-distributions/xnu/blob/xnu-12377.1.9/bsd/kern/kern_exec.c#L6056)
  support D2's offset, including preservation of empty argv[0].
- [XNU procargs copyout](https://github.com/apple-oss-distributions/xnu/blob/xnu-11417.121.6/bsd/kern/kern_sysctl.c#L1429)
  supports the hidden-prefix handling, and its short-buffer branch requires
  D2's maximum-sized read and saturated-result rejection.
- [Public process flags](https://github.com/apple-oss-distributions/xnu/blob/xnu-12377.1.9/bsd/sys/proc.h#L169)
  define `P_LP64`; [the sysctl header](https://github.com/apple-oss-distributions/xnu/blob/xnu-12377.1.9/bsd/sys/sysctl.h)
  defines the libc signature and MIB constants.
- [XNU process birth/exec behavior](https://github.com/apple-oss-distributions/xnu/blob/xnu-12377.1.9/bsd/kern/kern_fork.c#L1123)
  and [private unique-ID flavors](https://github.com/apple-oss-distributions/xnu/blob/xnu-12377.1.9/bsd/sys/proc_info_private.h#L45)
  support D3's selected key, exec limitation and rejected stronger private ABI.
- [Pinned gopsutil Darwin methods](https://github.com/shirou/gopsutil/blob/v4.26.8/process/process_darwin.go)
  show the empty-argv limitation and millisecond timestamp conversion;
  [its Process methods](https://github.com/shirou/gopsutil/blob/v4.26.8/process/process.go)
  show creation-time caching.
- [Pinned x/sys Darwin support](https://github.com/golang/sys/blob/v0.48.0/unix/syscall_darwin.go)
  supplies checked KinfoProc access and states that raw Darwin syscall traps
  are unsupported; [pinned purego binding semantics](https://github.com/ebitengine/purego/blob/v0.11.1/func.go)
  provide the existing libc-call mechanism used by D2.
