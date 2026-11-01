#!/usr/bin/env python3
"""Build CLI release archives and checksums; optionally sign them locally."""
import argparse
import datetime
import gzip
import hashlib
import io
import json
import os
from pathlib import Path
import re
import subprocess
import sys
import tarfile


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--version", required=True)
    parser.add_argument("--output", type=Path)
    parser.add_argument("--allow-dirty", action="store_true")
    parser.add_argument("--sign", action="store_true")
    default = (Path.home() / "Library/Application Support" if sys.platform == "darwin"
               else Path(os.environ.get("XDG_DATA_HOME", Path.home() / ".local/share"))) / "gohc/signing"
    parser.add_argument("--signing-dir", type=Path, default=Path(os.environ.get("GOHC_SIGNING_DIR", str(default))))
    args = parser.parse_args()
    if not re.fullmatch(r"v[0-9]+\.[0-9]+\.[0-9]+(?:-[A-Za-z0-9.-]+)?", args.version):
        parser.error("Use a version such as v0.1.0")
    repo = Path(__file__).resolve().parent.parent
    def git(*values):
        return subprocess.check_output(["git", *values], cwd=repo, text=True).strip()
    dirty = bool(git("status", "--porcelain"))
    if dirty and not args.allow_dirty:
        parser.error("Commit release changes first, or use --allow-dirty for local candidates")
    commit = git("rev-parse", "HEAD")
    if not args.allow_dirty and git("rev-parse", args.version + "^{commit}") != commit:
        parser.error("Release tag must point to HEAD")
    epoch = int(os.environ.get("SOURCE_DATE_EPOCH", git("show", "-s", "--format=%ct", "HEAD")))
    date = datetime.datetime.fromtimestamp(epoch, datetime.timezone.utc).strftime("%Y-%m-%dT%H:%M:%SZ")
    output = (args.output or repo / "dist" / args.version).resolve()
    if output.exists() and any(output.iterdir()):
        parser.error("Output directory must be empty; refusing to overwrite release files")
    output.mkdir(parents=True, exist_ok=True)
    env = os.environ.copy()
    env.pop("GOOS", None); env.pop("GOARCH", None)
    env["CGO_ENABLED"] = "0"
    subprocess.run(["go", "test", "./..."], cwd=repo, env=env, check=True)
    subprocess.run(["go", "vet", "./..."], cwd=repo, env=env, check=True)
    archives = []
    toolchain = subprocess.check_output(["go", "version"], env=env, text=True).strip()
    commit_label = commit + ("-dirty" if dirty else "")
    for system, arch in [("linux", "amd64"), ("linux", "arm64"), ("darwin", "amd64"), ("darwin", "arm64")]:
        name = f"gohc_{args.version}_{system}_{arch}"
        binary = output / (name + ".binary")
        cross = env.copy(); cross["GOOS"] = system; cross["GOARCH"] = arch
        ldflags = f"-s -w -X main.version={args.version} -X main.commit={commit_label} -X main.buildDate={date}"
        subprocess.run(["go", "build", "-trimpath", "-buildvcs=false", "-ldflags", ldflags, "-o", str(binary), "."], cwd=repo, env=cross, check=True)
        archive = output / (name + ".tar.gz")
        with archive.open("wb") as raw, gzip.GzipFile(filename="", fileobj=raw, mode="wb", mtime=epoch) as compressed, tarfile.open(fileobj=compressed, mode="w") as tar:
            for filename, source, mode in [("gohc", binary, 0o755)] + [(n, repo / n, 0o644) for n in ["LICENSE", "README.md", "config.example.json"]]:
                data = source.read_bytes()
                info = tarfile.TarInfo(name + "/" + filename); info.size = len(data)
                info.mode = mode; info.mtime = epoch; info.uid = info.gid = 0
                tar.addfile(info, io.BytesIO(data))
        binary.unlink()
        archives.append(archive)
        print("Built", archive.name, flush=True)
    sums = output / "SHA256SUMS"
    sums.write_text("".join(hashlib.sha256(p.read_bytes()).hexdigest() + "  " + p.name + "\n" for p in archives))
    if args.sign:
        signing = args.signing_dir.expanduser().resolve()
        config = json.loads((signing / "signing.json").read_text())
        passfile = signing / "passphrase"
        if passfile.stat().st_mode & 0o077:
            parser.error("Signing passphrase must have owner-only permissions")
        secret = passfile.read_bytes()
        signenv = os.environ.copy(); signenv["GNUPGHOME"] = str(signing / "gnupg")
        for p in archives + [sums]:
            subprocess.run(["gpg", "--batch", "--pinentry-mode", "loopback", "--passphrase-fd", "0", "--local-user", config["fingerprint"], "--armor", "--detach-sign", str(p)], input=secret, env=signenv, check=True)
        (output / "public-key.asc").write_bytes((signing / "public-key.asc").read_bytes())
    (output / "build.json").write_text(json.dumps({"version": args.version, "commit": commit_label, "built_at": date, "go": toolchain, "platforms": [p.name for p in archives]}, indent=2) + "\n")
    print("Release files:", output)


if __name__ == "__main__":
    main()
