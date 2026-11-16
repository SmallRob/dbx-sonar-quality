package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"time"

	dbxpluginsdk "github.com/t8y2/dbx/plugins/sdk/go/dbx-plugin-sdk"
)

const pluginID = "io.github.smallrob.sonar-quality"

var pluginVersion = "0.1.9"

type plugin struct {
	connector *dbConnector
	history   *historyStore
}

func main() {
	p := &plugin{connector: &dbConnector{}, history: &historyStore{}}
	server := dbxpluginsdk.NewServer(dbxpluginsdk.Metadata{ID: pluginID, Version: pluginVersion, Capabilities: []string{}}, p)
	go p.snapshotWorker()
	if err := server.Serve(); err != nil {
		fmt.Fprintln(os.Stderr, "sidecar 退出:", err)
		os.Exit(1)
	}
}

func (p *plugin) snapshotWorker() {
	timer := time.NewTimer(10 * time.Second)
	defer timer.Stop()
	for range timer.C {
		func() {
			defer func() { _ = recover() }()
			cfg := loadConfig()
			if cfg.Username == "" {
				return
			}
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			defer cancel()
			_, _ = p.history.prune(cfg.SnapshotDays)
			projects, err := p.projects(ctx, *cfg)
			if err != nil {
				return
			}
			for _, project := range projects {
				if project.LastAnalysis != nil {
					_, _ = p.history.ensureProjectLatest(ctx, *cfg, p.connector, project.UUID, *project.LastAnalysis)
				}
			}
		}()
		timer.Reset(60 * time.Second)
	}
}

func (p *plugin) Handle(_ dbxpluginsdk.RequestContext, method string, params json.RawMessage, _ *dbxpluginsdk.Emitter) (any, *dbxpluginsdk.PluginError) {
	values := map[string]json.RawMessage{}
	if len(params) > 0 {
		if err := json.Unmarshal(params, &values); err != nil {
			return nil, dbxpluginsdk.NewError(-32602, "参数 JSON 格式错误")
		}
	}
	decode := func(key string, target any) bool {
		raw, ok := values[key]
		if !ok {
			return true
		}
		return json.Unmarshal(raw, target) == nil
	}
	str := func(key string) string { var value string; _ = decode(key, &value); return value }
	integer := func(key string) int { var value int; _ = decode(key, &value); return value }
	boolean := func(key string) bool { var value bool; _ = decode(key, &value); return value }

	switch method {
	case "ping", "dbx-sonar-quality/ping":
		return map[string]any{"ok": true, "version": pluginVersion, "readOnly": true, "schema": "SonarQube 9.9 compatible"}, nil
	case "config/get", "quality/config/get":
		return loadConfig().publicConfig(), nil
	case "config/save", "quality/config/save":
		var incoming Config
		if raw, ok := values["config"]; ok {
			if json.Unmarshal(raw, &incoming) != nil {
				return nil, dbxpluginsdk.NewError(-32602, "config 格式错误")
			}
		} else {
			incoming = Config{Host: str("host"), Port: integer("port"), Database: str("database"), Username: str("username"), Password: str("password"), SSLMode: str("sslmode"), SonarURL: str("sonarUrl"), ReviewSuffixes: str("reviewSuffixes"), RefreshSeconds: integer("refreshSeconds"), SnapshotDays: integer("snapshotDays")}
		}
		current := loadConfig()
		if incoming.Password == "" {
			incoming.Password = current.Password
		}
		normalizeConfig(&incoming)
		if err := validateConfig(incoming); err != nil {
			return nil, dbxpluginsdk.NewError(-32602, err.Error())
		}
		if err := saveConfig(&incoming); err != nil {
			return nil, dbxpluginsdk.NewError(-32000, "保存配置失败："+err.Error())
		}
		p.connector.close()
		return incoming.publicConfig(), nil
	case "config/test", "quality/config/test", "quality/health":
		cfg := loadConfig()
		if _, ok := values["config"]; ok {
			var incoming Config
			if !decode("config", &incoming) {
				return nil, dbxpluginsdk.NewError(-32602, "config 格式错误")
			}
			if incoming.Password == "" {
				incoming.Password = cfg.Password
			}
			cfg = &incoming
		}
		normalizeConfig(cfg)
		ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
		defer cancel()
		result, err := testConnection(ctx, *cfg)
		if err != nil {
			return nil, qualityError(err)
		}
		return result, nil
	case "quality/projects":
		ctx, cancel := queryContext()
		defer cancel()
		projects, err := p.projects(ctx, *loadConfig())
		if err != nil {
			return nil, qualityError(err)
		}
		return map[string]any{"projects": projects}, nil
	case "quality/scans":
		ctx, cancel := queryContext()
		defer cancel()
		items, total, err := p.scans(ctx, *loadConfig(), str("projectUuid"), integer("limit"), integer("offset"))
		if err != nil {
			return nil, qualityError(err)
		}
		return map[string]any{"items": items, "total": total}, nil
	case "quality/overview":
		ctx, cancel := queryContext()
		defer cancel()
		result, err := p.overview(ctx, *loadConfig(), str("projectUuid"), str("analysisUuid"))
		if err != nil {
			return nil, qualityError(err)
		}
		return result, nil
	case "quality/global":
		ctx, cancel := queryContext()
		defer cancel()
		result, err := p.global(ctx, *loadConfig(), boolean("includeReview"))
		if err != nil {
			return nil, qualityError(err)
		}
		return result, nil
	case "quality/issues":
		var query IssueQuery
		if raw, ok := values["query"]; ok {
			if json.Unmarshal(raw, &query) != nil {
				return nil, dbxpluginsdk.NewError(-32602, "query 格式错误")
			}
		} else {
			query = IssueQuery{ProjectUUID: str("projectUuid"), AnalysisUUID: str("analysisUuid"), RuleUUID: str("ruleUuid"), Directory: str("directory"), FileUUID: str("fileUuid"), Assignee: str("assignee"), Search: str("search"), Sort: str("sort"), Scope: str("scope"), Limit: integer("limit"), Offset: integer("offset"), Types: []int{}, Severities: []string{}, Statuses: []string{}, RuleUUIDs: []string{}, Directories: []string{}}
			_ = decode("types", &query.Types)
			_ = decode("severities", &query.Severities)
			_ = decode("statuses", &query.Statuses)
			_ = decode("ruleUuids", &query.RuleUUIDs)
			_ = decode("directories", &query.Directories)
		}
		ctx, cancel := queryContext()
		defer cancel()
		result, err := p.issues(ctx, *loadConfig(), query)
		if err != nil {
			return nil, qualityError(err)
		}
		return result, nil
	case "quality/export":
		var query IssueQuery
		if raw, ok := values["query"]; ok {
			if json.Unmarshal(raw, &query) != nil {
				return nil, dbxpluginsdk.NewError(-32602, "query 格式错误")
			}
		} else {
			query = IssueQuery{ProjectUUID: str("projectUuid"), AnalysisUUID: str("analysisUuid"), RuleUUID: str("ruleUuid"), Directory: str("directory"), FileUUID: str("fileUuid"), Assignee: str("assignee"), Search: str("search"), Sort: str("sort"), Scope: str("scope"), Limit: integer("limit"), Offset: integer("offset"), Types: []int{}, Severities: []string{}, Statuses: []string{}, RuleUUIDs: []string{}, Directories: []string{}}
			_ = decode("types", &query.Types)
			_ = decode("severities", &query.Severities)
			_ = decode("statuses", &query.Statuses)
			_ = decode("ruleUuids", &query.RuleUUIDs)
			_ = decode("directories", &query.Directories)
		}
		ctx, cancel := queryContext()
		defer cancel()
		result, err := p.exportIssues(ctx, *loadConfig(), query)
		if err != nil {
			return nil, qualityError(err)
		}
		return result, nil
	case "quality/issue":
		ctx, cancel := queryContext()
		defer cancel()
		result, err := p.issueDetail(ctx, *loadConfig(), str("projectUuid"), str("analysisUuid"), str("issueKey"))
		if err != nil {
			return nil, qualityError(err)
		}
		return result, nil
	case "quality/compare":
		result, err := p.history.compare(str("projectUuid"), str("from"), str("to"))
		if err != nil {
			return nil, qualityError(err)
		}
		return result, nil
	}
	return nil, dbxpluginsdk.MethodNotFound(method)
}

func qualityError(err error) *dbxpluginsdk.PluginError {
	if errors.Is(err, errSnapshotNotCaptured) {
		return &dbxpluginsdk.PluginError{Code: -32004, Message: "该历史扫描未留存问题快照；历史指标仍可查看，当前问题不会被冒充为历史结果。", Data: map[string]any{"code": "SNAPSHOT_NOT_CAPTURED"}}
	}
	return dbxpluginsdk.NewError(-32000, err.Error())
}
