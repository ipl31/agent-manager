#!/usr/bin/env python3
import argparse
import json
import os
import random
import resource
import statistics
import subprocess
import sys
import time


def run(command, timeout):
    before = resource.getrusage(resource.RUSAGE_CHILDREN)
    started = time.monotonic_ns()
    try:
        completed = subprocess.run(
            command,
            stdout=subprocess.PIPE,
            stderr=subprocess.PIPE,
            check=False,
            timeout=timeout,
        )
        timed_out = False
    except subprocess.TimeoutExpired as exc:
        completed = exc
        timed_out = True
    elapsed_ns = time.monotonic_ns() - started
    after = resource.getrusage(resource.RUSAGE_CHILDREN)
    stdout = completed.stdout or b""
    stderr = completed.stderr or b""
    return {
        "elapsed_ns": elapsed_ns,
        "child_user_ns": round((after.ru_utime - before.ru_utime) * 1e9),
        "child_system_ns": round((after.ru_stime - before.ru_stime) * 1e9),
        "exit_code": None if timed_out else completed.returncode,
        "timed_out": timed_out,
        "rows": len(stdout.splitlines()),
        "stderr": stderr.decode("utf-8", "replace").strip(),
    }


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("--iterations", type=int, default=20)
    parser.add_argument("--timeout", type=float, default=2.0)
    parser.add_argument("--deadline", type=float, default=45.0)
    parser.add_argument("--max-load", type=float, default=12.0)
    args = parser.parse_args()

    if args.iterations < 1 or args.iterations > 100:
        parser.error("iterations must be between 1 and 100")
    if args.deadline <= 0 or args.deadline > 120:
        parser.error("deadline must be between 0 and 120 seconds")

    baseline_load = os.getloadavg()
    if baseline_load[0] > args.max_load:
        raise SystemExit(f"refusing to start: load1 {baseline_load[0]:.2f} > {args.max_load:.2f}")

    pid_text = subprocess.check_output(["/bin/ps", "-axo", "pid="]).decode()
    stable_pids = [int(pid) for pid in pid_text.split() if int(pid) > 0][:5]
    if len(stable_pids) < 5:
        raise SystemExit("need five existing processes")

    cases = []
    for count in (1, 2, 5):
        selected = ",".join(str(pid) for pid in stable_pids[:count])
        cases.append((f"ps_p_{count}", ["/bin/ps", "-o", "pid=,ppid=,args=", "-p", selected]))
        cases.append((f"ps_x_p_{count}", ["/bin/ps", "-xo", "pid=,ppid=,args=", "-p", selected]))

    print(json.dumps({
        "type": "metadata",
        "timestamp": time.strftime("%Y-%m-%dT%H:%M:%SZ", time.gmtime()),
        "iterations": args.iterations,
        "timeout_seconds": args.timeout,
        "deadline_seconds": args.deadline,
        "max_load1": args.max_load,
        "baseline_load": baseline_load,
        "logical_cpus": os.cpu_count(),
        "process_count": len(pid_text.split()),
        "pids": stable_pids,
    }), flush=True)

    deadline = time.monotonic() + args.deadline
    samples = []
    schedule = [case for _ in range(args.iterations) for case in cases]
    random.Random(530).shuffle(schedule)
    for index, (name, command) in enumerate(schedule):
        if time.monotonic() >= deadline:
            print(json.dumps({"type": "abort", "reason": "deadline", "sample": index}), flush=True)
            break
        load = os.getloadavg()
        if load[0] > args.max_load:
            print(json.dumps({"type": "abort", "reason": "load", "load": load, "sample": index}), flush=True)
            break
        result = run(command, args.timeout)
        result.update({"type": "sample", "case": name, "index": index, "load": os.getloadavg()})
        samples.append(result)
        print(json.dumps(result), flush=True)
        if result["timed_out"]:
            print(json.dumps({"type": "abort", "reason": "command timeout", "case": name}), flush=True)
            break

    for name, _ in cases:
        elapsed = [sample["elapsed_ns"] / 1e6 for sample in samples if sample["case"] == name]
        if elapsed:
            print(json.dumps({
                "type": "summary",
                "case": name,
                "samples": len(elapsed),
                "median_ms": statistics.median(elapsed),
                "min_ms": min(elapsed),
                "max_ms": max(elapsed),
            }), flush=True)


if __name__ == "__main__":
    main()
