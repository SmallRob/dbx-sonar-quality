package main

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"
)

const maxExportIssues = 5000

var severityOrder = []string{"BLOCKER", "CRITICAL", "MAJOR", "MINOR", "INFO"}
var severityNames = map[string]string{"BLOCKER": "阻断", "CRITICAL": "严重", "MAJOR": "主要", "MINOR": "次要", "INFO": "提示"}

var exportLocation = time.FixedZone("CST", 8*3600)

// exportIssues 按当前筛选条件导出 Markdown。明细只使用问题表字段与本地修复建议，
// 不逐条读取规则正文和源码，避免 3000+ 条导出时产生数万次数据库查询。
func (p *plugin) exportIssues(ctx context.Context, cfg Config, query IssueQuery) (IssueExport, error) {
	if query.ProjectUUID == "" {
		return IssueExport{}, errors.New("缺少项目")
	}
	query.Limit = maxExportIssues
	query.Offset = 0
	fromSnapshot := false
	items := []Issue{}
	total := 0

	historical := query.AnalysisUUID != "" && query.AnalysisUUID != "latest"
	if historical {
		if !validSonarID(query.AnalysisUUID) {
			return IssueExport{}, errors.New("analysisUuid 格式不正确")
		}
		selected, err := p.analysis(ctx, cfg, query.ProjectUUID, query.AnalysisUUID)
		if err != nil {
			return IssueExport{}, err
		}
		latest, err := p.analysis(ctx, cfg, query.ProjectUUID, "latest")
		if err != nil {
			return IssueExport{}, err
		}
		if selected.UUID != latest.UUID {
			snapshot, err := p.history.read(query.ProjectUUID, query.AnalysisUUID)
			if err != nil {
				return IssueExport{}, errSnapshotNotCaptured
			}
			result := filterSnapshot(snapshot, query)
			items, total, fromSnapshot = result.Items, result.Total, true
		} else {
			historical = false
		}
	}
	if !historical {
		var err error
		items, total, _, err = p.liveIssues(ctx, cfg, query, false)
		if err != nil {
			return IssueExport{}, err
		}
		if latest, err := p.analysis(ctx, cfg, query.ProjectUUID, "latest"); err == nil {
			query.AnalysisUUID = latest.UUID
		}
	}
	if total == 0 || len(items) == 0 {
		return IssueExport{}, errors.New("没有符合导出条件的问题")
	}

	project, err := p.project(ctx, cfg, query.ProjectUUID)
	if err != nil {
		return IssueExport{}, err
	}
	analysis, err := p.analysis(ctx, cfg, query.ProjectUUID, query.AnalysisUUID)
	if err != nil {
		return IssueExport{}, err
	}
	now := time.Now().In(exportLocation)
	scanLabel := "最新扫描"
	if historical {
		scanLabel = "指定扫描"
	}
	markdown := buildIssueMarkdown(project, analysis, query, items, total, fromSnapshot, scanLabel, cfg.SonarURL, now)
	return IssueExport{
		Markdown:   markdown,
		Filename:   exportFilename(project, query, now),
		Total:      total,
		Exported:   len(items),
		Truncated:  total > len(items),
		Severities: exportSeverityKeys(query.Severities),
	}, nil
}

func exportSeverityKeys(values []string) []string {
	out := []string{}
	seen := map[string]bool{}
	for _, key := range severityOrder {
		if len(values) == 0 || containsStringFold(values, key) {
			if !seen[key] {
				out = append(out, key)
				seen[key] = true
			}
		}
	}
	return out
}

func exportFilename(project Project, query IssueQuery, now time.Time) string {
	slug := filenameSlug(project.Key)
	if slug == "" {
		slug = "sonarqube"
	}
	severity := "all-severity"
	if keys := exportSeverityKeys(query.Severities); len(keys) > 0 && len(query.Severities) > 0 {
		severity = strings.ToLower(strings.Join(keys, "-"))
	}
	return fmt.Sprintf("%s-issues-%s-%s.md", slug, severity, now.Format("20060102-1504"))
}

func filenameSlug(value string) string {
	value = strings.TrimSpace(value)
	var b strings.Builder
	for _, r := range value {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '-' || r == '_' || r == '.' {
			b.WriteRune(r)
		} else if r == ' ' || r == '/' || r == '\\' {
			b.WriteByte('-')
		}
	}
	result := strings.Trim(b.String(), "-._")
	if len(result) > 50 {
		result = result[:50]
	}
	return result
}

func buildIssueMarkdown(project Project, analysis Analysis, query IssueQuery, items []Issue, total int, fromSnapshot bool, scanLabel, sonarURL string, now time.Time) string {
	var b strings.Builder
	b.WriteString("# SonarQube 问题详情导出\n\n")
	b.WriteString("> 只读导出，不影响 SonarQube 数据。\n\n")
	b.WriteString("## 导出范围\n\n")
	b.WriteString("- 项目：" + mdText(project.Name) + "（" + mdCode(project.Key) + "）\n")
	if analysis.UUID != "" {
		label := scanLabel
		if label == "" {
			label = "最新扫描"
		}
		b.WriteString("- 扫描：" + label + " " + mdCode(analysis.UUID))
		if analysis.Version != "" {
			b.WriteString(" · 版本 " + mdText(analysis.Version))
		}
		if analysis.AnalyzedAt > 0 {
			b.WriteString(" · " + mdTime(analysis.AnalyzedAt, exportLocation))
		}
		b.WriteString("\n")
	}
	b.WriteString("- 严重度：" + mdText(exportSeverityText(query.Severities)) + "\n")
	b.WriteString("- 范围：" + mdText(exportScopeText(query)) + "\n")
	b.WriteString("- 数据来源：" + mdText(exportSourceText(fromSnapshot)) + "\n")
	b.WriteString("- 导出时间：" + now.Format("2006-01-02 15:04") + "\n")
	b.WriteString("- 记录数：" + fmt.Sprintf("%d", len(items)))
	if total > len(items) {
		b.WriteString(fmt.Sprintf(" / 共 %d 条（已达到单次导出上限 %d）", total, maxExportIssues))
	}
	b.WriteString("\n\n")

	groups := groupIssuesBySeverity(items)
	b.WriteString("## 严重度汇总\n\n")
	b.WriteString("| 严重度 | 条数 |\n| --- | ---: |\n")
	for _, key := range severityOrder {
		count := 0
		for _, group := range groups {
			if group.key == key {
				count = len(group.items)
				break
			}
		}
		if len(query.Severities) == 0 || containsStringFold(query.Severities, key) {
			b.WriteString("| " + severityNames[key] + "（" + key + "） | " + fmt.Sprintf("%d", count) + " |\n")
		}
	}
	for _, group := range groups {
		if group.key == "" {
			b.WriteString("| 其他 | " + fmt.Sprintf("%d", len(group.items)) + " |\n")
		}
	}
	b.WriteString("\n")

	for _, group := range groups {
		b.WriteString("## " + mdText(group.label) + " · " + fmt.Sprintf("%d", len(group.items)) + " 条\n\n")
		for index, item := range group.items {
			writeIssueMarkdown(&b, index+1, item, project, sonarURL)
		}
	}
	return b.String()
}

type issueGroup struct {
	key   string
	label string
	items []Issue
}

func groupIssuesBySeverity(items []Issue) []issueGroup {
	result := []issueGroup{}
	for _, key := range severityOrder {
		group := issueGroup{key: key, label: severityNames[key] + "（" + key + "）"}
		for _, item := range items {
			if strings.EqualFold(item.Severity, key) {
				group.items = append(group.items, item)
			}
		}
		if len(group.items) > 0 {
			result = append(result, group)
		}
	}
	other := issueGroup{key: "", label: "其他"}
	for _, item := range items {
		if !knownSeverity(item.Severity) {
			other.items = append(other.items, item)
		}
	}
	if len(other.items) > 0 {
		result = append(result, other)
	}
	return result
}

func writeIssueMarkdown(b *strings.Builder, index int, item Issue, project Project, sonarURL string) {
	title := item.RuleNameZh
	if title == "" {
		title = item.RuleName
	}
	if title == "" {
		title = item.RuleKey
	}
	b.WriteString(fmt.Sprintf("### %d. %s\n\n", index, mdText(title)))
	b.WriteString("- 问题键：" + mdCode(item.Key) + "\n")
	if item.RuleKey != "" {
		b.WriteString("- 规则键：" + mdCode(item.RuleKey) + "\n")
	}
	b.WriteString("- 类型 / 严重度 / 状态：" + mdText(typeLabel(item)) + " · " + mdText(severityLabel(item)) + " · " + mdText(statusLabel(item)) + "\n")
	b.WriteString("- 文件：" + mdCode(issueLocation(item)))
	b.WriteString("\n- 工作量：" + mdText(effortText(item.Effort)) + "\n")
	if item.CreatedAt > 0 {
		b.WriteString("- 发现时间：" + mdTime(item.CreatedAt, exportLocation) + "\n")
	}
	if len(item.Tags) > 0 {
		b.WriteString("- 标签：" + mdText(strings.Join(item.Tags, "、")) + "\n")
	}
	if len(item.SecurityStandards) > 0 {
		b.WriteString("- 安全标准：" + mdText(strings.Join(item.SecurityStandards, "、")) + "\n")
	}
	message := item.MessageZh
	if message == "" {
		message = item.Message
	}
	if message != "" {
		b.WriteString("- 问题描述：" + mdText(message) + "\n")
	}
	if guidance := guidanceFor(item); len(guidance) > 0 {
		b.WriteString("- 处理建议：\n")
		for _, line := range guidance {
			b.WriteString("  - " + mdText(line) + "\n")
		}
	}
	if sonarURL != "" && project.Key != "" {
		link := sonarURL + "/project/issues?id=" + url.QueryEscape(project.Key) + "&issues=" + url.QueryEscape(item.Key) + "&open=" + url.QueryEscape(item.Key)
		b.WriteString("- SonarQube：[" + mdText(item.Key) + "](" + link + ")\n")
	}
	b.WriteString("\n")
}

func exportSeverityText(values []string) string {
	if len(values) == 0 {
		return "全部严重度"
	}
	names := []string{}
	for _, key := range severityOrder {
		if containsStringFold(values, key) {
			names = append(names, severityNames[key]+"（"+key+"）")
		}
	}
	if len(names) == 0 {
		return strings.Join(values, "、")
	}
	return strings.Join(names, "、")
}

func exportScopeText(query IssueQuery) string {
	parts := []string{}
	if query.Scope != "all" {
		parts = append(parts, "仅未解决问题")
	} else {
		parts = append(parts, "全部问题")
	}
	if len(query.Types) > 0 {
		types := []string{}
		for _, item := range query.Types {
			types = append(types, issueTypeName(item))
		}
		parts = append(parts, "类型："+strings.Join(types, "、"))
	}
	if len(query.RuleUUIDs) > 0 || query.RuleUUID != "" {
		count := len(query.RuleUUIDs)
		if query.RuleUUID != "" {
			count++
		}
		parts = append(parts, fmt.Sprintf("已选 %d 条规则", count))
	}
	if query.Directory != "" {
		parts = append(parts, "目录："+query.Directory)
	}
	if query.Search != "" {
		parts = append(parts, "搜索："+query.Search)
	}
	return strings.Join(parts, "；")
}

func exportSourceText(fromSnapshot bool) string {
	if fromSnapshot {
		return "本地留存的问题快照"
	}
	return "SonarQube PostgreSQL 实时问题表"
}

func typeLabel(item Issue) string {
	if item.TypeLabel != "" {
		return item.TypeLabel
	}
	return issueTypeName(item.Type)
}

func issueTypeName(value int) string {
	if label := typeLabels[value]; label != "" {
		return label
	}
	return "其他"
}

func severityLabel(item Issue) string {
	if item.SeverityLabel != "" {
		if item.Severity != "" {
			return item.SeverityLabel + "（" + item.Severity + "）"
		}
		return item.SeverityLabel
	}
	return item.Severity
}

func statusLabel(item Issue) string {
	if item.StatusLabel != "" {
		if item.Status != "" {
			return item.StatusLabel + "（" + item.Status + "）"
		}
		return item.StatusLabel
	}
	return item.Status
}

func issueLocation(item Issue) string {
	if item.Line != nil {
		return fmt.Sprintf("%s:%d", item.FilePath, *item.Line)
	}
	return item.FilePath
}

func effortText(minutes int) string {
	if minutes <= 0 {
		return "—"
	}
	if minutes < 60 {
		return fmt.Sprintf("%d 分钟", minutes)
	}
	hours := minutes / 60
	mins := minutes % 60
	if mins == 0 {
		return fmt.Sprintf("%d 小时", hours)
	}
	return fmt.Sprintf("%d 小时 %d 分钟", hours, mins)
}

func mdTime(timestamp int64, loc *time.Location) string {
	if timestamp <= 0 {
		return "—"
	}
	if timestamp > 1e12 {
		timestamp /= 1000
	}
	return time.Unix(timestamp, 0).In(loc).Format("2006-01-02 15:04")
}

func mdCode(value string) string {
	value = strings.ReplaceAll(strings.TrimSpace(value), "`", "\\`")
	return "`" + value + "`"
}

func mdText(value string) string {
	value = strings.TrimSpace(value)
	value = strings.ReplaceAll(value, "\r\n", " ")
	value = strings.ReplaceAll(value, "\n", " ")
	value = strings.ReplaceAll(value, "\r", " ")
	value = strings.ReplaceAll(value, "|", "\\|")
	value = strings.ReplaceAll(value, "<", "&lt;")
	value = strings.ReplaceAll(value, ">", "&gt;")
	return value
}

func knownSeverity(value string) bool {
	for _, key := range severityOrder {
		if strings.EqualFold(value, key) {
			return true
		}
	}
	return false
}
