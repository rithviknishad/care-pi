#!/usr/bin/env python3
"""preseed.py LOCK_JSON DIST ROOTFS

Extract the default FE bundle and each built FE plug bundle of a solution lock from the dist snapshot
DIST into the root file system ROOTFS of the care-pi image (image/stage-care/02-preseed):

  ROOTFS/var/lib/care-pi/fe/<build id>/            the default FE build (index.html at the root)
  ROOTFS/var/lib/care-pi/plugs/<slug>/<sha12>/     each built FE plug bundle (its entry at the root)

The rules are those of the agent (agent/internal/fe/artifact.go, extractTarGz): the SHA-256 of the lock,
only files and directories, no unsafe path, and one common top directory is removed.
"""
import hashlib
import json
import os
import posixpath
import shutil
import sys
import tarfile


def sha256_of(path):
    h = hashlib.sha256()
    with open(path, "rb") as f:
        for chunk in iter(lambda: f.read(1 << 20), b""):
            h.update(chunk)
    return h.hexdigest()


def clean_members(tar):
    out = []
    for m in tar.getmembers():
        if m.type == tarfile.XGLTYPE:
            continue
        if not (m.isfile() or m.isdir()):
            sys.exit(f"preseed: the archive has the entry {m.name!r} of a type other than file or directory")
        name = posixpath.normpath(m.name.replace("\\", "/"))
        if name == ".":
            continue
        if name.startswith("/") or name == ".." or name.startswith("../"):
            sys.exit(f"preseed: the archive has the unsafe path {m.name!r}")
        out.append((name, m))
    return out


def common_top_dir(members):
    top = ""
    for name, m in members:
        first, _, below = name.partition("/")
        if not below and not m.isdir():
            return ""
        if not top:
            top = first
        elif top != first:
            return ""
    return top


def extract(bundle, dst, want_sha256, need):
    got = sha256_of(bundle)
    if got != want_sha256:
        sys.exit(f"preseed: the SHA-256 of {bundle} is {got}, not {want_sha256} (the lock)")
    tmp = dst + ".tmp"
    shutil.rmtree(tmp, ignore_errors=True)
    os.makedirs(tmp, mode=0o755)
    with tarfile.open(bundle, "r:gz") as tar:
        members = clean_members(tar)
        strip = common_top_dir(members)
        for name, m in members:
            if strip:
                if name == strip:
                    continue
                name = name[len(strip) + 1:]
            target = os.path.join(tmp, *name.split("/"))
            if m.isdir():
                os.makedirs(target, mode=0o755, exist_ok=True)
                continue
            os.makedirs(os.path.dirname(target), mode=0o755, exist_ok=True)
            with tar.extractfile(m) as src, open(target, "wb") as out:
                shutil.copyfileobj(src, out)
            os.chmod(target, 0o644)
    if not os.path.isfile(os.path.join(tmp, need)):
        sys.exit(f"preseed: the bundle {bundle} has no {need} at its root")
    for root, dirs, _ in os.walk(tmp):
        for d in dirs:
            os.chmod(os.path.join(root, d), 0o755)
    os.chmod(tmp, 0o755)
    shutil.rmtree(dst, ignore_errors=True)
    os.makedirs(os.path.dirname(dst), mode=0o755, exist_ok=True)
    os.rename(tmp, dst)
    for root, dirs, files in os.walk(dst):
        for n in dirs + files:
            os.lchown(os.path.join(root, n), 0, 0)
    os.lchown(dst, 0, 0)
    print(f"preseed: {bundle} -> {dst}")


def main():
    if len(sys.argv) != 4:
        sys.exit(__doc__)
    lock = json.load(open(sys.argv[1]))
    dist, rootfs = sys.argv[2], sys.argv[3]
    fe = lock["fe"]
    extract(os.path.join(dist, fe["url"]), os.path.join(rootfs, "var/lib/care-pi/fe", fe["build_id"]), fe["sha256"], "index.html")
    for pid in sorted(lock.get("plugs") or {}):
        f = (lock["plugs"][pid] or {}).get("frontend")
        if not f or f.get("source") != "built":
            continue
        dst = os.path.join(rootfs, "var/lib/care-pi/plugs", f["slug"], f["sha"][:12])
        extract(os.path.join(dist, f["url"]), dst, f["sha256"], f["entry"])


if __name__ == "__main__":
    main()
