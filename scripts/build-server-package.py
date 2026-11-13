#!/usr/bin/env python3
"""把标准平台候选转换为 Docker DBX Web 安装器可识别的平铺二进制包。

DBX Store 的五平台候选使用 ``bin/<target>/<binary>``。自托管 Docker DBX 的
Web 安装接口在上传自定义签名包时，会先按 manifest 的
``entrypoints.backend.executable``（``bin/<binary>``）检查文件是否存在，再做
平台目录解析。这里只把 bin 目录里的可执行文件平铺到 manifest 指定的相对路径，
重算 checksums.json；manifest、UI、权限都不变。市场候选不要用本脚本。
"""

from __future__ import annotations

import argparse
import hashlib
import json
import zipfile
from pathlib import Path


def sha256(data: bytes) -> str:
    return hashlib.sha256(data).hexdigest()


def main() -> int:
    parser = argparse.ArgumentParser(description="构建 Docker DBX 平铺安装包")
    parser.add_argument("input", type=Path, help="标准平台候选 .dbxp")
    parser.add_argument("output", type=Path, help="输出 .dbxp")
    parser.add_argument("--target", required=True, help="目标平台，如 linux-x64")
    args = parser.parse_args()

    if args.input.resolve() == args.output.resolve():
        raise SystemExit("输出不能覆盖输入")
    if not args.input.name.endswith(".dbxp") or not args.output.name.endswith(".dbxp"):
        raise SystemExit("输入和输出都必须使用 .dbxp 扩展名")

    with zipfile.ZipFile(args.input) as zin:
        entries = {info.filename: zin.read(info) for info in zin.infolist()}
        dates = {info.filename: info.date_time for info in zin.infolist()}
    manifest = json.loads(entries["manifest.json"])
    executable = manifest["entrypoints"]["backend"]["executable"]
    source_name = f"bin/{args.target}/{Path(executable).name}"
    if source_name not in entries:
        raise SystemExit(f"候选包缺少 {source_name}")
    if executable in entries:
        raise SystemExit(f"候选包已经包含平铺路径 {executable}")

    entries[executable] = entries.pop(source_name)
    checksums = {
        "algorithm": "sha256",
        "files": {
            name: sha256(data)
            for name, data in entries.items()
            if name != "checksums.json"
        },
    }
    entries["checksums.json"] = (
        json.dumps(checksums, indent=2, sort_keys=True) + "\n"
    ).encode("utf-8")

    args.output.parent.mkdir(parents=True, exist_ok=True)
    tmp = args.output.with_name(f".{args.output.name}.tmp")
    with zipfile.ZipFile(tmp, "w", zipfile.ZIP_DEFLATED) as zout:
        for name in sorted(entries):
            info = zipfile.ZipInfo(name, date_time=dates.get(name, (2026, 1, 1, 0, 0, 0)))
            info.compress_type = zipfile.ZIP_DEFLATED
            info.external_attr = (0o100755 if name == executable else 0o100644) << 16
            zout.writestr(info, entries[name])
    tmp.replace(args.output)
    print(f"wrote {args.output}")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
