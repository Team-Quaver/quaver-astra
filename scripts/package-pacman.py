#!/usr/bin/env python3
"""Create a Pacman package payload without requiring makepkg."""

import argparse
import hashlib
import tarfile
from pathlib import Path


def digest(path: Path, algorithm: str) -> str:
    h = hashlib.new(algorithm)
    with path.open("rb") as fh:
        for block in iter(lambda: fh.read(1024 * 1024), b""):
            h.update(block)
    return h.hexdigest()


def normalize_mode(path: Path, is_dir: bool) -> str:
    mode = path.stat().st_mode & 0o7777
    if is_dir:
        # Normalize directory permissions so package installation remains sane.
        return f"{0o755:o}"
    return f"{mode:o}"


def make_mtree(payload: Path, pkginfo: Path) -> str:
    lines = ["#mtree"]
    entries = [pkginfo]
    entries.extend(sorted(p for p in payload.rglob("*")))

    for path in entries:
        is_dir = path.is_dir()
        stat = path.stat()
        relative = path.relative_to(payload).as_posix() if path.is_relative_to(payload) else path.name
        line = [
            relative,
            "time=0",
            f"mode={normalize_mode(path, is_dir)}",
            "uid=0",
            "gid=0",
        ]
        if is_dir:
            line.append("type=dir")
        else:
            line.extend([
                "type=file",
                f"size={stat.st_size}",
                f"md5digest={digest(path, 'md5')}",
                f"sha256digest={digest(path, 'sha256')}",
            ])
        lines.append(" ".join(line))
    return "\n".join(lines) + "\n"


def add_tar_entry(tar: tarfile.TarFile, arcname: str, path: Path, is_dir: bool) -> None:
    info = tar.gettarinfo(str(path), arcname=arcname)
    info.uid = 0
    info.gid = 0
    info.uname = "root"
    info.gname = "root"
    info.mtime = 0
    if is_dir:
        info.type = tarfile.DIRTYPE
        info.mode = 0o755
        info.size = 0
        tar.addfile(info)
        return

    info.mode = path.stat().st_mode & 0o7777
    with path.open("rb") as fh:
        tar.addfile(info, fh)


def create_archive(payload: Path, pkginfo: Path, mtree: Path, output: Path) -> None:
    with tarfile.open(output, mode="w", format=tarfile.GNU_FORMAT) as tar:
        add_tar_entry(tar, ".PKGINFO", pkginfo, False)
        add_tar_entry(tar, ".MTREE", mtree, False)
        for path in sorted(payload.rglob("*"), key=lambda p: p.as_posix()):
            arcname = path.relative_to(payload).as_posix()
            add_tar_entry(tar, arcname, path, path.is_dir())


def main() -> None:
    parser = argparse.ArgumentParser()
    parser.add_argument("--name", required=True)
    parser.add_argument("--version", required=True)
    parser.add_argument("--pkgrel", default="1", required=True)
    parser.add_argument("--arch", required=True)
    parser.add_argument("--desc", required=True)
    parser.add_argument("--url", required=True)
    parser.add_argument("--license", required=True)
    parser.add_argument("--depend", action="append", default=[], required=True)
    parser.add_argument("--payload", required=True, type=Path)
    parser.add_argument("--output", required=True, type=Path)
    args = parser.parse_args()
    if not args.pkgrel.isdigit():
        parser.error(f"pkgrel must be a positive integer: {args.pkgrel}")

    payload = args.payload.resolve()
    if not payload.is_dir():
        parser.error(f"payload is not a directory: {payload}")
    args.output.parent.mkdir(parents=True, exist_ok=True)

    installed_size = sum(p.stat().st_size for p in payload.rglob("*") if p.is_file())
    pkginfo = "\n".join([
        "pkgname = " + args.name,
        "pkgbase = " + args.name,
        # Pacman's .PKGINFO stores the complete EVR without the epoch:
        # pkgver-pkgrel. A bare "5" is rejected as an invalid package
        # version, even though the filename itself is otherwise valid.
        "pkgver = " + args.version + "-" + args.pkgrel,
        "pkgdesc = " + args.desc,
        "url = " + args.url,
        "builddate = 0",
        "packager = Quaver Astra CI <ne0w0r1d@ne0w0r1d.top>",
        "size = " + str(installed_size),
        "arch = " + args.arch,
        "license = " + args.license,
        *(f"depend = {value}" for value in args.depend),
        "xdata = pkgtype=pkg",
    ]) + "\n"

    staging = args.output.parent / f".{args.output.name}.pkgwork"
    staging.mkdir(parents=True, exist_ok=True)
    pkginfo_path = staging / ".PKGINFO"
    pkginfo_path.write_text(pkginfo)
    mtree_path = staging / ".MTREE"
    mtree_path.write_text(make_mtree(payload, pkginfo_path))

    tar_path = staging / "package.tar"
    create_archive(payload, pkginfo_path, mtree_path, tar_path)
    if args.output.exists():
        args.output.unlink()
    tar_path.replace(args.output)
    # Keep only the generated archive; staging metadata is no longer needed.
    for path in (pkginfo_path, mtree_path):
        path.unlink(missing_ok=True)
    staging.rmdir()


if __name__ == "__main__":
    main()
