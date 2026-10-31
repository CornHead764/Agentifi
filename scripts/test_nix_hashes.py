"""Tests for scripts/nix-hashes.sh, run in a copy of the script beside invented
Nix files, with a stand-in `nix` that reports a hash mismatch for each stale
hash it finds in them."""

import json
import os
import pathlib
import shutil
import subprocess

import pytest

SCRIPT = pathlib.Path(__file__).parent / "nix-hashes.sh"

OLD_GO = "sha256-" + "A" * 43 + "="
NEW_GO = "sha256-" + "B" * 42 + "+="
OLD_GEN = "sha256-" + "C" * 42 + "/="
NEW_GEN = "sha256-" + "D" * 43 + "="

# Stands in for `nix build`: fails with Nix's own mismatch wording for the
# first stale hash still written in the files (or for the first one listed,
# with NIX_STANDIN_ALWAYS), and passes once none is.
NIX_STANDIN = r'''#!/usr/bin/env python3
import json, os, pathlib, sys
root = pathlib.Path.cwd()
text = "".join(p.read_text() for p in [root / "flake.nix", *sorted((root / "nix").glob("*.nix"))])
if os.environ.get("NIX_STANDIN_FAIL"):
    print("error: builder for '/nix/store/x-agentifi.drv' failed with exit code 1", file=sys.stderr)
    sys.exit(1)
for old, new in json.loads(os.environ["NIX_STANDIN_STALE"]):
    if old in text or os.environ.get("NIX_STANDIN_ALWAYS"):
        print("error: hash mismatch in fixed-output derivation '/nix/store/x-agentifi-go-modules.drv':", file=sys.stderr)
        print(f"         specified: {old}", file=sys.stderr)
        print(f"            got:    {new}", file=sys.stderr)
        sys.exit(1)
'''


@pytest.fixture
def repo(tmp_path):
    (tmp_path / "scripts").mkdir()
    shutil.copy(SCRIPT, tmp_path / "scripts" / SCRIPT.name)
    (tmp_path / "nix").mkdir()
    (tmp_path / "flake.nix").write_text("{ outputs = _: { }; }\n")
    (tmp_path / "nix" / "package.nix").write_text(
        "{\n"
        f'  generated = {{ vendorHash = "{OLD_GEN}"; }};\n'
        f'  vendorHash = "{OLD_GO}";\n'
        "}\n"
    )
    (tmp_path / "nix" / "playwright-driver.nix").write_text('{ hash = "sha256-' + "E" * 43 + '="; }\n')
    bin_dir = tmp_path / "bin"
    bin_dir.mkdir()
    (bin_dir / "nix").write_text(NIX_STANDIN)
    (bin_dir / "nix").chmod(0o755)
    return tmp_path


def run(repo, stale, **env):
    return subprocess.run(
        [str(repo / "scripts" / SCRIPT.name)],
        cwd=repo / "nix",
        env={**os.environ, "PATH": f"{repo / 'bin'}:{os.environ['PATH']}", "NIX_STANDIN_STALE": json.dumps(stale), **env},
        capture_output=True,
        text=True,
    )


def test_replaces_every_stale_hash_until_the_build_passes(repo):
    result = run(repo, [[OLD_GO, NEW_GO], [OLD_GEN, NEW_GEN]])

    assert result.returncode == 0, result.stderr
    assert result.stdout.splitlines() == [f"{OLD_GO} {NEW_GO}", f"{OLD_GEN} {NEW_GEN}"]
    package = (repo / "nix" / "package.nix").read_text()
    assert f'vendorHash = "{NEW_GO}";' in package
    assert f'generated = {{ vendorHash = "{NEW_GEN}"; }};' in package


def test_a_passing_build_changes_nothing(repo):
    before = (repo / "nix" / "package.nix").read_text()

    result = run(repo, [])

    assert result.returncode == 0, result.stderr
    assert result.stdout == ""
    assert (repo / "nix" / "package.nix").read_text() == before


def test_another_build_failure_is_not_a_hash_to_fix(repo):
    before = (repo / "nix" / "package.nix").read_text()

    result = run(repo, [[OLD_GO, NEW_GO]], NIX_STANDIN_FAIL="1")

    assert result.returncode == 1
    assert "without a hash mismatch" in result.stderr
    assert (repo / "nix" / "package.nix").read_text() == before


def test_a_hash_written_twice_is_left_for_a_person(repo):
    package = repo / "nix" / "package.nix"
    package.write_text(package.read_text().replace(OLD_GEN, OLD_GO))
    before = package.read_text()

    result = run(repo, [[OLD_GO, NEW_GO]])

    assert result.returncode == 1
    assert "2 times" in result.stderr
    assert package.read_text() == before


def test_a_mismatch_for_a_hash_not_in_the_files_fails(repo):
    result = run(repo, [["sha256-" + "F" * 43 + "=", NEW_GO]], NIX_STANDIN_ALWAYS="1")

    assert result.returncode == 1
    assert "0 times" in result.stderr
