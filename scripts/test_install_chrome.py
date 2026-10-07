"""Tests for scripts/install-chrome.sh, run against an invented apt repository
on disk, with packages whose "chrome" is a shell script."""

import hashlib
import io
import os
import pathlib
import shutil
import subprocess
import tarfile

import pytest

SCRIPT = pathlib.Path(__file__).parent / "install-chrome.sh"

# Stands in for dpkg-deb where it is not installed: `-x <deb> <dir>`, enough
# for these packages.
DPKG_DEB_STANDIN = r'''#!/usr/bin/env python3
import io, sys, tarfile
assert sys.argv[1] == "-x"
data = open(sys.argv[2], "rb").read()
assert data.startswith(b"!<arch>\n")
offset = 8
while offset < len(data):
    name = data[offset:offset + 16].decode().strip().rstrip("/")
    size = int(data[offset + 48:offset + 58].decode().strip())
    body = data[offset + 60:offset + 60 + size]
    if name.startswith("data.tar"):
        tarfile.open(fileobj=io.BytesIO(body)).extractall(sys.argv[3], filter="tar")
    offset += 60 + size + size % 2
'''


def tar_xz(files):
    out = io.BytesIO()
    with tarfile.open(fileobj=out, mode="w:xz") as tar:
        for name, (body, mode) in files.items():
            info = tarfile.TarInfo(name)
            info.size = len(body)
            info.mode = mode
            tar.addfile(info, io.BytesIO(body))
    return out.getvalue()


def ar(members):
    out = b"!<arch>\n"
    for name, body in members:
        header = f"{name:<16}{0:<12}{0:<6}{0:<6}{0o100644:<8o}{len(body):<10}`\n"
        out += header.encode() + body + (b"\n" if len(body) % 2 else b"")
    return out


def deb(version):
    control = f"Package: google-chrome-stable\nVersion: {version}\nArchitecture: amd64\n"
    chrome = f"#!/bin/sh\necho 'Google Chrome {version}'\n".encode()
    return ar([
        ("debian-binary", b"2.0\n"),
        ("control.tar.xz", tar_xz({"./control": (control.encode(), 0o644)})),
        ("data.tar.xz", tar_xz({
            "./opt/google/chrome/chrome": (chrome, 0o755),
            "./opt/google/chrome/resources.pak": (b"resources", 0o644),
            "./usr/bin/google-chrome-stable": (b"#!/bin/sh\n", 0o755),
        })),
    ])


class Repository:
    """An apt repository laid out as Google's is."""

    def __init__(self, root):
        self.root = root
        self.stanzas = []

    @property
    def url(self):
        return self.root.as_uri()

    def publish(self, package, version, body=None, sha256=None):
        filename = f"pool/main/g/{package}/{package}_{version}_amd64.deb"
        path = self.root / filename
        path.parent.mkdir(parents=True, exist_ok=True)
        path.write_bytes(body if body is not None else deb(version))
        digest = sha256 or hashlib.sha256(path.read_bytes()).hexdigest()
        self.stanzas.append(
            f"Package: {package}\nVersion: {version}\nArchitecture: amd64\n"
            f"Filename: {filename}\nSize: {path.stat().st_size}\nSHA256: {digest}\n"
        )
        index = self.root / "dists/stable/main/binary-amd64/Packages"
        index.parent.mkdir(parents=True, exist_ok=True)
        index.write_text("\n".join(self.stanzas))


@pytest.fixture
def repo(tmp_path):
    return Repository(tmp_path / "repository")


@pytest.fixture
def run(tmp_path):
    env = dict(os.environ)
    if shutil.which("dpkg-deb") is None:
        bin_dir = tmp_path / "bin"
        bin_dir.mkdir()
        standin = bin_dir / "dpkg-deb"
        standin.write_text(DPKG_DEB_STANDIN)
        standin.chmod(0o755)
        env["PATH"] = f"{bin_dir}{os.pathsep}{env['PATH']}"

    def run(target, repository_url):
        return subprocess.run(
            ["bash", str(SCRIPT), str(target)],
            env={**env, "CHROME_REPOSITORY": repository_url},
            capture_output=True, text=True,
        )

    return run


def installed(target):
    current = target / "current"
    return os.readlink(current) if current.is_symlink() else None


def versions(target):
    return sorted(p.name for p in target.iterdir() if p.is_dir() and not p.is_symlink())


def test_installs_the_stable_package_and_says_where_it_came_from(tmp_path, repo, run):
    repo.publish("google-chrome-beta", "130.0.2.0-1")
    repo.publish("google-chrome-stable", "120.0.1.0-1")
    repo.publish("google-chrome-unstable", "140.0.3.0-1")
    target = tmp_path / "chrome"

    result = run(target, repo.url)

    assert result.returncode == 0, result.stdout + result.stderr
    assert installed(target) == "120.0.1.0-1"
    chrome = target / "current" / "chrome"
    assert os.access(chrome, os.X_OK)
    assert subprocess.run([chrome], capture_output=True, text=True).stdout.strip() \
        == "Google Chrome 120.0.1.0-1"
    source = (target / "current" / "ORIGIN").read_text()
    assert "google-chrome-stable 120.0.1.0-1" in source
    assert "pool/main/g/google-chrome-stable/google-chrome-stable_120.0.1.0-1_amd64.deb" in source
    assert hashlib.sha256(
        (repo.root / "pool/main/g/google-chrome-stable/google-chrome-stable_120.0.1.0-1_amd64.deb").read_bytes()
    ).hexdigest() in source
    assert not (target / "usr").exists()
    assert not list(target.glob(".download.*"))


def test_an_installed_current_stable_is_kept_without_downloading(tmp_path, repo, run):
    repo.publish("google-chrome-stable", "120.0.1.0-1")
    target = tmp_path / "chrome"
    assert run(target, repo.url).returncode == 0
    (repo.root / "pool").rename(tmp_path / "pool-gone")

    result = run(target, repo.url)

    assert result.returncode == 0, result.stdout + result.stderr
    assert "nothing newer" in result.stdout
    assert installed(target) == "120.0.1.0-1"


def test_a_newer_stable_is_switched_to_and_the_one_before_it_kept(tmp_path, repo, run):
    target = tmp_path / "chrome"
    repo.publish("google-chrome-stable", "120.0.1.0-1")
    assert run(target, repo.url).returncode == 0

    repo.stanzas.clear()
    repo.publish("google-chrome-stable", "121.0.1.0-1")
    assert run(target, repo.url).returncode == 0
    assert installed(target) == "121.0.1.0-1"
    assert versions(target) == ["120.0.1.0-1", "121.0.1.0-1"]

    repo.stanzas.clear()
    repo.publish("google-chrome-stable", "122.0.1.0-1")
    assert run(target, repo.url).returncode == 0
    assert installed(target) == "122.0.1.0-1"
    assert versions(target) == ["121.0.1.0-1", "122.0.1.0-1"]


def test_versions_compare_as_versions_not_as_text(tmp_path, repo, run):
    target = tmp_path / "chrome"
    repo.publish("google-chrome-stable", "99.0.1.0-1")
    assert run(target, repo.url).returncode == 0

    repo.stanzas.clear()
    repo.publish("google-chrome-stable", "100.0.1.0-1")
    assert run(target, repo.url).returncode == 0
    assert installed(target) == "100.0.1.0-1"


def test_an_index_older_than_the_installed_chrome_changes_nothing(tmp_path, repo, run):
    target = tmp_path / "chrome"
    repo.publish("google-chrome-stable", "121.0.1.0-1")
    assert run(target, repo.url).returncode == 0

    repo.stanzas.clear()
    repo.publish("google-chrome-stable", "120.0.1.0-1")
    result = run(target, repo.url)

    assert result.returncode == 0
    assert installed(target) == "121.0.1.0-1"
    assert versions(target) == ["121.0.1.0-1"]


def test_a_download_that_fails_its_checksum_leaves_the_installed_chrome(tmp_path, repo, run):
    target = tmp_path / "chrome"
    repo.publish("google-chrome-stable", "120.0.1.0-1")
    assert run(target, repo.url).returncode == 0

    repo.stanzas.clear()
    repo.publish("google-chrome-stable", "121.0.1.0-1", sha256="0" * 64)
    result = run(target, repo.url)

    assert result.returncode == 0
    assert "does not match" in result.stdout
    assert "keeping Chrome 120.0.1.0-1" in result.stdout
    assert installed(target) == "120.0.1.0-1"
    assert versions(target) == ["120.0.1.0-1"]
    assert not list(target.glob(".download.*"))


def test_a_download_that_fails_its_checksum_with_nothing_installed_fails(tmp_path, repo, run):
    repo.publish("google-chrome-stable", "120.0.1.0-1", sha256="0" * 64)
    target = tmp_path / "chrome"

    result = run(target, repo.url)

    assert result.returncode == 1
    assert "no Chrome is installed" in result.stdout
    assert installed(target) is None


def test_no_route_out_keeps_an_installed_chrome(tmp_path, repo, run):
    target = tmp_path / "chrome"
    repo.publish("google-chrome-stable", "120.0.1.0-1")
    assert run(target, repo.url).returncode == 0

    result = run(target, (tmp_path / "unreachable").as_uri())

    assert result.returncode == 0
    assert "could not read" in result.stdout
    assert installed(target) == "120.0.1.0-1"


def test_no_route_out_and_nothing_installed_fails_and_says_how_to_fix_it(tmp_path, run):
    result = run(tmp_path / "chrome", (tmp_path / "unreachable").as_uri())

    assert result.returncode == 1
    assert "no Chrome is installed" in result.stdout
    assert "docker compose up chrome" in result.stdout


def test_an_index_naming_a_path_outside_the_pool_is_refused(tmp_path, repo, run):
    index = repo.root / "dists/stable/main/binary-amd64/Packages"
    index.parent.mkdir(parents=True)
    index.write_text(
        "Package: google-chrome-stable\nVersion: 120.0.1.0-1\n"
        f"Filename: pool/../../escape.deb\nSHA256: {'0' * 64}\n"
    )

    result = run(tmp_path / "chrome", repo.url)

    assert result.returncode == 1
    assert "unexpected version or file" in result.stdout
