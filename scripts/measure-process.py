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


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--binary", required=True, type=Path)
    parser.add_argument("--seed-binary", required=True, type=Path)
    parser.add_argument("--transactions", type=int, default=20000)
    parser.add_argument("--runs", type=int, default=5)
    parser.add_argument("--settle", type=float, default=5)
    parser.add_argument("--timeout", type=float, default=30)
    args = parser.parse_args()
    if args.runs < 1 or args.transactions < 32 or args.settle < 0 or args.timeout <= 0:
        parser.error("positive runs/timeout, >=32 transactions and nonnegative settle required")
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
                    time.sleep(args.settle)
                    if process.poll() is not None:
                        raise RuntimeError("serve exited while settling")
                    rss_kib = int(subprocess.check_output(
                        ["ps", "-o", "rss=", "-p", str(process.pid)], text=True).strip())
                    samples.append({"cold_start_ms": cold_ms, "rss_bytes": rss_kib * 1024})
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
                      "samples": samples, "summary": summary}, indent=2))


if __name__ == "__main__":
    main()
