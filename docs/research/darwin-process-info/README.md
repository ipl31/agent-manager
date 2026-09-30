# Darwin child-process lookup measurements

Date: 2026-09-30

This records the target-host measurements for the Darwin-native child identity
work described in [`../../darwin-process-info.md`](../../darwin-process-info.md).
The host was a Mac14,14 Mac Studio with an Apple M2 Ultra, 24 logical CPUs,
64 GiB RAM, and macOS 15.5 (24F74). It had 735 processes at the start of the
first run. The initial one-minute load was 1.04 and macOS reported 92% memory
free after each run.

The host is not running the macOS 26.5.1 version from issue #530, so it does
not reproduce that release's approximately 135 ms absolute latency. It does
reproduce the defining discontinuity: an unmodified `ps -p` becomes more than
twice as expensive when its selection grows from one PID to two, and adding
more PIDs does not materially increase that cost.

## Safety boundary

The standalone harness uses existing, stable system PIDs and never creates a
process population. It runs one `ps` at a time, gives each invocation a
two-second deadline, stops after 45 seconds, and aborts before or during the
run if one-minute load exceeds 12 on this 24-CPU host. Its cases are shuffled
with a fixed seed.

The Go benchmark creates exactly five sleeping child processes, launches one
lookup at a time, fixes every sub-benchmark at 100 iterations, repeats it ten
times, and has a 60-second test deadline. The children are killed by test
cleanup and naturally expire after 60 seconds if the benchmark process is
forcibly terminated. After both runs, the host had no benchmark sleepers,
load was 1.38, and memory was still 92% free.

## Existing `ps` behavior

These are medians of 20 interleaved samples from the standalone harness.

| Lookup | 1 PID | 2 PIDs | 5 PIDs |
|---|---:|---:|---:|
| Current `ps -o ... -p` | 1.671 ms | 3.751 ms | 3.892 ms |
| Darwin `ps -xo ... -p` | 1.637 ms | 1.790 ms | 1.876 ms |

The multi-PID current path spent a median 2.50-2.63 ms in child system CPU,
compared with 0.69-0.75 ms for `-x`. Every command returned exactly the
requested row count and exited zero. There were no timeouts or safety aborts.

This confirms that `-x` is an effective Darwin-only hotfix. It is not the
preferred design: it still forks `ps`, remains tens of times slower than
native lookup, and widens selection instead of narrowing it on Linux.

## Native implementation

The Go benchmark compares the implementation in this branch with both `ps`
forms. Each cell is the median of ten benchmark results, each result averaging
100 serial operations.

| Direct children | Native | Current `ps` | Darwin `ps -x` | Current/native |
|---:|---:|---:|---:|---:|
| 1 | 0.028 ms | 1.587 ms | 1.579 ms | 56.5x |
| 2 | 0.032 ms | 3.705 ms | 1.743 ms | 115.3x |
| 5 | 0.080 ms | 3.753 ms | 1.802 ms | 46.8x |

The native lookup uses `kern.proc.pid` for the observed parent and
`kern.procargs2` for `argv`, through gopsutil's Darwin implementation. It
retains only `argv[0]`. Target-host tests also proved that it:

- names a real direct child;
- rejects the same PID when the expected parent is wrong;
- preserves an `argv[0]` containing spaces exactly;
- keeps `Trees` limited to direct children rather than grandchildren.

At agent-manager's default two-second poll interval, 13 managers on this host
would spend about 24 ms of wall time per second in the old five-PID lookup,
versus about 0.5 ms with the native lookup. Applying the issue reporter's
135.1 ms measurement to the same 13 managers yields about 878 ms of serialized
lookup wall time per second, before the machine-wide metrics call and the rest
of a poll. This explains why the regression can become self-sustaining when
many unattended managers poll at once.

## Live application verification

A `CGO_ENABLED=0` Darwin/arm64 build ran under a named outer tmux socket with
a short dedicated `TMUX_TMPDIR`, throwaway `HOME`, disposable store, and
disposable working directory. Two shell sessions each ran a direct `sleep`
child; the manager stayed responsive and displayed their combined tree
memory. A real Claude CLI then remained at its first-run theme chooser without
submitting a prompt or making a model request. The manager classified it as
waiting and accounted for its approximately 316 MiB resident process.

A temporary `ps` wrapper logged argv and delegated to `/bin/ps`. Across 87
full-application calls, every line was exactly:

```text
-axo pid=,ppid=,pcpu=,rss=,time=
```

There were no `-p` calls. This proves the built Darwin application retains the
machine-wide metrics pass while the child-name process launch is gone.

Finally, the Claude process was stopped, leaving its managed pane at the
shell. That shell ran `/bin/sleep` with `argv[0]` set to `codex`. On the next
poll, `agent-manager sessions --json` reported the row's stored tool as
`codex`, proving that native identity still drives relaunch detection. Both
explicit test tmux servers were then stopped, no test process remained, host
load was 0.88, and memory was still 92% free.

## Raw evidence

- `2026-09-30-macos-15.5-ps.jsonl` contains 120 samples plus metadata and six
  summaries. SHA-256:
  `5d2f0f0bd5c2c6949328b31261be5982ec4892325bffcf89e5fda6c072c8b8d9`.
- `2026-09-30-macos-15.5-go-bench.txt` contains all 90 benchmark results and
  the target's Go benchmark metadata. SHA-256:
  `7f31df05551afc297e428720c9ba7211489e2663e2b30018b24e3cbf1d28d709`.
- `darwin_ps_bench.py` is the bounded standalone harness. SHA-256:
  `c315d5e11b52ab45eadd40bccc0626d5dd327ff86bef88786f0a5fcd4cea24d3`.
- `2026-09-30-macos-15.5-live-ps-calls.txt` contains all 87 `ps` argv records
  from the live application run. SHA-256:
  `0004f34829eed86405bb78a6f8b18483cb6b9971a43028ce9418ba24dc5f19f7`.
- `2026-09-30-macos-15.5-sysstat-tests.txt` is the complete verbose target
  test output. SHA-256:
  `5657a92d40ce3ffbfe231d01155ee3e58f98d123cdeda26576cea419f86df94e`.

The raw files deliberately exclude process command lines, environment
variables, prompts, credentials, and the host's network name.
