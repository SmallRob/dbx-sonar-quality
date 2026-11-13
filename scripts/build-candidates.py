# -*- coding: utf-8 -*-
"""Build unsigned candidate packages for all five DBX Store targets.

The official CLI only builds the sidecar for the current runner. This script
cross-compiles from vendored dependencies and creates the same package layout:

    manifest.json
    bin/<target>/dbx-plugin-sonar-quality[.exe]
    assets/*
    ui/*
    checksums.json

Signing is deliberately left to DBX Store.
"""
from __future__ import annotations
import argparse, hashlib, json, os, re, shutil, subprocess, sys, zipfile
from pathlib import Path

ROOT = Path(__file__).resolve().parents[1]
DIST = ROOT / "dist"
CROSS = ROOT / ".crossbuild"
TARGETS = {
    "linux-x64": ("linux", "amd64", ""),
    "linux-arm64": ("linux", "arm64", ""),
    "darwin-x64": ("darwin", "amd64", ""),
    "darwin-arm64": ("darwin", "arm64", ""),
    "windows-x64": ("windows", "amd64", ".exe"),
}
BINARY = "dbx-plugin-sonar-quality"

def sha256_bytes(data: bytes) -> str:
    return hashlib.sha256(data).hexdigest()

def sha256_file(path: Path) -> str:
    h = hashlib.sha256()
    with path.open("rb") as fh:
        for chunk in iter(lambda: fh.read(1 << 20), b""): h.update(chunk)
    return h.hexdigest()

def build(target: str) -> Path:
    goos, goarch, ext = TARGETS[target]
    CROSS.mkdir(parents=True, exist_ok=True)
    out = CROSS / f"{BINARY}-{goos}-{goarch}{ext}"
    env = dict(os.environ)
    env.update({"GOOS": goos, "GOARCH": goarch, "CGO_ENABLED": "0", "GOPROXY": "off", "GOFLAGS": "-mod=vendor"})
    go = env.get("GO_BINARY", "go")
    print(f"[build] {target}")
    subprocess.run([go, "build", "-trimpath", "-buildvcs=false", "-o", str(out), "."],
                   cwd=ROOT / "backend", env=env, check=True)
    return out

def pack(target: str, binary: Path) -> Path:
    manifest_bytes = (ROOT / "manifest.json").read_bytes()
    manifest = json.loads(manifest_bytes)
    pid, version = manifest["id"], manifest["version"]
    files = [("manifest.json", manifest_bytes)]
    for folder in ("assets", "ui"):
        base = ROOT / folder
        if base.exists():
            for path in sorted(base.rglob("*")):
                if path.is_file(): files.append((path.relative_to(ROOT).as_posix(), path.read_bytes()))
    files.append((f"bin/{target}/{BINARY}{binary.suffix}", binary.read_bytes()))
    checksums = {name: sha256_bytes(data) for name, data in files}
    checksum_blob = json.dumps({"algorithm": "sha256", "files": checksums}, ensure_ascii=False, indent=2, sort_keys=True).encode("utf-8")
    DIST.mkdir(parents=True, exist_ok=True)
    out = DIST / f"{pid}-{version}-{target}.dbxp"
    tmp = out.with_suffix(".dbxp.part")
    with zipfile.ZipFile(tmp, "w", zipfile.ZIP_DEFLATED, compresslevel=9) as zf:
        for name, data in files:
            info = zipfile.ZipInfo(name, date_time=(2026, 1, 1, 0, 0, 0))
            info.compress_type = zipfile.ZIP_DEFLATED
            info.external_attr = (0o100000 | (0o755 if name.startswith("bin/") else 0o644)) << 16
            zf.writestr(info, data)
        info = zipfile.ZipInfo("checksums.json", date_time=(2026, 1, 1, 0, 0, 0))
        info.compress_type = zipfile.ZIP_DEFLATED
        info.external_attr = (0o100000 | 0o644) << 16
        zf.writestr(info, checksum_blob)
    tmp.replace(out)
    return out

def main() -> int:
    ap = argparse.ArgumentParser()
    ap.add_argument("--target", action="append", choices=sorted(TARGETS))
    ap.add_argument("--skip-build", action="store_true")
    args = ap.parse_args()
    manifest = json.loads((ROOT / "manifest.json").read_text(encoding="utf-8"))
    version = manifest["version"]
    source = manifest["homepage"].rstrip("/")
    match = re.search(r"github\.com/([^/]+/[^/]+)", source)
    if not match: raise SystemExit("manifest.homepage must be a GitHub repository URL")
    repository = match.group(1)
    targets = args.target or list(TARGETS)
    artifacts = []
    for target in targets:
        goos, goarch, ext = TARGETS[target]
        binary = CROSS / f"{BINARY}-{goos}-{goarch}{ext}"
        if not args.skip_build: binary = build(target)
        if not binary.exists(): raise SystemExit(f"missing built binary: {binary}")
        package = pack(target, binary)
        artifacts.append({"target": target, "url": package.name, "sha256": sha256_file(package), "size": package.stat().st_size})
        print(f"  packed {package.name} ({package.stat().st_size} bytes)")
    release = {"plugin": {"id": manifest["id"], "name": manifest["name"], "description": manifest.get("description", ""),
                          "publisher": manifest["publisher"], "version": version,
                          "permissions": sorted(manifest.get("permissions", []))},
               "artifacts": sorted(artifacts, key=lambda item: item["target"])}
    (DIST / "release-candidates.json").write_text(json.dumps(release, ensure_ascii=False, indent=2) + "\n", encoding="utf-8")
    store = json.loads((ROOT / ".dbx-store.json").read_text(encoding="utf-8"))
    icon = store["icon"]
    if not icon.startswith(("http://", "https://")):
        icon = f"https://raw.githubusercontent.com/{repository}/v{version}/{icon.lstrip('/')}"
    candidate = {"schemaVersion": 1, "id": manifest["id"], "publisher": manifest["publisher"], "version": version,
                 "name": store["name"], "description": store["description"], "icon": icon, "tags": store["tags"],
                 "permissions": sorted(manifest.get("permissions", [])),
                 "source": f"https://github.com/{repository}/tree/v{version}", "homepage": f"https://github.com/{repository}",
                 "license": store["license"], "releaseNotes": store["releaseNotes"],
                 "localizations": store.get("localizations", {}),
                 "targets": [{"target": item["target"],
                              "url": f"https://github.com/{repository}/releases/download/v{version}/{item['url']}",
                              "sha256": item["sha256"], "size": item["size"]} for item in release["artifacts"]]}
    candidate_path = ROOT / "store" / "candidates" / f"{manifest['id']}.json"
    candidate_path.parent.mkdir(parents=True, exist_ok=True)
    candidate_path.write_text(json.dumps(candidate, ensure_ascii=False, indent=2) + "\n", encoding="utf-8")
    for artifact in release["artifacts"]:
        with zipfile.ZipFile(DIST / artifact["url"]) as zf:
            names = zf.namelist()
            if "signature.json" in names: raise SystemExit(f"unsigned candidate contains signature.json: {artifact['url']}")
            if "manifest.json" not in names or not any(name.startswith(f"bin/{artifact['target']}/") for name in names):
                raise SystemExit(f"package layout invalid: {artifact['url']}")
    print(f"wrote {DIST / 'release-candidates.json'} and {candidate_path}")
    return 0

if __name__ == "__main__":
    sys.exit(main())
