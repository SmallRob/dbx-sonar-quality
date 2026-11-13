#!/usr/bin/env python3
"""Roll DBX 仓库签名。

按 DBX 官方 packager 的签名格式，对已校验的未签名 .dbxp 候选追加
signature.json。签名对象是包内 checksums.json 的原始字节，算法 Ed25519。

仅自定义/私有仓库运维者使用；官方 DBX Store 上架仍由 Store 的受保护密钥签名。
"""

from __future__ import annotations

import argparse
import base64
import hashlib
import json
import os
import shutil
import zipfile
from pathlib import Path

try:
    from nacl.signing import SigningKey
except ImportError as exc:  # pragma: no cover - runtime dependency
    raise SystemExit("需要 PyNaCl：py -3.12 -m pip install pynacl") from exc

MAX_PACKAGE_BYTES = 512 * 1024 * 1024
MAX_FILE_BYTES = 256 * 1024 * 1024
MAX_UNCOMPRESSED_BYTES = 1024 * 1024 * 1024


def sha256_bytes(data: bytes) -> str:
    return hashlib.sha256(data).hexdigest()


def load_key(seed_b64: str, expected_public: str | None) -> tuple[SigningKey, str]:
    try:
        seed = base64.b64decode(seed_b64.strip(), validate=True)
    except Exception as exc:
        raise SystemExit(f"DBX_PLUGIN_SIGNING_KEY 不是合法 base64：{exc}") from exc
    if len(seed) != 32:
        raise SystemExit("DBX_PLUGIN_SIGNING_KEY 必须是 32 字节 Ed25519 seed")
    key = SigningKey(seed)
    public_b64 = base64.b64encode(bytes(key.verify_key)).decode("ascii")
    if expected_public and public_b64 != expected_public.strip():
        raise SystemExit("私钥与 DBX_PLUGIN_SIGNING_PUBLIC_KEY 不匹配")
    return key, public_b64


def validate_and_read_checksums(path: Path) -> bytes:
    size = path.stat().st_size
    if size > MAX_PACKAGE_BYTES:
        raise SystemExit(f"包超过 {MAX_PACKAGE_BYTES} 字节：{size}")
    with zipfile.ZipFile(path) as archive:
        infos = archive.infolist()
        if len(infos) > 10000:
            raise SystemExit("包内条目超过 10000")
        names: set[str] = set()
        checksums_raw: bytes | None = None
        actual: dict[str, str] = {}
        total = 0
        for info in infos:
            name = info.filename
            if name.endswith("/"):
                raise SystemExit(f"包内不应包含目录条目：{name}")
            if "\\" in name or name.startswith("/") or ".." in Path(name).parts:
                raise SystemExit(f"包内路径不安全：{name}")
            if name in names:
                raise SystemExit(f"包内存在重复条目：{name}")
            names.add(name)
            if name == "signature.json":
                raise SystemExit("输入包已带 signature.json，不能用未签名候选覆盖")
            if info.file_size > MAX_FILE_BYTES:
                raise SystemExit(f"{name} 超过 {MAX_FILE_BYTES} 字节")
            total += info.file_size
            if total > MAX_UNCOMPRESSED_BYTES:
                raise SystemExit(f"包解压后超过 {MAX_UNCOMPRESSED_BYTES} 字节")
            data = archive.read(info)
            if name == "checksums.json":
                checksums_raw = data
            else:
                actual[name] = sha256_bytes(data)
        if "manifest.json" not in names:
            raise SystemExit("包内缺少 manifest.json")
        if checksums_raw is None:
            raise SystemExit("包内缺少 checksums.json")
        manifest = json.loads(archive.read("manifest.json"))
        checksums = json.loads(checksums_raw)
        if checksums.get("algorithm") != "sha256":
            raise SystemExit("checksums.json 的 algorithm 必须是 sha256")
        declared = checksums.get("files")
        if not isinstance(declared, dict) or len(declared) != len(actual):
            raise SystemExit("checksums.json 未精确覆盖包内文件")
        for name, digest in actual.items():
            expected = declared.get(name)
            if not isinstance(expected, str) or expected.lower() != digest:
                raise SystemExit(f"checksums.json 校验失败：{name}")
    if not isinstance(manifest, dict) or not manifest.get("id") or not manifest.get("version"):
        raise SystemExit("manifest.json 缺少 id/version")
    return checksums_raw


def append_signature(input_path: Path, output_path: Path, key_id: str, signature_b64: str) -> None:
    signature = {
        "algorithm": "ed25519",
        "key_id": key_id,
        "signature": signature_b64,
    }
    payload = json.dumps(signature, indent=2, ensure_ascii=False).encode("utf-8")
    output_path.parent.mkdir(parents=True, exist_ok=True)
    tmp = output_path.with_name(f".{output_path.name}.tmp")
    if tmp.exists():
        tmp.unlink()
    shutil.copy2(input_path, tmp)
    with zipfile.ZipFile(tmp, "a", compression=zipfile.ZIP_DEFLATED) as archive:
        info = zipfile.ZipInfo("signature.json", date_time=(2026, 11, 13, 0, 0, 0))
        info.compress_type = zipfile.ZIP_DEFLATED
        info.external_attr = 0o100644 << 16
        archive.writestr(info, payload)
    if output_path.exists():
        output_path.unlink()
    os.replace(tmp, output_path)


def main() -> int:
    parser = argparse.ArgumentParser(description="给 DBX 未签名候选追加仓库签名")
    parser.add_argument("input", type=Path, help="未签名 .dbxp")
    parser.add_argument("output", type=Path, help="签名后 .dbxp")
    parser.add_argument("--key-id", default=os.environ.get("DBX_PLUGIN_SIGNING_KEY_ID"))
    parser.add_argument("--artifact-metadata", type=Path)
    parser.add_argument("--target")
    parser.add_argument("--artifact-url")
    args = parser.parse_args()

    key_id = args.key_id
    if not key_id:
        raise SystemExit("缺少 --key-id 或 DBX_PLUGIN_SIGNING_KEY_ID")
    if not args.input.name.endswith(".dbxp") or not args.output.name.endswith(".dbxp"):
        raise SystemExit("输入和输出都必须使用 .dbxp 扩展名")
    if args.input.resolve() == args.output.resolve():
        raise SystemExit("签名输出不能覆盖输入候选")

    seed = os.environ.get("DBX_PLUGIN_SIGNING_KEY", "")
    if not seed:
        raise SystemExit("缺少 DBX_PLUGIN_SIGNING_KEY")
    key, _ = load_key(seed, os.environ.get("DBX_PLUGIN_SIGNING_PUBLIC_KEY"))
    checksums_raw = validate_and_read_checksums(args.input)

    signature_b64 = base64.b64encode(key.sign(checksums_raw).signature).decode("ascii")
    append_signature(args.input.resolve(), args.output.resolve(), key_id, signature_b64)

    if args.artifact_metadata:
        if not args.target:
            raise SystemExit("--artifact-metadata 需要 --target")
        signed = args.output.resolve()
        meta = {
            "target": args.target,
            "url": args.artifact_url or signed.name,
            "sha256": sha256_bytes(signed.read_bytes()),
            "size": signed.stat().st_size,
            "signingKeyId": key_id,
        }
        args.artifact_metadata.write_text(
            json.dumps(meta, indent=2, ensure_ascii=False) + "\n", encoding="utf-8"
        )
    print(f"signed {args.output} key_id={key_id}")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
