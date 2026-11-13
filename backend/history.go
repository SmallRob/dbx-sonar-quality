package main

import (
	"compress/gzip"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	dbxpluginsdk "github.com/t8y2/dbx/plugins/sdk/go/dbx-plugin-sdk"
)

type historyStore struct{}

func snapshotPath(projectUUID, analysisUUID string) (string, error) {
	if !validSonarID(projectUUID) || !validSonarID(analysisUUID) {
		return "", errors.New("快照标识格式不正确")
	}
	root := dbxpluginsdk.DataDir()
	if root == "" {
		return "", errors.New("DBX 未提供插件数据目录")
	}
	return filepath.Join(root, "history", projectUUID, analysisUUID+".json.gz"), nil
}

func (h *historyStore) read(projectUUID, analysisUUID string) (*issueSnapshot, error) {
	path, err := snapshotPath(projectUUID, analysisUUID)
	if err != nil {
		return nil, err
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	reader, err := gzip.NewReader(file)
	if err != nil {
		return nil, err
	}
	defer reader.Close()
	var snapshot issueSnapshot
	if err := json.NewDecoder(reader).Decode(&snapshot); err != nil {
		return nil, err
	}
	if snapshot.SchemaVersion != 1 || snapshot.ProjectUUID != projectUUID || snapshot.AnalysisUUID != analysisUUID {
		return nil, errors.New("扫描快照格式或身份不兼容")
	}
	return &snapshot, nil
}

func (h *historyStore) ensureProjectLatest(ctx context.Context, cfg Config, connector *dbConnector, projectUUID string, analysis Analysis) (*issueSnapshot, error) {
	if existing, err := h.read(projectUUID, analysis.UUID); err == nil {
		return existing, nil
	}
	db, err := connector.connection(ctx, cfg)
	if err != nil {
		return nil, err
	}
	tx, err := db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true, Isolation: sql.LevelRepeatableRead})
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	var latestUUID string
	if err := tx.QueryRowContext(ctx, `SELECT uuid FROM snapshots WHERE component_uuid=$1 ORDER BY build_date DESC LIMIT 1`, projectUUID).Scan(&latestUUID); err != nil {
		return nil, err
	}
	if latestUUID != analysis.UUID {
		return nil, errors.New("扫描已更新，将在下次刷新时留存")
	}
	rows, err := tx.QueryContext(ctx, issueSelect+` WHERE pb.project_uuid=$1 ORDER BY i.kee LIMIT 200001`, projectUUID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []Issue{}
	for rows.Next() {
		item, err := scanIssue(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
		if len(items) > 200000 {
			return nil, errors.New("单项目问题超过 200000，已停止快照留存")
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	snapshot := &issueSnapshot{SchemaVersion: 1, ProjectUUID: projectUUID, AnalysisUUID: analysis.UUID, CapturedAt: time.Now().UTC().Format(time.RFC3339), Issues: items}
	if err := h.write(snapshot); err != nil {
		return nil, err
	}
	return snapshot, nil
}

func (h *historyStore) write(snapshot *issueSnapshot) error {
	path, err := snapshotPath(snapshot.ProjectUUID, snapshot.AnalysisUUID)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	tmp := path + ".tmp"
	file, err := os.OpenFile(tmp, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	writer := gzip.NewWriter(file)
	encodeErr := json.NewEncoder(writer).Encode(snapshot)
	closeErr := writer.Close()
	fileErr := file.Close()
	if encodeErr != nil {
		return encodeErr
	}
	if closeErr != nil {
		return closeErr
	}
	if fileErr != nil {
		return fileErr
	}
	return os.Rename(tmp, path)
}

func (h *historyStore) compare(projectUUID, fromUUID, toUUID string) (CompareResult, error) {
	result := CompareResult{From: fromUUID, To: toUUID, AddedItems: []Issue{}, RemovedItems: []Issue{}, Note: "“不再出现”可能来自修复、关闭、文件删除或扫描范围变化，需结合问题状态确认。"}
	if fromUUID == toUUID {
		return result, errors.New("请选择两个不同的扫描记录")
	}
	from, err := h.read(projectUUID, fromUUID)
	if err != nil {
		return result, errSnapshotNotCaptured
	}
	to, err := h.read(projectUUID, toUUID)
	if err != nil {
		return result, errSnapshotNotCaptured
	}
	fromMap := map[string]Issue{}
	toMap := map[string]Issue{}
	for _, item := range from.Issues {
		if item.Resolution == "" {
			fromMap[item.Key] = item
		}
	}
	for _, item := range to.Issues {
		if item.Resolution == "" {
			toMap[item.Key] = item
		}
	}
	for key, item := range toMap {
		if _, ok := fromMap[key]; ok {
			result.Persistent++
		} else {
			result.Added++
			if len(result.AddedItems) < 200 {
				result.AddedItems = append(result.AddedItems, item)
			}
		}
	}
	for key, item := range fromMap {
		if _, ok := toMap[key]; !ok {
			result.NoLongerPresent++
			if len(result.RemovedItems) < 200 {
				result.RemovedItems = append(result.RemovedItems, item)
			}
		}
	}
	sort.Slice(result.AddedItems, func(i, j int) bool { return result.AddedItems[i].CreatedAt > result.AddedItems[j].CreatedAt })
	sort.Slice(result.RemovedItems, func(i, j int) bool { return result.RemovedItems[i].Key < result.RemovedItems[j].Key })
	return result, nil
}

func (h *historyStore) prune(days int) (int, error) {
	if days < 1 {
		days = 90
	}
	root := filepath.Join(dbxpluginsdk.DataDir(), "history")
	if _, err := os.Stat(root); os.IsNotExist(err) {
		return 0, nil
	}
	cutoff := time.Now().AddDate(0, 0, -days)
	removed := 0
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if entry.Type()&os.ModeSymlink != 0 {
			if entry.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json.gz") {
			return nil
		}
		info, err := entry.Info()
		if err != nil || info.ModTime().After(cutoff) {
			return nil
		}
		relative, err := filepath.Rel(root, path)
		if err != nil || strings.HasPrefix(relative, "..") {
			return nil
		}
		if err := os.Remove(path); err == nil {
			removed++
		}
		return nil
	})
	return removed, err
}

func summarizeIssues(items []Issue) (IssueSummary, map[string]int, []GroupCount, []GroupCount) {
	summary := IssueSummary{}
	severities := map[string]int{}
	files := map[string]*GroupCount{}
	rules := map[string]*GroupCount{}
	for _, item := range items {
		if item.Resolution != "" {
			if item.Status == "RESOLVED" || item.Status == "CLOSED" {
				summary.Resolved++
			}
			continue
		}
		summary.Total++
		summary.Open++
		summary.EffortMinutes += int64(item.Effort)
		severities[item.Severity]++
		switch item.Type {
		case 1:
			summary.CodeSmells++
		case 2:
			summary.Bugs++
		case 3:
			summary.Vulnerabilities++
		case 4:
			summary.Hotspots++
		}
		if files[item.FileUUID] == nil {
			files[item.FileUUID] = &GroupCount{ID: item.FileUUID, Label: item.FilePath}
		}
		files[item.FileUUID].Count++
		if rules[item.RuleUUID] == nil {
			rules[item.RuleUUID] = &GroupCount{ID: item.RuleUUID, Label: item.RuleNameZh}
		}
		rules[item.RuleUUID].Count++
	}
	topFiles := groupCounts(files)
	if len(topFiles) > 8 {
		topFiles = topFiles[:8]
	}
	topRules := groupCounts(rules)
	if len(topRules) > 8 {
		topRules = topRules[:8]
	}
	return summary, severities, topFiles, topRules
}
