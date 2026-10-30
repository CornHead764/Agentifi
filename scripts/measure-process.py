#!/usr/bin/env python3
"""Measure exec-to-healthy latency and settled RSS on an invented household."""
import argparse
import json
import math
import os
from pathlib import Path
import platform
import socket
import subprocess
import tempfile
import time
import urllib.error
import urllib.request


def percentile(values, percent):
    ordered = sorted(values)
    return ordered[max(0, math.ceil(len(ordered) * percent / 100) - 1)]


def cpu_seconds(pid):
    if platform.system() == "Linux":
        stat = Path(f"/proc/{pid}/stat").read_text()
        fields = stat[stat.rfind(")") + 2:].split()
        return (int(fields[11]) + int(fields[12])) / os.sysconf("SC_CLK_TCK")
    value = subprocess.check_output(["ps", "-o", "time=", "-p", str(pid)], text=True).strip()
    days, sep, clock = value.partition("-")
    if not sep:
        days, clock = "0", days
    seconds = 0.0
    for part in clock.split(":"):
        seconds = seconds * 60 + float(part)
    return int(days) * 86400 + seconds


def wait_for_quiet(process, timeout, window=5, threshold=0.02):
    started = time.perf_counter()
    previous_time = started
    previous_cpu = cpu_seconds(process.pid)
    while True:
        remaining = timeout - (time.perf_counter() - started)
        if remaining <= 0:
            raise TimeoutError(f"serve did not become quiet within {timeout:g} seconds")
        time.sleep(min(window, remaining))
        if process.poll() is not None:
            raise RuntimeError("serve exited while waiting for quiet")
        now = time.perf_counter()
        cpu = cpu_seconds(process.pid)
        elapsed = now - previous_time
        if elapsed >= window * 0.99 and (cpu - previous_cpu) / elapsed < threshold:
            return now - started
        previous_time, previous_cpu = now, cpu


def rss_bytes(pid):
    return int(subprocess.check_output(["ps", "-o", "rss=", "-p", str(pid)], text=True).strip()) * 1024


def wait_for_stable_rss(process, timeout, window=180, step=5, tolerance=1 << 20):
    """Wait until RSS has moved less than tolerance for window seconds.

    Go forces a GC every two minutes and returns pages after it, so the window
    must outlast that cycle or a plateau before the drop reads as settled.
    """
    started = time.perf_counter()
    anchor, anchor_at = rss_bytes(process.pid), started
    while True:
        if time.perf_counter() - started >= timeout:
            raise TimeoutError(f"RSS did not settle within {timeout:g} seconds")
        time.sleep(step)
        if process.poll() is not None:
            raise RuntimeError("serve exited while settling")
        now, rss = time.perf_counter(), rss_bytes(process.pid)
        if abs(rss - anchor) > tolerance:
            anchor, anchor_at = rss, now
        elif now - anchor_at >= window:
            return rss, now - started


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--binary", required=True, type=Path)
    parser.add_argument("--seed-binary", required=True, type=Path)
    parser.add_argument("--transactions", type=int, default=20000)
    parser.add_argument("--runs", type=int, default=5)
    parser.add_argument("--settle", type=float, default=None,
                        help="use a fixed delay after health instead of waiting for CPU quiet")
    parser.add_argument("--quiet-timeout", type=float, default=120)
    parser.add_argument("--stable-window", type=float, default=180,
                        help="seconds RSS must hold within 1 MiB to count as settled")
    parser.add_argument("--stable-timeout", type=float, default=600)
    parser.add_argument("--timeout", type=float, default=30)
    args = parser.parse_args()
    if (args.runs < 1 or args.transactions < 32 or args.timeout <= 0 or
            args.quiet_timeout < 5 or (args.settle is not None and args.settle < 0)):
        parser.error("positive runs/timeout, >=32 transactions, quiet-timeout >=5 and nonnegative settle required")
    binary, seeder = str(args.binary.resolve()), str(args.seed_binary.resolve())
    samples = []
    with tempfile.TemporaryDirectory(prefix="agentifi-perf-") as directory:
        root = Path(directory)
        # No inherited deployment secrets, .env, network integrations or backups.
        env = {key: value for key, value in os.environ.items()
               if key in ("PATH", "HOME", "TMPDIR", "SYSTEMROOT")}
        env.update(DATA_DIR=str(root / "db"), STORAGE_PATH=str(root / "documents"),
                   SECRET_KEY="seed-development-only-not-a-secret",
                   CREDENTIAL_ENCRYPTION_KEY="seed-development-only-not-a-secret",
                   SYNC_ENABLED="false", SIMPLEFIN_ENABLED="false",
                   INVESTMENT_NEWS_ENABLED="false", MARKET_PRICES_ENABLED="false",
                   UPDATE_CHECK_ENABLED="false", BACKUP_DIR="", DEBUG="false")
        with (root / "process.log").open("w+") as log:
            def command(argv):
                subprocess.run(argv, cwd=root, env=env, stdout=log, stderr=log,
                               check=True, timeout=args.timeout)
            command([binary, "migrate"])
            command([seeder, "--data", env["DATA_DIR"], "--storage-path", env["STORAGE_PATH"],
                     "--households", "1", "--transactions", str(args.transactions),
                     "--seed", "1", "--today", "2026-06-30"])
            for _ in range(args.runs):
                with socket.socket() as sock:
                    sock.bind(("127.0.0.1", 0))
                    port = sock.getsockname()[1]
                env["HTTP_ADDR"] = f"127.0.0.1:{port}"
                started = time.perf_counter()
                process = subprocess.Popen([binary, "serve"], cwd=root, env=env,
                                           stdout=log, stderr=log)
                try:
                    while True:
                        if process.poll() is not None:
                            raise RuntimeError("serve exited before becoming healthy")
                        if time.perf_counter() - started > args.timeout:
                            raise TimeoutError("serve did not become healthy")
                        try:
                            with urllib.request.urlopen(f"http://127.0.0.1:{port}/health", timeout=0.1) as response:
                                if response.status == 200:
                                    break
                        except (urllib.error.URLError, TimeoutError):
                            time.sleep(0.005)
                    cold_ms = (time.perf_counter() - started) * 1000
                    if args.settle is None:
                        quiet_seconds = wait_for_quiet(process, args.quiet_timeout)
                    else:
                        time.sleep(args.settle)
                        quiet_seconds = None
                    if process.poll() is not None:
                        raise RuntimeError("serve exited while settling")
                    sample = {"cold_start_ms": cold_ms, "rss_bytes": rss_bytes(process.pid)}
                    if quiet_seconds is not None:
                        sample["time_to_quiet_seconds"] = quiet_seconds
                        sample["rss_at_quiet_bytes"] = sample["rss_bytes"]
                        sample["rss_bytes"], sample["time_to_settle_seconds"] = wait_for_stable_rss(
                            process, args.stable_timeout, window=args.stable_window)
                    samples.append(sample)
                finally:
                    process.terminate()
                    try:
                        process.wait(timeout=10)
                    except subprocess.TimeoutExpired:
                        process.kill()
                        process.wait()
            log.flush()
    summary = {key: {f"p{p}": percentile([row[key] for row in samples], p)
                     for p in (50, 95)} for key in samples[0]}
    print(json.dumps({"machine": platform.platform(), "transactions": args.transactions,
                      "households": 1, "settle_seconds": args.settle,
                      "quiet_timeout_seconds": args.quiet_timeout if args.settle is None else None,
                      "samples": samples, "summary": summary}, indent=2))


if __name__ == "__main__":
    main()
