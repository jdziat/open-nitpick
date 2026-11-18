"""Freeze public performance inputs; download source bytes without executing them."""

import argparse
import base64
import hashlib
import json
from pathlib import Path, PurePosixPath
import re
import subprocess
import tarfile
from urllib.parse import urlparse


def snapshot_files(archive):
    files = {}
    total = 0
    with tarfile.open(archive) as source:
        for member in source:
            parts = PurePosixPath(member.name).parts
            if member.name.startswith("/") or ".." in parts:
                raise ValueError("archive path leaves the snapshot")
            if member.isdir():
                continue
            if not member.isfile() or len(parts) < 2:
                raise ValueError("snapshot contains a link or unsupported entry")
            path = "/".join(parts[1:])
            if path in files or member.size > 8 * 1024 * 1024:
                raise ValueError("duplicate or oversized snapshot file")
            total += member.size
            if total > 128 * 1024 * 1024:
                raise ValueError("snapshot exceeds the total byte limit")
            stream = source.extractfile(member)
            if stream is None:
                raise ValueError("snapshot file has no contents")
            with stream:
                data = stream.read(member.size + 1)
            if len(data) != member.size:
                raise ValueError("snapshot file is incomplete")
            files[path] = base64.b64encode(data).decode("ascii")
    if not files:
        raise ValueError("snapshot contains no regular files")
    return files


def download(target, endpoint, accept=None):
    if target.exists() and target.stat().st_size > 0:
        return
    temporary = target.with_suffix(target.suffix + ".download")
    args = ["gh", "api", endpoint]
    if accept:
        args += ["-H", "Accept: " + accept]
    with temporary.open("wb") as output:
        subprocess.run(args, stdout=output, check=True)
    temporary.replace(target)


def freeze(registration, directory):
    directory.mkdir(parents=True, exist_ok=True)
    manifest = []
    benchmarks = []
    for item in registration["workloads"]:
        url = urlparse(item["url"])
        parts = url.path.strip("/").split("/")
        if url.netloc != "github.com" or len(parts) != 4 or parts[2] != "pull":
            raise ValueError("workload must name a public GitHub pull request")
        if not all(re.fullmatch("[0-9a-f]{40}", item[field]) for field in ("base", "head")):
            raise ValueError("workload revisions must be immutable commit hashes")
        name = item["cohort"]
        if not re.fullmatch("[a-z0-9-]+", name):
            raise ValueError("invalid workload name")
        folder = directory / name / (item["base"] + "-" + item["head"])
        folder.mkdir(parents=True, exist_ok=True)
        diff = folder / "change.diff"
        archive = folder / "source.tar.gz"
        repo = "/".join(parts[:2])
        download(diff, f"repos/{repo}/compare/{item['base']}...{item['head']}", "application/vnd.github.diff")
        download(archive, f"repos/{repo}/tarball/{item['head']}")
        files = snapshot_files(archive)
        contents = folder / "sources.json"
        contents.write_text(json.dumps(files, sort_keys=True))
        manifest.append({
            **item,
            "diff_sha256": hashlib.sha256(diff.read_bytes()).hexdigest(),
            "archive_sha256": hashlib.sha256(archive.read_bytes()).hexdigest(),
            "sources_sha256": hashlib.sha256(contents.read_bytes()).hexdigest(),
            "source_files": len(files),
        })
        benchmarks.append({"Name": name, "Diff": str(diff.resolve()), "Sources": str(contents.resolve())})
    (directory / "manifest.json").write_text(json.dumps(manifest, indent=2) + "\n")
    (directory / "benchmarks.json").write_text(json.dumps(benchmarks, indent=2) + "\n")


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--registration", required=True, type=Path)
    parser.add_argument("--directory", required=True, type=Path)
    args = parser.parse_args()
    freeze(json.loads(args.registration.read_text()), args.directory)


if __name__ == "__main__":
    main()
