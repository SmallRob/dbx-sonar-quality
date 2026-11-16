# -*- coding: utf-8 -*-
"""Validate the single-file Workbench UI without trusting the .html extension."""
from __future__ import annotations
import re, subprocess, sys, tempfile
from html.parser import HTMLParser
from pathlib import Path

ROOT = Path(__file__).resolve().parents[1]
UI = ROOT / "ui" / "index.html"

class ScriptExtractor(HTMLParser):
    def __init__(self):
        super().__init__(convert_charrefs=True)
        self.inside = False
        self.parts = []
    def handle_starttag(self, tag, attrs):
        if tag.lower() == "script":
            self.inside = True
    def handle_endtag(self, tag):
        if tag.lower() == "script":
            self.inside = False
    def handle_data(self, data):
        if self.inside:
            self.parts.append(data)

def main():
    html = UI.read_text(encoding="utf-8")
    if re.search(r'(?:src|href)\s*=\s*"https?://', html, re.IGNORECASE):
        raise SystemExit("external dependency found in UI src/href")
    parser = ScriptExtractor(); parser.feed(html)
    script = "\n".join(parser.parts)
    if not script.strip(): raise SystemExit("no inline JavaScript extracted")
    with tempfile.TemporaryDirectory(prefix="sonar-quality-ui-") as tmp:
        js = Path(tmp) / "index.js"; js.write_text(script, encoding="utf-8")
        result = subprocess.run(["node", "--check", str(js)], text=True, encoding="utf-8", capture_output=True)
        if result.returncode != 0:
            raise SystemExit(result.stderr or result.stdout)
    if re.search(r"onclick\s*=", html, re.IGNORECASE):
        raise SystemExit("inline onclick found; use bound listeners")
    if "localStorage" in script or "sessionStorage" in script:
        raise SystemExit("DBX 插件 UI 运行在 sandbox=allow-scripts iframe 中，不能使用 localStorage/sessionStorage")
    if re.search(r'type\s*=\s*"submit"|requestSubmit\s*\(', script, re.IGNORECASE):
        raise SystemExit("DBX 插件 UI 没有 allow-forms，不能使用原生表单提交")
    if re.search(r'type\s*=\s*"submit"', html, re.IGNORECASE):
        raise SystemExit("DBX 插件 UI 没有 allow-forms，按钮不能用 type=submit")
    if 'html[data-dbx-theme="dark"]' not in html:
        raise SystemExit("missing DBX dark theme tokens")
    if "#drawerContent{display:flex" not in html:
        raise SystemExit("detail drawer must be a flex column so its body can scroll")
    if "html.drawer-open" not in html or "overflow-y:auto" not in html:
        raise SystemExit("detail drawer must lock page scroll and scroll its own body")
    if ".drawer-head .icon-btn" not in html:
        raise SystemExit("detail drawer close button must keep a fixed 40x40 size")
    brutes = re.findall(r"<button\b[^>]*>", html, re.IGNORECASE)
    for tag in brutes:
        if not any(marker in tag for marker in ("data-action", "data-view", "issue-title", "id=", "type=\"button\"", "type=\"reset\"")):
            raise SystemExit(f"possible zombie button: {tag}")
    print(f"frontend syntax ok: {len(script)} JS chars, {len(brutes)} buttons")

if __name__ == "__main__": main()
