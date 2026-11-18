import base64
import io
import tarfile

import pytest

from freeze_workloads import snapshot_files


def archive_file(tmp_path, entries):
    path = tmp_path / "source.tar"
    with tarfile.open(path, "w") as output:
        for name, content, kind in entries:
            member = tarfile.TarInfo(name)
            if kind == "symlink":
                member.type = tarfile.SYMTYPE
                member.linkname = "/etc/passwd"
                output.addfile(member)
            else:
                raw = content.encode()
                member.size = len(raw)
                output.addfile(member, io.BytesIO(raw))
    return path


def test_frozen_sources_keep_the_original_bytes(tmp_path):
    archive = archive_file(tmp_path, [("snapshot/sub/a.go", "package a\n", "regular")])
    sources = snapshot_files(archive)
    assert set(sources) == {"sub/a.go"}
    assert base64.b64decode(sources["sub/a.go"]) == b"package a\n"


@pytest.mark.parametrize("entries", [
    [("snapshot/../outside", "x", "regular")],
    [("snapshot/link", "", "symlink")],
    [("snapshot/a.go", "first", "regular"), ("snapshot/a.go", "second", "regular")],
])
def test_invalid_snapshot_entries_are_refused(tmp_path, entries):
    archive = archive_file(tmp_path, entries)
    with pytest.raises(ValueError):
        snapshot_files(archive)


def test_empty_snapshot_does_not_count_as_a_workload(tmp_path):
    archive = archive_file(tmp_path, [])
    with pytest.raises(ValueError, match="no regular files"):
        snapshot_files(archive)
