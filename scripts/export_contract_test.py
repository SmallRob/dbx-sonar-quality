# -*- coding: utf-8 -*-
"""按 DBX 宿主桥契约回归 Markdown 导出。

宿主真实契约（取自 DBX 的 PluginWorkbenchHost）：

1. 分块保存
   fileTransfer.beginSave({name,contentType,size}) -> {handleId, chunkBytes} | null
   fileTransfer.write({handleId,offset}, data)      data 为 base64 字符串或 ArrayBuffer，
                                                    单块上限 8 MiB
   fileTransfer.finish(handleId) / cancel(handleId)
2. 单次保存
   saveFile(options, data)：data 缺失时只发 options，桥内没有 dataBase64 就抛
   `host.saveFile requires transferred binary data or dataBase64`；并且桥对
   整包有 2 MB 级上限（大文件会报 `Plugin bridge request is too large`）。

0.1.9 只传 `{suggestedName, content}`，所以导出必然失败；0.1.10 改为优先分块、
退到单次、最后回落预览。本脚本用同一套校验逻辑跑这几条路径。
"""
from __future__ import annotations

import subprocess
import sys
import tempfile
from pathlib import Path

ROOT = Path(__file__).resolve().parents[1]
UI = ROOT / "ui" / "index.html"
FUNCTIONS = [
    "function bytesToBase64",
    "function toBase64Utf8",
    "function exportFileName",
    "async function saveExportFile",
    "async function deliverExport",
    "function openExportPreview",
    "function exportToast",
]
STUBS = """
const assert = require("assert");
const previews = [];
const toasts = [];
let saved = null;
const state = {};
const $ = () => ({
  set textContent(v) { previews.push(["name", v]); },
  set value(v) { previews.push(["body", v]); },
  classList: { add() { previews.push(["open"]); } },
});
const toast = (m) => toasts.push(m);
const num = (v) => Number(v || 0).toLocaleString("en-US");
function errorMessage(e) { return e instanceof Error ? e.message : String(e); }
function openCount() { return previews.filter((p) => p[0] === "open").length; }
"""

BRIDGE = """
const BRIDGE_LIMIT = 1_500_000; // 介于 1 MiB 与 2 MiB 之间，模拟 web 宿主整包上限
function hostSaveFile(options = {}, data) {
  let meta = options || {};
  if (data === undefined) throw new Error("host.saveFile requires transferred binary data or dataBase64");
  if (typeof data === "string") meta = { ...meta, dataBase64: data };
  if (typeof meta.dataBase64 !== "string") throw new Error("host.saveFile requires transferred binary data or dataBase64");
  if (!meta.fileName) throw new Error("host.saveFile requires fileName");
  if (meta.dataBase64.length > BRIDGE_LIMIT) throw new Error("Plugin bridge request is too large");
  saved = { name: meta.fileName, bytes: Buffer.from(meta.dataBase64, "base64") };
}

function hostTransfer({ cancel = false } = {}) {
  const buffers = new Map();
  const offsets = [];
  return {
    beginSave: async ({ name }) => (cancel ? null : { handleId: "h1", chunkBytes: 262144, name }),
    write: async (handleId, offset, data) => {
      assert.strictEqual(handleId, "h1", "write(handleId, offset, data) 是位置参数");
      assert.ok(data instanceof Uint8Array, "分块以 Uint8Array 视图交给宿主 transfer");
      assert.ok(data.byteLength <= 8 * 1024 * 1024, "单块不得超过 8 MiB");
      offsets.push(offset);
      buffers.set(offset, Buffer.from(data));
      return { written: data.byteLength, nextOffset: offset + data.byteLength };
    },
    finish: async () => {
      const ordered = [...buffers.keys()].sort((a, b) => a - b);
      saved = { name: "chunked", bytes: Buffer.concat(ordered.map((k) => buffers.get(k))), offsets, chunks: ordered.length };
    },
    cancel: async () => { saved = null; },
  };
}
"""

CHECKS = """
(async () => {
  const md = "# 导出\\n\\n中文内容 ✓\\n" + "x".repeat(200000);
  const big = "# 导出\\n\\n" + "y".repeat(3_000_000);
  const result = { markdown: md, filename: "proj-issues-blocker-20261123-1438.md", exported: 19, total: 19 };

  // 1. 宿主什么都没有 -> 预览
  global.window = {};
  await deliverExport(result);
  assert.strictEqual(openCount(), 1, "缺少保存接口时应打开预览");
  assert.ok(toasts[0].includes("未提供保存接口"), toasts[0]);

  // 2. 分块保存 -> 内容无损，偏移连续
  saved = null; previews.length = 0; toasts.length = 0;
  global.window = { dbxPlugin: { fileTransfer: hostTransfer() } };
  await deliverExport({ ...result, markdown: big });
  assert.ok(saved, "分块保存应当成功");
  assert.strictEqual(saved.bytes.toString("utf8"), big, "分块拼接必须无损");
  assert.ok(saved.chunks > 1, "3 MB 内容应分成多块");
  assert.deepStrictEqual(saved.offsets, saved.offsets.slice().sort((a, b) => a - b), "偏移必须递增");
  assert.strictEqual(openCount(), 0, "保存成功后不应打开预览");

  // 3. 只有 saveFile -> 小文件成功，大文件被桥上限拒绝后回落预览
  saved = null; previews.length = 0; toasts.length = 0;
  global.window = { dbxPlugin: { saveFile: hostSaveFile } };
  await deliverExport(result);
  assert.strictEqual(saved.bytes.toString("utf8"), md, "saveFile base64 往返必须无损");
  assert.strictEqual(openCount(), 0);

  saved = null; previews.length = 0; toasts.length = 0;
  await deliverExport({ ...result, markdown: big });
  assert.strictEqual(openCount(), 1, "超过桥上限应回落预览");
  assert.ok(toasts[0].includes("已打开 Markdown 预览"), toasts[0]);

  // 4. 用户取消保存 -> 不写文件、不打开预览
  saved = null; previews.length = 0; toasts.length = 0;
  global.window = { dbxPlugin: { fileTransfer: hostTransfer({ cancel: true }) } };
  await deliverExport(result);
  assert.strictEqual(openCount(), 0, "取消保存不应打开预览");
  assert.ok(toasts[0].includes("已取消保存"), toasts[0]);

  // 5. 宿主写盘抛错 -> 回落预览并保留内容
  saved = null; previews.length = 0; toasts.length = 0;
  global.window = { dbxPlugin: { fileTransfer: { ...hostTransfer(), write: async () => { throw new Error("disk full"); } } } };
  await deliverExport(result);
  assert.strictEqual(openCount(), 1, "写盘失败应回落预览");
  assert.ok(toasts[0].includes("已打开 Markdown 预览"), toasts[0]);

  console.log("export contract ok: chunked 3 MB, single-shot, cancel, fallback");
})().catch((error) => { console.error(error); process.exit(1); });
"""


def extract(source: str, marker: str) -> str:
    start = source.find(marker)
    if start < 0:
        raise SystemExit(f"ui/index.html 缺少 {marker}")
    end = source.find("\n", start)
    return source[start:end if end > 0 else len(source)]


def main() -> int:
    html = UI.read_text(encoding="utf-8")
    body = "\n".join(extract(html, marker) for marker in FUNCTIONS)
    script = STUBS + body + BRIDGE + CHECKS
    with tempfile.TemporaryDirectory(prefix="sonar-export-") as tmp:
        path = Path(tmp) / "export_contract.js"
        path.write_text(script, encoding="utf-8")
        result = subprocess.run(["node", str(path)], text=True, encoding="utf-8", capture_output=True)
    if result.returncode != 0:
        sys.stderr.write(result.stderr or result.stdout)
        raise SystemExit("导出契约测试失败")
    print(result.stdout.strip() or "export contract ok")
    return 0


if __name__ == "__main__":
    sys.exit(main())
