# -*- coding: utf-8 -*-
"""Real SonarQube read-only integration smoke test.

Required environment: SONAR_TEST_HOST, SONAR_TEST_USERNAME, SONAR_TEST_PASSWORD.
Optional: SONAR_TEST_PORT, SONAR_TEST_DATABASE, SONAR_TEST_SSLMODE,
SONAR_TEST_PROJECT_KEY, SONAR_TEST_URL.
No credential is written into the repository or printed by this script.
"""
from __future__ import annotations
import json, os, queue, subprocess, sys, tempfile, threading, time
from pathlib import Path

ROOT = Path(__file__).resolve().parents[1]

class RPC:
    def __init__(self, binary: Path, data_dir: Path):
        env = dict(os.environ); env["DBX_PLUGIN_DATA_DIR"] = str(data_dir)
        self.proc = subprocess.Popen([str(binary)], stdin=subprocess.PIPE, stdout=subprocess.PIPE,
                                     stderr=subprocess.PIPE, env=env, text=True, encoding="utf-8")
        self.lines = queue.Queue(); self.errors = queue.Queue()
        threading.Thread(target=self._read_stdout, daemon=True).start()
        threading.Thread(target=self._read_stderr, daemon=True).start()
        self.serial = 0
    def _read_stdout(self):
        for line in self.proc.stdout: self.lines.put(line)
    def _read_stderr(self):
        for line in self.proc.stderr: self.errors.put(line)
    def call(self, method, params=None, timeout=90, allow_error=False):
        self.serial += 1; rid = self.serial
        payload = {"jsonrpc":"2.0","id":rid,"method":method,"params":params or {}}
        self.proc.stdin.write(json.dumps(payload, ensure_ascii=False)+"\n"); self.proc.stdin.flush()
        deadline = time.time()+timeout
        while time.time()<deadline:
            try: line=self.lines.get(timeout=max(.1,deadline-time.time()))
            except queue.Empty: break
            data=json.loads(line)
            if data.get("id")!=rid: continue
            if "error" in data:
                if allow_error: return data["error"]
                raise RuntimeError(f"{method}: {data['error'].get('message')}")
            return data.get("result")
        raise TimeoutError(method)
    def close(self):
        try: self.proc.stdin.close()
        except Exception: pass
        try: self.proc.wait(timeout=15)
        except subprocess.TimeoutExpired: self.proc.kill()

def require(condition, message):
    if not condition: raise AssertionError(message)

def main():
    if len(sys.argv)!=2: raise SystemExit("usage: python scripts/integration_smoke.py <sidecar-binary>")
    required=["SONAR_TEST_HOST","SONAR_TEST_USERNAME","SONAR_TEST_PASSWORD"]
    if any(not os.getenv(k) for k in required): raise SystemExit("skip: set SONAR_TEST_HOST/USERNAME/PASSWORD")
    scratch=ROOT.parent/"_tmp"; scratch.mkdir(exist_ok=True)
    with tempfile.TemporaryDirectory(prefix="sonar-quality-smoke-",dir=scratch) as tmp:
        data=Path(tmp)
        config={"host":os.environ["SONAR_TEST_HOST"],"port":int(os.getenv("SONAR_TEST_PORT","5432")),
                "database":os.getenv("SONAR_TEST_DATABASE","sonar"),"username":os.environ["SONAR_TEST_USERNAME"],
                "password":os.environ["SONAR_TEST_PASSWORD"],"sslmode":os.getenv("SONAR_TEST_SSLMODE","prefer"),
                "sonarUrl":os.getenv("SONAR_TEST_URL",""),"reviewSuffixes":"-Review","refreshSeconds":60,"snapshotDays":90}
        (data/"config.json").write_text(json.dumps(config),encoding="utf-8")
        rpc=RPC(Path(sys.argv[1]).resolve(),data)
        try:
            init=rpc.call("plugin/initialize",{"host":{"protocolVersions":[1]}})
            health=rpc.call("config/test")
            require(health["ok"] and health["readOnly"],"read-only schema validation failed")
            projects=rpc.call("quality/projects")["projects"]
            require(projects,"no SonarQube projects")
            wanted=os.getenv("SONAR_TEST_PROJECT_KEY","")
            project=next((p for p in projects if p["key"]==wanted),projects[0])
            pid=project["uuid"]
            overview=rpc.call("quality/overview",{"projectUuid":pid,"analysisUuid":"latest"})
            require(overview.get("issues"),"latest issue summary missing")
            scans=rpc.call("quality/scans",{"projectUuid":pid,"limit":100,"offset":0})
            require(scans["items"],"analysis history missing")
            issues=rpc.call("quality/issues",{"query":{"projectUuid":pid,"analysisUuid":"latest","scope":"open","limit":25,"offset":0,"sort":"risk"}})
            require(issues["total"]==overview["issues"]["total"],f"issue total mismatch: {issues['total']} vs {overview['issues']['total']}")
            bugs=rpc.call("quality/issues",{"query":{"projectUuid":pid,"analysisUuid":"latest","types":[2],"scope":"open","limit":5,"offset":0}})
            require(bugs["total"]==overview["issues"]["bugs"],"Bug filter total mismatch")
            require(issues["items"],"no issue rows")
            source_ok=False; source_notes=[]
            for item in issues["items"][:25]:
                detail=rpc.call("quality/issue",{"projectUuid":pid,"analysisUuid":"latest","issueKey":item["key"]})
                require(detail["issue"]["key"]==item["key"],"issue detail identity mismatch")
                if detail.get("source") and detail["source"].get("lines"): source_ok=True; break
                if detail.get("source") and detail["source"].get("note"): source_notes.append(detail["source"]["note"])
            require(source_ok,"no UTF-8 LZ4Block source context decoded from first 25 issues: "+"; ".join(sorted(set(source_notes))))
            global_result=rpc.call("quality/global",{"includeReview":True})
            require(global_result["projects"],"global project matrix missing")
            if len(scans["items"])>1:
                old=scans["items"][-1]["uuid"]
                old_overview=rpc.call("quality/overview",{"projectUuid":pid,"analysisUuid":old})
                require(old_overview["analysis"]["uuid"]==old,"historical metrics mismatch")
                old_issues=rpc.call("quality/issues",{"query":{"projectUuid":pid,"analysisUuid":old,"scope":"open","limit":1}},allow_error=True)
                if "code" not in old_issues: require(old_issues.get("analysisUuid")==old,"historical snapshot identity mismatch")
                else: require(old_issues.get("code")==-32004,"historical snapshot must fail with explicit code")
            print(json.dumps({"ok":True,"plugin":init["plugin"],"project":project["key"],
                              "analyses":scans["total"],"openIssues":issues["total"],"bugs":bugs["total"],
                              "sourceDecoded":source_ok,"globalProjects":len(global_result["projects"])},ensure_ascii=False,indent=2))
        finally: rpc.close()

if __name__=="__main__": main()
