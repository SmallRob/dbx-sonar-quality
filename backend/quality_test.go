package main

import (
	"encoding/binary"
	"strings"
	"testing"
	"time"

	"github.com/OneOfOne/xxhash"
	"github.com/pierrec/lz4/v4"
)

func TestIssueWhereUsesReadOnlyParameters(t *testing.T) {
	where, args, err := issueWhere(IssueQuery{ProjectUUID: "project-1", Search: "%_'; DROP TABLE issues;--"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(where, "resolution IS NULL") {
		t.Fatal("默认查询必须只包含未解决问题")
	}
	if strings.Contains(where, "DROP TABLE") || strings.Contains(where, "project-1") {
		t.Fatal("用户输入不能拼入 SQL")
	}
	if len(args) != 2 {
		t.Fatalf("参数数量错误: %d", len(args))
	}
	if !strings.Contains(args[1].(string), `\%\_`) {
		t.Fatalf("LIKE 通配符未转义: %q", args[1])
	}
}

func TestIssueWhereRejectsInvalidValues(t *testing.T) {
	for _, query := range []IssueQuery{{ProjectUUID: "../bad"}, {ProjectUUID: "p", Severities: []string{"URGENT"}}, {ProjectUUID: "p", Types: []int{99}}, {ProjectUUID: "p", Sort: "unknown"}, {ProjectUUID: "p", RuleUUIDs: []string{"../bad"}}} {
		if query.Sort == "unknown" {
			if _, err := issueOrder(query.Sort); err == nil {
				t.Fatal("应拒绝未知排序")
			}
			continue
		}
		if _, _, err := issueWhere(query); err == nil {
			t.Fatalf("应拒绝输入: %+v", query)
		}
	}
}

func TestIssueWhereSupportsMultipleRules(t *testing.T) {
	where, args, err := issueWhere(IssueQuery{ProjectUUID: "project-1", RuleUUIDs: []string{"rule-1", "rule-2", "rule-1"}})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(where, "i.rule_uuid=ANY(") {
		t.Fatalf("规则多选必须使用数组条件: %s", where)
	}
	if len(args) != 2 {
		t.Fatalf("参数数量错误: %d", len(args))
	}
	rules, ok := args[1].([]string)
	if !ok || len(rules) != 2 || rules[0] != "rule-1" || rules[1] != "rule-2" {
		t.Fatalf("规则去重错误: %#v", args[1])
	}
}

func TestExportMarkdownGroupsDetailsAndFilters(t *testing.T) {
	line := 42
	project := Project{Key: "asdm-app", Name: "示例项目"}
	analysis := Analysis{UUID: "analysis-1", Version: "1.2.0", AnalyzedAt: 1763000000}
	query := IssueQuery{ProjectUUID: "project-1", AnalysisUUID: "analysis-1", Scope: "open", Severities: []string{"CRITICAL", "MAJOR"}, RuleUUIDs: []string{"r1", "r2"}, Search: "password"}
	items := []Issue{
		{Key: "issue-1", RuleUUID: "r1", RuleKey: "java:S1192", RuleName: "String literals should not be duplicated", RuleNameZh: "重复字符串", Type: 1, TypeLabel: "代码异味", Severity: "CRITICAL", SeverityLabel: "严重", Status: "OPEN", StatusLabel: "未处理", MessageZh: "发现重复字符串", FilePath: "src/main.go", Line: &line, Effort: 35, Tags: []string{"design", "strings"}, CreatedAt: 1763000000},
		{Key: "issue-2", RuleUUID: "r2", RuleKey: "java:S3776", RuleName: "Cognitive Complexity", RuleNameZh: "认知复杂度", Type: 1, TypeLabel: "代码异味", Severity: "MAJOR", SeverityLabel: "主要", Status: "OPEN", StatusLabel: "未处理", Message: "Refactor this method", FilePath: "src/service.go", Effort: 120},
	}
	markdown := buildIssueMarkdown(project, analysis, query, items, 3, true, "指定扫描", "http://sonar.example", time.Date(2026, 11, 13, 12, 0, 0, 0, exportLocation))
	for _, want := range []string{
		"# SonarQube 问题详情导出",
		"项目：示例项目（`asdm-app`）",
		"扫描：指定扫描 `analysis-1` · 版本 1.2.0",
		"严重度：严重（CRITICAL）、主要（MAJOR）",
		"仅未解决问题；已选 2 条规则；搜索：password",
		"记录数：2 / 共 3 条（已达到单次导出上限 5000）",
		"## 严重（CRITICAL） · 1 条",
		"### 1. 重复字符串",
		"- 规则键：`java:S1192`",
		"- 文件：`src/main.go:42`",
		"- 工作量：35 分钟",
		"- 标签：design、strings",
		"## 主要（MAJOR） · 1 条",
		"- 工作量：2 小时",
		"http://sonar.example/project/issues?id=asdm-app",
	} {
		if !strings.Contains(markdown, want) {
			t.Fatalf("Markdown 缺少 %q:\n%s", want, markdown)
		}
	}
	if strings.Contains(markdown, "## 阻断") || strings.Contains(markdown, "## 提示") {
		t.Fatalf("未选严重度不应出现在导出中:\n%s", markdown)
	}
	if filename := exportFilename(project, IssueQuery{Severities: []string{"INFO", "BLOCKER"}}, time.Date(2026, 11, 13, 12, 0, 0, 0, exportLocation)); filename != "asdm-app-issues-blocker-info-20261113-1200.md" {
		t.Fatalf("文件名错误: %s", filename)
	}
	if mdText("a|b <x>") != "a\\|b &lt;x&gt;" {
		t.Fatalf("Markdown 转义错误")
	}
	if got := mdTime(1794369006913, exportLocation); got != "2026-11-11 11:50" {
		t.Fatalf("毫秒时间戳转换错误: %s", got)
	}
	if !strings.Contains(markdown, "数据来源：本地留存的问题快照") {
		t.Fatalf("快照来源标注错误")
	}
}

func TestLocalizeIssue(t *testing.T) {
	item := Issue{Type: 1, Severity: "CRITICAL", Status: "OPEN", RuleKey: "typescript:S125", RuleName: "Sections of code should not be commented out", Tags: []string{"{unused,dead-code}"}}
	localizeIssue(&item)
	if item.TypeLabel != "代码异味" || item.SeverityLabel != "严重" || item.RuleNameZh != "移除注释掉的代码" {
		t.Fatalf("中文映射错误: %+v", item)
	}
	if len(item.Tags) != 2 {
		t.Fatalf("标签解析错误: %v", item.Tags)
	}
}

func TestFilterSnapshotAndSummary(t *testing.T) {
	line := 12
	snapshot := &issueSnapshot{SchemaVersion: 1, ProjectUUID: "p", AnalysisUUID: "a", Issues: []Issue{
		{Key: "i1", Type: 2, Severity: "BLOCKER", Status: "OPEN", RuleUUID: "r1", RuleName: "Bug", FileUUID: "f1", FilePath: "src/a.ts", Directory: "src", Line: &line, CreatedAt: 20, Effort: 30},
		{Key: "i2", Type: 1, Severity: "MINOR", Status: "OPEN", RuleUUID: "r2", RuleName: "Smell", FileUUID: "f2", FilePath: "src/b.ts", Directory: "src", CreatedAt: 10, Effort: 10},
		{Key: "i3", Type: 1, Severity: "MAJOR", Status: "RESOLVED", Resolution: "FIXED", RuleUUID: "r2", FileUUID: "f2", FilePath: "src/b.ts"},
	}}
	result := filterSnapshot(snapshot, IssueQuery{Limit: 50, Severities: []string{"blocker"}})
	if result.Total != 1 || len(result.Items) != 1 || result.Items[0].Key != "i1" {
		t.Fatalf("快照筛选错误: %+v", result)
	}
	multiRule := filterSnapshot(snapshot, IssueQuery{Limit: 50, RuleUUIDs: []string{"r1", "r2"}})
	if multiRule.Total != 2 || len(multiRule.Items) != 2 {
		t.Fatalf("快照规则多选错误: %+v", multiRule)
	}
	summary, severities, files, rules := summarizeIssues(snapshot.Issues)
	if summary.Total != 2 || summary.Bugs != 1 || summary.CodeSmells != 1 || summary.Resolved != 1 || summary.EffortMinutes != 40 {
		t.Fatalf("汇总错误: %+v", summary)
	}
	if severities["BLOCKER"] != 1 || len(files) != 2 || len(rules) != 2 {
		t.Fatal("分组统计错误")
	}
}

func TestPublicConfigNeverReturnsPassword(t *testing.T) {
	cfg := Config{Username: "reader", Password: "secret"}
	public := cfg.publicConfig()
	if public["hasPassword"] != true {
		t.Fatal("应报告密码已配置")
	}
	for key, value := range public {
		if strings.Contains(strings.ToLower(key), "password") && key != "hasPassword" {
			t.Fatalf("泄露字段: %s", key)
		}
		if value == "secret" {
			t.Fatal("密码被返回")
		}
	}
}

func TestSnapshotPathRejectsTraversal(t *testing.T) {
	t.Setenv("DBX_PLUGIN_DATA_DIR", t.TempDir())
	if _, err := snapshotPath("../bad", "a"); err == nil {
		t.Fatal("必须拒绝目录穿越")
	}
	path, err := snapshotPath("project-1", "analysis-1")
	if err != nil || !strings.HasSuffix(path, "analysis-1.json.gz") {
		t.Fatalf("快照路径错误: %s %v", path, err)
	}
}

func rawLZ4Block(payload []byte) []byte {
	header := make([]byte, lz4JavaHeaderSize)
	copy(header, lz4JavaMagic)
	header[8] = 0x16
	binary.LittleEndian.PutUint32(header[9:13], uint32(len(payload)))
	binary.LittleEndian.PutUint32(header[13:17], uint32(len(payload)))
	binary.LittleEndian.PutUint32(header[17:21], xxhash.Checksum32S(payload, lz4ChecksumSeed))
	end := make([]byte, lz4JavaHeaderSize)
	copy(end, lz4JavaMagic)
	return append(append(header, payload...), end...)
}

func compressedLZ4Block(t *testing.T, payload []byte) []byte {
	compressed := make([]byte, lz4.CompressBlockBound(len(payload)))
	n, err := lz4.CompressBlock(payload, compressed, nil)
	if err != nil || n == 0 {
		t.Skip("测试文本不适合压缩")
	}
	compressed = compressed[:n]
	header := make([]byte, lz4JavaHeaderSize)
	copy(header, lz4JavaMagic)
	header[8] = 0x26
	binary.LittleEndian.PutUint32(header[9:13], uint32(n))
	binary.LittleEndian.PutUint32(header[13:17], uint32(len(payload)))
	binary.LittleEndian.PutUint32(header[17:21], xxhash.Checksum32S(payload, lz4ChecksumSeed))
	end := make([]byte, lz4JavaHeaderSize)
	copy(end, lz4JavaMagic)
	return append(append(header, compressed...), end...)
}

func TestDecodeSonarSourceProto(t *testing.T) {
	line := append(testVarint(8), testVarint(1)...)
	line = append(line, testVarint(18)...)
	line = append(line, testVarint(uint64(len("package demo")))...)
	line = append(line, "package demo"...)
	line = append(line, testVarint(40)...)
	line = append(line, testVarint(1700000000)...)
	wrapped := append(testVarint(10), testVarint(uint64(len(line)))...)
	wrapped = append(wrapped, line...)
	lines, err := decodeSonarSourceData(wrapped)
	if err != nil {
		t.Fatal(err)
	}
	if len(lines) != 1 || lines[0] != "package demo" {
		t.Fatalf("源码协议解析错误: %#v", lines)
	}
	if _, err := decodeSonarSourceData([]byte{0x0a, 0x02, 0x08}); err == nil {
		t.Fatal("损坏的源码协议必须报错")
	}
}

func testVarint(value uint64) []byte {
	var out []byte
	for value >= 0x80 {
		out = append(out, byte(value&0x7f)|0x80)
		value >>= 7
	}
	return append(out, byte(value))
}

func TestDecodeLZ4JavaBlock(t *testing.T) {
	text := []byte(strings.Repeat("package demo;\n// repeated source\n", 100))
	for name, data := range map[string][]byte{"raw": rawLZ4Block(text), "compressed": compressedLZ4Block(t, text)} {
		t.Run(name, func(t *testing.T) {
			decoded, err := decodeLZ4JavaBlock(data)
			if err != nil {
				t.Fatal(err)
			}
			if string(decoded) != string(text) {
				t.Fatal("解码内容不一致")
			}
		})
	}
	bad := rawLZ4Block(text)
	bad[len(bad)-1] = 1
	if _, err := decodeLZ4JavaBlock(bad); err == nil {
		t.Fatal("损坏数据必须报错")
	}
}

func TestHistoryCompare(t *testing.T) {
	t.Setenv("DBX_PLUGIN_DATA_DIR", t.TempDir())
	h := &historyStore{}
	from := &issueSnapshot{SchemaVersion: 1, ProjectUUID: "p", AnalysisUUID: "a", Issues: []Issue{{Key: "same"}, {Key: "old"}}}
	to := &issueSnapshot{SchemaVersion: 1, ProjectUUID: "p", AnalysisUUID: "b", Issues: []Issue{{Key: "same"}, {Key: "new"}}}
	if err := h.write(from); err != nil {
		t.Fatal(err)
	}
	if err := h.write(to); err != nil {
		t.Fatal(err)
	}
	result, err := h.compare("p", "a", "b")
	if err != nil {
		t.Fatal(err)
	}
	if result.Added != 1 || result.Persistent != 1 || result.NoLongerPresent != 1 {
		t.Fatalf("对比错误: %+v", result)
	}
}
