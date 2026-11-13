package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"net/url"
	"regexp"
	"sort"
	"strings"
)

var errSnapshotNotCaptured = errors.New("SNAPSHOT_NOT_CAPTURED")

const issueSelect = `SELECT i.kee,i.rule_uuid,coalesce(r.plugin_name||':'||r.plugin_rule_key,''),coalesce(r.name,''),i.issue_type,i.severity,
coalesce(i.message,''),coalesce(i.component_uuid,''),coalesce(c.path,c.name,''),i.line,coalesce(i.effort,0),i.status,coalesce(i.resolution,''),
coalesce(i.assignee,''),coalesce(i.author_login,''),coalesce(i.tags,''),coalesce(r.security_standards,''),i.issue_creation_date,i.issue_update_date,
coalesce(i.issue_close_date,0),i.locations
FROM issues i JOIN project_branches pb ON pb.uuid=i.project_uuid
LEFT JOIN rules r ON r.uuid=i.rule_uuid LEFT JOIN components c ON c.uuid=i.component_uuid `

func scanIssue(scanner rowScanner) (Issue, error) {
	var item Issue
	var line, effort sql.NullInt64
	var tags, standards string
	var locations []byte
	err := scanner.Scan(&item.Key, &item.RuleUUID, &item.RuleKey, &item.RuleName, &item.Type, &item.Severity, &item.Message, &item.FileUUID, &item.FilePath, &line, &effort, &item.Status, &item.Resolution, &item.Assignee, &item.Author, &tags, &standards, &item.CreatedAt, &item.UpdatedAt, &item.ClosedAt, &locations)
	if err != nil {
		return item, err
	}
	if line.Valid {
		v := int(line.Int64)
		item.Line = &v
	}
	if effort.Valid {
		item.Effort = int(effort.Int64)
	}
	item.Tags = []string{tags}
	item.SecurityStandards = []string{standards}
	item.Directory = parentPath(item.FilePath)
	if len(locations) > 0 && json.Valid(locations) {
		item.Locations = append(json.RawMessage(nil), locations...)
	}
	localizeIssue(&item)
	return item, nil
}

func parentPath(path string) string {
	if i := strings.LastIndex(path, "/"); i >= 0 {
		return path[:i]
	}
	return ""
}

var allowedSeverities = map[string]bool{"BLOCKER": true, "CRITICAL": true, "MAJOR": true, "MINOR": true, "INFO": true}
var allowedStatuses = map[string]bool{"OPEN": true, "CONFIRMED": true, "REOPENED": true, "RESOLVED": true, "CLOSED": true, "TO_REVIEW": true, "REVIEWED": true, "ACCEPTED": true}

func issueWhere(query IssueQuery) (string, []any, error) {
	if !validSonarID(query.ProjectUUID) {
		return "", nil, errors.New("projectUuid 格式不正确")
	}
	where := ` WHERE pb.project_uuid=$1 `
	args := []any{query.ProjectUUID}
	if query.Scope != "all" {
		where += ` AND i.resolution IS NULL `
	}
	if len(query.Types) > 0 {
		for _, t := range query.Types {
			if t < 1 || t > 4 {
				return "", nil, errors.New("未知问题类型")
			}
		}
		args = append(args, query.Types)
		where += fmt.Sprintf(` AND i.issue_type=ANY($%d) `, len(args))
	}
	if len(query.Severities) > 0 {
		values := []string{}
		for _, v := range query.Severities {
			v = strings.ToUpper(v)
			if !allowedSeverities[v] {
				return "", nil, errors.New("未知严重度")
			}
			values = append(values, v)
		}
		args = append(args, values)
		where += fmt.Sprintf(` AND i.severity=ANY($%d) `, len(args))
	}
	if len(query.Statuses) > 0 {
		values := []string{}
		for _, v := range query.Statuses {
			v = strings.ToUpper(v)
			if !allowedStatuses[v] {
				return "", nil, errors.New("未知问题状态")
			}
			values = append(values, v)
		}
		args = append(args, values)
		where += fmt.Sprintf(` AND i.status=ANY($%d) `, len(args))
	}
	ruleUUIDs := append([]string{}, query.RuleUUIDs...)
	if query.RuleUUID != "" {
		ruleUUIDs = append(ruleUUIDs, query.RuleUUID)
	}
	seenRules := map[string]bool{}
	ruleValues := []string{}
	for _, ruleUUID := range ruleUUIDs {
		ruleUUID = strings.TrimSpace(ruleUUID)
		if ruleUUID == "" || seenRules[ruleUUID] {
			continue
		}
		if !validSonarID(ruleUUID) {
			return "", nil, errors.New("ruleUuid 格式不正确")
		}
		seenRules[ruleUUID] = true
		ruleValues = append(ruleValues, ruleUUID)
	}
	if len(ruleValues) > 500 {
		return "", nil, errors.New("规则筛选数量过多")
	}
	if len(ruleValues) > 0 {
		args = append(args, ruleValues)
		where += fmt.Sprintf(` AND i.rule_uuid=ANY($%d) `, len(args))
	}
	if query.FileUUID != "" {
		if !validSonarID(query.FileUUID) {
			return "", nil, errors.New("fileUuid 格式不正确")
		}
		args = append(args, query.FileUUID)
		where += fmt.Sprintf(` AND i.component_uuid=$%d `, len(args))
	}
	if dirs := selectedDirectories(query); len(dirs) > 0 {
		if len(dirs) > 100 {
			return "", nil, errors.New("目录筛选数量过多")
		}
		for _, dir := range dirs {
			if len(dir) > 600 {
				return "", nil, errors.New("目录筛选过长")
			}
		}
		args = append(args, dirs)
		where += fmt.Sprintf(` AND EXISTS (SELECT 1 FROM unnest($%d::text[]) AS d WHERE c.path=d OR c.path LIKE d||'/%%') `, len(args))
	}
	if query.Assignee != "" {
		if len(query.Assignee) > 160 {
			return "", nil, errors.New("负责人筛选过长")
		}
		args = append(args, query.Assignee)
		where += fmt.Sprintf(` AND i.assignee=$%d `, len(args))
	}
	if strings.TrimSpace(query.Search) != "" {
		if len(query.Search) > 240 {
			return "", nil, errors.New("搜索词过长")
		}
		args = append(args, "%"+escapeLike(strings.TrimSpace(query.Search))+"%")
		where += fmt.Sprintf(` AND (i.message ILIKE $%d ESCAPE '\' OR c.path ILIKE $%d ESCAPE '\' OR r.name ILIKE $%d ESCAPE '\' OR (r.plugin_name||':'||r.plugin_rule_key) ILIKE $%d ESCAPE '\') `, len(args), len(args), len(args), len(args))
	}
	return where, args, nil
}

func escapeLike(value string) string {
	replacer := strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)
	return replacer.Replace(value)
}

func issueOrder(sortBy string) (string, error) {
	switch sortBy {
	case "", "risk":
		return ` ORDER BY CASE i.severity WHEN 'BLOCKER' THEN 0 WHEN 'CRITICAL' THEN 1 WHEN 'MAJOR' THEN 2 WHEN 'MINOR' THEN 3 ELSE 4 END,CASE i.issue_type WHEN 3 THEN 0 WHEN 2 THEN 1 WHEN 4 THEN 2 ELSE 3 END,i.issue_creation_date DESC,i.kee `, nil
	case "newest":
		return ` ORDER BY i.issue_creation_date DESC,i.kee `, nil
	case "effort":
		return ` ORDER BY i.effort DESC NULLS LAST,i.kee `, nil
	case "file":
		return ` ORDER BY c.path,i.line NULLS FIRST,i.kee `, nil
	default:
		return "", errors.New("不支持的排序方式")
	}
}

func (p *plugin) issues(ctx context.Context, cfg Config, query IssueQuery) (IssueList, error) {
	if query.Limit <= 0 {
		query.Limit = 50
	}
	if query.Limit > 200 {
		query.Limit = 200
	}
	if query.Offset < 0 || query.Offset > 10000 {
		query.Offset = 0
	}
	result := IssueList{Limit: query.Limit, Offset: query.Offset, AnalysisUUID: query.AnalysisUUID, Items: []Issue{}}
	if query.AnalysisUUID != "" && query.AnalysisUUID != "latest" {
		if !validSonarID(query.AnalysisUUID) {
			return result, errors.New("analysisUuid 格式不正确")
		}
		analysis, err := p.analysis(ctx, cfg, query.ProjectUUID, query.AnalysisUUID)
		if err != nil {
			return result, err
		}
		latest, err := p.analysis(ctx, cfg, query.ProjectUUID, "latest")
		if err != nil {
			return result, err
		}
		if analysis.UUID != latest.UUID {
			snapshot, err := p.history.read(query.ProjectUUID, analysis.UUID)
			if err != nil {
				return result, errSnapshotNotCaptured
			}
			return filterSnapshot(snapshot, query), nil
		}
	}
	items, total, facets, err := p.liveIssues(ctx, cfg, query, true)
	if err != nil {
		return result, err
	}
	result.Items, result.Total, result.Facets, result.IssuesCaptured = items, total, facets, false
	if latest, err := p.analysis(ctx, cfg, query.ProjectUUID, "latest"); err == nil {
		result.AnalysisUUID = latest.UUID
		if snapshot, err := p.history.ensureProjectLatest(ctx, cfg, p.connector, query.ProjectUUID, latest); err == nil && snapshot != nil {
			result.IssuesCaptured = true
		}
	}
	return result, nil
}

func (p *plugin) liveIssues(ctx context.Context, cfg Config, query IssueQuery, withFacets bool) ([]Issue, int, FilterFacets, error) {
	db, err := p.connector.connection(ctx, cfg)
	if err != nil {
		return nil, 0, FilterFacets{}, err
	}
	where, args, err := issueWhere(query)
	if err != nil {
		return nil, 0, FilterFacets{}, err
	}
	var total int
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM issues i JOIN project_branches pb ON pb.uuid=i.project_uuid LEFT JOIN rules r ON r.uuid=i.rule_uuid LEFT JOIN components c ON c.uuid=i.component_uuid `+where, args...).Scan(&total); err != nil {
		return nil, 0, FilterFacets{}, errors.New("统计问题失败：" + safeDBError(err, cfg))
	}
	order, err := issueOrder(query.Sort)
	if err != nil {
		return nil, 0, FilterFacets{}, err
	}
	limit := query.Limit
	if limit <= 0 {
		limit = 50
	}
	args = append(args, limit, query.Offset)
	rows, err := db.QueryContext(ctx, issueSelect+where+order+fmt.Sprintf(` LIMIT $%d OFFSET $%d`, len(args)-1, len(args)), args...)
	if err != nil {
		return nil, 0, FilterFacets{}, errors.New("读取问题失败：" + safeDBError(err, cfg))
	}
	defer rows.Close()
	items := []Issue{}
	for rows.Next() {
		item, err := scanIssue(rows)
		if err != nil {
			return nil, 0, FilterFacets{}, err
		}
		items = append(items, item)
	}
	facets := FilterFacets{Rules: []GroupCount{}, Directories: []GroupCount{}, Assignees: []GroupCount{}}
	if withFacets {
		facets = p.issueFacets(ctx, db, query.ProjectUUID)
	}
	return items, total, facets, rows.Err()
}

func (p *plugin) issueFacets(ctx context.Context, db *sql.DB, projectUUID string) FilterFacets {
	facets := FilterFacets{Rules: []GroupCount{}, Directories: []GroupCount{}, Assignees: []GroupCount{}}
	queries := []struct {
		sql      string
		target   *[]GroupCount
		withHint bool
	}{
		{`SELECT r.uuid,r.name,coalesce(r.plugin_name||':'||r.plugin_rule_key,''),count(*) FROM issues i JOIN project_branches pb ON pb.uuid=i.project_uuid JOIN rules r ON r.uuid=i.rule_uuid WHERE pb.project_uuid=$1 AND i.resolution IS NULL GROUP BY r.uuid,r.name,r.plugin_name,r.plugin_rule_key ORDER BY count(*) DESC,r.name LIMIT 500`, &facets.Rules, true},
		{`SELECT coalesce(regexp_replace(c.path,'/[^/]*$',''),''),coalesce(regexp_replace(c.path,'/[^/]*$',''),''),count(*) FROM issues i JOIN project_branches pb ON pb.uuid=i.project_uuid JOIN components c ON c.uuid=i.component_uuid WHERE pb.project_uuid=$1 AND i.resolution IS NULL GROUP BY 1,2 ORDER BY count(*) DESC LIMIT 500`, &facets.Directories, false},
		{`SELECT i.assignee,coalesce(u.name,i.assignee),count(*) FROM issues i JOIN project_branches pb ON pb.uuid=i.project_uuid LEFT JOIN users u ON u.uuid=i.assignee WHERE pb.project_uuid=$1 AND i.resolution IS NULL AND i.assignee IS NOT NULL GROUP BY i.assignee,u.name ORDER BY count(*) DESC LIMIT 100`, &facets.Assignees, false},
	}
	for _, q := range queries {
		rows, err := db.QueryContext(ctx, q.sql, projectUUID)
		if err != nil {
			continue
		}
		for rows.Next() {
			var item GroupCount
			if q.withHint {
				if rows.Scan(&item.ID, &item.Label, &item.Hint, &item.Count) == nil {
					*q.target = append(*q.target, item)
				}
			} else if rows.Scan(&item.ID, &item.Label, &item.Count) == nil {
				*q.target = append(*q.target, item)
			}
		}
		rows.Close()
	}
	return facets
}

func filterSnapshot(snapshot *issueSnapshot, query IssueQuery) IssueList {
	result := IssueList{Limit: query.Limit, Offset: query.Offset, AnalysisUUID: snapshot.AnalysisUUID, IssuesCaptured: true, Items: []Issue{}, Facets: FilterFacets{Rules: []GroupCount{}, Directories: []GroupCount{}, Assignees: []GroupCount{}}}
	filtered := []Issue{}
	ruleCounts := map[string]*GroupCount{}
	dirCounts := map[string]*GroupCount{}
	assigneeCounts := map[string]*GroupCount{}
	ruleFilter := map[string]bool{}
	directoryFilter := selectedDirectories(query)
	for _, ruleUUID := range query.RuleUUIDs {
		if ruleUUID != "" {
			ruleFilter[ruleUUID] = true
		}
	}
	if query.RuleUUID != "" {
		ruleFilter[query.RuleUUID] = true
	}
	for _, item := range snapshot.Issues {
		if item.Resolution == "" {
			if ruleCounts[item.RuleUUID] == nil {
				ruleCounts[item.RuleUUID] = &GroupCount{ID: item.RuleUUID, Label: item.RuleName, Hint: item.RuleKey}
			}
			ruleCounts[item.RuleUUID].Count++
			if dirCounts[item.Directory] == nil {
				dirCounts[item.Directory] = &GroupCount{ID: item.Directory, Label: item.Directory}
			}
			dirCounts[item.Directory].Count++
			if item.Assignee != "" {
				if assigneeCounts[item.Assignee] == nil {
					assigneeCounts[item.Assignee] = &GroupCount{ID: item.Assignee, Label: item.Assignee}
				}
				assigneeCounts[item.Assignee].Count++
			}
		}
		if query.Scope != "all" && item.Resolution != "" {
			continue
		}
		if len(query.Types) > 0 && !containsInt(query.Types, item.Type) {
			continue
		}
		if len(query.Severities) > 0 && !containsStringFold(query.Severities, item.Severity) {
			continue
		}
		if len(query.Statuses) > 0 && !containsStringFold(query.Statuses, item.Status) {
			continue
		}
		if len(ruleFilter) > 0 && !ruleFilter[item.RuleUUID] {
			continue
		}
		if query.FileUUID != "" && item.FileUUID != query.FileUUID {
			continue
		}
		if len(directoryFilter) > 0 && !matchesDirectories(item, directoryFilter) {
			continue
		}
		if query.Assignee != "" && item.Assignee != query.Assignee {
			continue
		}
		if query.Search != "" {
			term := strings.ToLower(query.Search)
			haystack := strings.ToLower(item.Message + " " + item.FilePath + " " + item.RuleName + " " + item.RuleKey)
			if !strings.Contains(haystack, term) {
				continue
			}
		}
		filtered = append(filtered, item)
	}
	sort.SliceStable(filtered, func(i, j int) bool {
		switch query.Sort {
		case "newest":
			if filtered[i].CreatedAt != filtered[j].CreatedAt {
				return filtered[i].CreatedAt > filtered[j].CreatedAt
			}
		case "effort":
			if filtered[i].Effort != filtered[j].Effort {
				return filtered[i].Effort > filtered[j].Effort
			}
		case "file":
			if filtered[i].FilePath != filtered[j].FilePath {
				return filtered[i].FilePath < filtered[j].FilePath
			}
		default:
			a, b := severityRank(filtered[i].Severity), severityRank(filtered[j].Severity)
			if a != b {
				return a < b
			}
		}
		return filtered[i].Key < filtered[j].Key
	})
	result.Total = len(filtered)
	start := query.Offset
	if start > len(filtered) {
		start = len(filtered)
	}
	end := start + query.Limit
	if end > len(filtered) {
		end = len(filtered)
	}
	result.Items = filtered[start:end]
	result.Facets.Rules = groupCounts(ruleCounts)
	result.Facets.Directories = groupCounts(dirCounts)
	result.Facets.Assignees = groupCounts(assigneeCounts)
	return result
}

func selectedDirectories(query IssueQuery) []string {
	values := append([]string{}, query.Directories...)
	if query.Directory != "" {
		values = append(values, query.Directory)
	}
	seen := map[string]bool{}
	result := []string{}
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" || seen[value] {
			continue
		}
		seen[value] = true
		result = append(result, value)
	}
	return result
}

func matchesDirectories(item Issue, directories []string) bool {
	for _, directory := range directories {
		if item.Directory == directory || strings.HasPrefix(item.FilePath, directory+"/") {
			return true
		}
	}
	return false
}

func containsInt(values []int, want int) bool {
	for _, v := range values {
		if v == want {
			return true
		}
	}
	return false
}
func containsStringFold(values []string, want string) bool {
	for _, v := range values {
		if strings.EqualFold(v, want) {
			return true
		}
	}
	return false
}
func severityRank(value string) int {
	return map[string]int{"BLOCKER": 0, "CRITICAL": 1, "MAJOR": 2, "MINOR": 3, "INFO": 4}[value]
}
func groupCounts(groups map[string]*GroupCount) []GroupCount {
	out := []GroupCount{}
	for _, item := range groups {
		out = append(out, *item)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Count > out[j].Count })
	if len(out) > 500 {
		out = out[:500]
	}
	return out
}

func (p *plugin) issueDetail(ctx context.Context, cfg Config, projectUUID, analysisUUID, issueKey string) (IssueDetail, error) {
	var detail IssueDetail
	if !validSonarID(issueKey) {
		return detail, errors.New("issueKey 格式不正确")
	}
	var item Issue
	found := false
	if analysisUUID != "" && analysisUUID != "latest" {
		snapshot, err := p.history.read(projectUUID, analysisUUID)
		if err != nil {
			return detail, errSnapshotNotCaptured
		}
		for _, candidate := range snapshot.Issues {
			if candidate.Key == issueKey {
				item = candidate
				found = true
				break
			}
		}
	} else {
		query := IssueQuery{ProjectUUID: projectUUID, Scope: "all", Limit: 200, Offset: 0}
		where, args, err := issueWhere(query)
		if err != nil {
			return detail, err
		}
		args = append(args, issueKey)
		where += fmt.Sprintf(` AND i.kee=$%d `, len(args))
		db, err := p.connector.connection(ctx, cfg)
		if err != nil {
			return detail, err
		}
		item, err = scanIssue(db.QueryRowContext(ctx, issueSelect+where+` LIMIT 1`, args...))
		if err == nil {
			found = true
		} else if err != sql.ErrNoRows {
			return detail, err
		}
	}
	if !found {
		return detail, errors.New("问题不存在或已不在该扫描中")
	}
	detail.Issue = item
	db, err := p.connector.connection(ctx, cfg)
	if err != nil {
		return detail, err
	}
	sectionRows, err := db.QueryContext(ctx, `SELECT kee,content FROM rule_desc_sections WHERE rule_uuid=$1 ORDER BY kee`, item.RuleUUID)
	if err == nil {
		defer sectionRows.Close()
		for sectionRows.Next() {
			var section RuleSection
			if sectionRows.Scan(&section.Key, &section.Content) == nil {
				section.Content = plainRuleText(section.Content)
				detail.RuleSections = append(detail.RuleSections, section)
			}
		}
	}
	changeRows, err := db.QueryContext(ctx, `SELECT coalesce(user_login,''),issue_change_creation_date,coalesce(change_data,'') FROM issue_changes WHERE issue_key=$1 ORDER BY issue_change_creation_date DESC LIMIT 100`, issueKey)
	if err == nil {
		defer changeRows.Close()
		for changeRows.Next() {
			var change IssueChange
			if changeRows.Scan(&change.User, &change.ChangedAt, &change.Diff) == nil {
				detail.Changes = append(detail.Changes, change)
			}
		}
	}
	detail.Guidance = guidanceFor(item)
	showSource := analysisUUID == "" || analysisUUID == "latest"
	if !showSource {
		selected, selectedErr := p.analysis(ctx, cfg, projectUUID, analysisUUID)
		latest, latestErr := p.analysis(ctx, cfg, projectUUID, "latest")
		showSource = selectedErr == nil && latestErr == nil && selected.UUID == latest.UUID
	}
	if showSource {
		detail.Source = readSourceContext(ctx, db, item)
	}
	if cfg.SonarURL != "" {
		project, err := p.project(ctx, cfg, projectUUID)
		if err == nil {
			detail.SonarURL = cfg.SonarURL + "/project/issues?id=" + url.QueryEscape(project.Key) + "&issues=" + url.QueryEscape(issueKey) + "&open=" + url.QueryEscape(issueKey)
		}
	}
	return detail, nil
}

var htmlTagPattern = regexp.MustCompile(`<[^>]*>`)
var blockTagPattern = regexp.MustCompile(`(?i)</?(p|div|li|ul|ol|h[1-6]|br|pre|code|tr|table|section)[^>]*>`)

func plainRuleText(value string) string {
	value = blockTagPattern.ReplaceAllString(value, "\n")
	value = htmlTagPattern.ReplaceAllString(value, "")
	value = html.UnescapeString(value)
	lines := []string{}
	for _, line := range strings.Split(value, "\n") {
		line = strings.TrimSpace(line)
		if line != "" {
			lines = append(lines, line)
		}
	}
	return strings.Join(lines, "\n")
}

func readSourceContext(ctx context.Context, db *sql.DB, item Issue) *SourceContext {
	var revision string
	var compressed []byte
	if err := db.QueryRowContext(ctx, `SELECT coalesce(revision,''),binary_data FROM file_sources WHERE file_uuid=$1`, item.FileUUID).Scan(&revision, &compressed); err != nil {
		return &SourceContext{Note: "SonarQube 未提供该文件的源码上下文"}
	}
	decoded, err := decodeLZ4JavaBlock(compressed)
	if err != nil {
		return &SourceContext{Revision: revision, Note: "SonarQube 源码压缩块无法解码，已安全跳过"}
	}
	all, err := decodeSonarSourceData(decoded)
	if err != nil {
		return &SourceContext{Revision: revision, Note: "SonarQube 源码协议数据无法解析，已安全跳过"}
	}
	center := 1
	if item.Line != nil {
		center = *item.Line
	}
	start := center - 12
	if start < 1 {
		start = 1
	}
	end := center + 12
	if end > len(all) {
		end = len(all)
	}
	context := &SourceContext{Revision: revision, Lines: []SourceLine{}, Truncated: end < len(all) || start > 1}
	for number := start; number <= end; number++ {
		context.Lines = append(context.Lines, SourceLine{Number: number, Text: all[number-1], Current: item.Line != nil && number == *item.Line})
	}
	return context
}
