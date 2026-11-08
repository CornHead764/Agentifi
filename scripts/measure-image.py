#!/usr/bin/env python3
"""Measure a built application image: its size, time to healthy and idle RSS.

The image is started on an empty data volume, as a first install would be, so
the figures compare one commit with the next rather than a household's load.
Prints JSON, and with --summary appends a Markdown table to that file (CI
passes $GITHUB_STEP_SUMMARY).
"""
import argparse
import json
import os
from pathlib import Path
import subprocess
import tarfile
import tempfile
import time
import urllib.error
import urllib.request
import uuid
import zlib


def docker(*argv, check=True):
    return subprocess.run(["docker", *argv], check=check, text=True,
                          stdout=subprocess.PIPE, stderr=subprocess.PIPE).stdout.strip()


def stream_sizes(stream):
    """The (gzipped, plain) sizes of one layer, whichever form `docker save` wrote it in."""
    head = stream.read(1 << 20)
    gzipped = head[:2] == b"\x1f\x8b"
    codec = zlib.decompressobj(31) if gzipped else zlib.compressobj(6, zlib.DEFLATED, 31)
    sizes = [0, 0]
    chunk = head
    while chunk:
        out = codec.decompress(chunk) if gzipped else codec.compress(chunk)
        sizes[0 if gzipped else 1] += len(chunk)
        sizes[1 if gzipped else 0] += len(out)
        chunk = stream.read(1 << 20)
    sizes[1 if gzipped else 0] += len(codec.flush())
    return sizes


def saved_sizes(image, directory):
    """The image's (gzipped, plain) layer sizes, read from `docker save`. The
    containerd image store saves gzipped layers and the classic one plain tars."""
    archive = Path(directory) / "image.tar"
    subprocess.run(["docker", "save", "-o", str(archive), image], check=True)
    compressed = uncompressed = 0
    with tarfile.open(archive) as saved:
        manifest = json.load(saved.extractfile("manifest.json"))
        if len(manifest) != 1:
            raise RuntimeError(f"docker save wrote {len(manifest)} images, not one")
        for layer in manifest[0]["Layers"]:
            gzipped, plain = stream_sizes(saved.extractfile(layer))
            compressed, uncompressed = compressed + gzipped, uncompressed + plain
    return compressed, uncompressed


def registry_size(reference):
    """The compressed size a pull downloads: the manifest's layers and config."""
    manifest = json.loads(docker("buildx", "imagetools", "inspect", "--raw", reference))
    if "layers" not in manifest:
        raise RuntimeError(f"{reference} is not a single-platform image manifest")
    return manifest["config"]["size"] + sum(layer["size"] for layer in manifest["layers"])


def proc_stat_cpu_seconds(pid):
    stat = Path(f"/proc/{pid}/stat").read_text()
    fields = stat[stat.rfind(")") + 2:].split()
    return (int(fields[11]) + int(fields[12])) / os.sysconf("SC_CLK_TCK")


def rss_bytes(pid):
    for line in Path(f"/proc/{pid}/status").read_text().splitlines():
        if line.startswith("VmRSS:"):
            return int(line.split()[1]) * 1024
    raise RuntimeError(f"no VmRSS for {pid}")


def wait_for_quiet(pid, timeout, window=5, threshold=0.02):
    """Seconds until a window passes with the process under threshold CPU."""
    started = time.perf_counter()
    previous_at, previous_cpu = started, proc_stat_cpu_seconds(pid)
    while time.perf_counter() - started < timeout:
        time.sleep(window)
        now, cpu = time.perf_counter(), proc_stat_cpu_seconds(pid)
        if (cpu - previous_cpu) / (now - previous_at) < threshold:
            return now - started
        previous_at, previous_cpu = now, cpu
    raise TimeoutError(f"the server did not go quiet within {timeout:g} seconds")


# No integrations reach out, so the figures are the server's own.
ENVIRONMENT = [
    "SECRET_KEY=measure-image-only-not-a-secret-0123456789",
    "SYNC_ENABLED=false", "SIMPLEFIN_ENABLED=false", "INVESTMENT_NEWS_ENABLED=false",
    "MARKET_PRICES_ENABLED=false", "UPDATE_CHECK_ENABLED=false",
]


def environment_flags():
    return [flag for value in ENVIRONMENT for flag in ("-e", value)]


def start_once(image, volume, port, timeout):
    """Start serve and time `docker start` to the first healthy answer."""
    name = f"agentifi-measure-{uuid.uuid4().hex[:8]}"
    docker("create", "--name", name, "-v", f"{volume}:/data", "-p", f"127.0.0.1:{port}:8000",
           *environment_flags(), image, "serve")
    started = time.perf_counter()
    docker("start", name)
    while True:
        if time.perf_counter() - started > timeout:
            logs = docker("logs", name, check=False)
            docker("rm", "-f", name, check=False)
            raise TimeoutError(f"serve was not healthy within {timeout:g} seconds:\n{logs}")
        try:
            with urllib.request.urlopen(f"http://127.0.0.1:{port}/health", timeout=0.5) as response:
                if response.status == 200:
                    return name, time.perf_counter() - started
        except (urllib.error.URLError, ConnectionError, TimeoutError):
            pass
        time.sleep(0.02)


def mib(value):
    return f"{value / (1 << 20):.1f} MiB"


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("image", help="a tag or id in the local Docker daemon")
    parser.add_argument("--registry-ref", help="name@digest of the same image as pushed, "
                        "for the exact compressed size instead of an estimate")
    parser.add_argument("--runs", type=int, default=3)
    parser.add_argument("--port", type=int, default=18000)
    parser.add_argument("--timeout", type=float, default=60)
    parser.add_argument("--quiet-timeout", type=float, default=120)
    parser.add_argument("--summary", type=Path, help="append a Markdown table to this file")
    parser.add_argument("--title", default="Image measurements")
    args = parser.parse_args()

    with tempfile.TemporaryDirectory(prefix="agentifi-measure-") as directory:
        compressed, uncompressed = saved_sizes(args.image, directory)
    result = {"image": args.image, "uncompressed_bytes": uncompressed}
    if args.registry_ref:
        result["compressed_bytes"], result["compressed_basis"] = registry_size(args.registry_ref), "registry manifest"
    else:
        result["compressed_bytes"], result["compressed_basis"] = compressed, "layers gzipped (estimate)"

    volume = f"agentifi-measure-{uuid.uuid4().hex[:8]}"
    docker("volume", "create", volume)
    samples = []
    try:
        docker("run", "--rm", "-v", f"{volume}:/data", *environment_flags(), args.image, "migrate", "--skip-backup")
        for _ in range(args.runs):
            name, healthy = start_once(args.image, volume, args.port, args.timeout)
            try:
                pid = int(docker("inspect", "-f", "{{.State.Pid}}", name))
                rss_at_healthy = rss_bytes(pid)
                quiet = wait_for_quiet(pid, args.quiet_timeout)
                samples.append({"time_to_healthy_seconds": healthy, "rss_at_healthy_bytes": rss_at_healthy,
                                "time_to_quiet_seconds": quiet, "idle_rss_bytes": rss_bytes(pid)})
            finally:
                docker("rm", "-f", name, check=False)
    finally:
        docker("volume", "rm", "-f", volume, check=False)
    result["samples"] = samples
    for key in samples[0]:
        result[f"median_{key}"] = sorted(sample[key] for sample in samples)[len(samples) // 2]
    print(json.dumps(result, indent=2))

    if args.summary:
        rows = [
            ("Compressed size", mib(result["compressed_bytes"]), result["compressed_basis"]),
            ("Uncompressed size", mib(result["uncompressed_bytes"]), "layers unpacked"),
            ("Time to healthy", f"{result['median_time_to_healthy_seconds'] * 1000:.0f} ms",
             "docker start to the first 200 from /health"),
            ("Idle RSS", mib(result["median_idle_rss_bytes"]),
             f"after {result['median_time_to_quiet_seconds']:.0f} s with CPU under 2%"),
            ("RSS when healthy", mib(result["median_rss_at_healthy_bytes"]), ""),
        ]
        with args.summary.open("a") as out:
            out.write(f"### {args.title}\n\nMedian of {len(samples)} starts on an empty data volume.\n\n")
            out.write("| Measure | Value | Basis |\n| --- | ---: | --- |\n")
            for row in rows:
                out.write("| " + " | ".join(row) + " |\n")
            out.write("\n")


if __name__ == "__main__":
    main()
