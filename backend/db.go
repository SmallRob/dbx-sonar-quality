package main

import (
	"context"
	"database/sql"
	"errors"
	"net/url"
	"strings"
	"sync"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"
)

type dbConnector struct {
	mu        sync.Mutex
	db        *sql.DB
	configKey string
}

func (c *dbConnector) connection(ctx context.Context, cfg Config) (*sql.DB, error) {
	key := cfg.dsn()
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.db != nil && c.configKey == key {
		return c.db, nil
	}
	if c.db != nil {
		_ = c.db.Close()
		c.db = nil
	}
	if err := validateConfig(cfg); err != nil {
		return nil, err
	}
	db, err := sql.Open("pgx", key)
	if err != nil {
		return nil, errors.New("无法初始化 PostgreSQL 驱动：" + safeDBError(err, cfg))
	}
	db.SetMaxOpenConns(4)
	db.SetMaxIdleConns(2)
	db.SetConnMaxLifetime(30 * time.Minute)
	pingCtx, cancel := context.WithTimeout(ctx, 6*time.Second)
	defer cancel()
	if err := db.PingContext(pingCtx); err != nil {
		_ = db.Close()
		return nil, errors.New("SonarQube 数据库连接失败，请检查网络、地址与凭据：" + safeDBError(err, cfg))
	}
	c.db, c.configKey = db, key
	return db, nil
}

func (c *dbConnector) close() {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.db != nil {
		_ = c.db.Close()
		c.db = nil
		c.configKey = ""
	}
}

func safeDBError(err error, cfg Config) string {
	if err == nil {
		return ""
	}
	message := err.Error()
	for _, secret := range []string{cfg.Password, url.QueryEscape(cfg.Password)} {
		if secret != "" {
			message = strings.ReplaceAll(message, secret, "[已隐藏]")
		}
	}
	if len(message) > 700 {
		message = message[:700] + "…"
	}
	return message
}

func queryContext() (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), 9*time.Second)
}

func testConnection(ctx context.Context, cfg Config) (map[string]any, error) {
	if err := validateConfig(cfg); err != nil {
		return nil, err
	}
	connector := &dbConnector{}
	defer connector.close()
	db, err := connector.connection(ctx, cfg)
	if err != nil {
		return nil, err
	}
	var database, user, readonly string
	if err := db.QueryRowContext(ctx, "SELECT current_database(), current_user, current_setting('default_transaction_read_only')").Scan(&database, &user, &readonly); err != nil {
		return nil, errors.New("连接验证失败：" + safeDBError(err, cfg))
	}
	required := map[string][]string{
		"projects":         {"uuid", "kee", "name", "qualifier"},
		"project_branches": {"uuid", "project_uuid", "kee"},
		"snapshots":        {"uuid", "component_uuid", "build_date"},
		"issues":           {"kee", "rule_uuid", "project_uuid", "component_uuid", "issue_type", "resolution"},
		"rules":            {"uuid", "plugin_rule_key", "plugin_name", "name"},
		"components":       {"uuid", "path", "name", "qualifier"},
		"project_measures": {"analysis_uuid", "metric_uuid"},
		"live_measures":    {"component_uuid", "metric_uuid"},
		"metrics":          {"uuid", "name"},
	}
	rows, err := db.QueryContext(ctx, `SELECT table_name,column_name FROM information_schema.columns WHERE table_schema='public' AND table_name=ANY($1)`, []string{"projects", "project_branches", "snapshots", "issues", "rules", "components", "project_measures", "live_measures", "metrics"})
	if err != nil {
		return nil, errors.New("无法读取 SonarQube schema：" + safeDBError(err, cfg))
	}
	defer rows.Close()
	found := map[string]map[string]bool{}
	for rows.Next() {
		var table, column string
		if err := rows.Scan(&table, &column); err != nil {
			return nil, err
		}
		if found[table] == nil {
			found[table] = map[string]bool{}
		}
		found[table][column] = true
	}
	missing := []string{}
	for table, columns := range required {
		for _, column := range columns {
			if !found[table][column] {
				missing = append(missing, table+"."+column)
			}
		}
	}
	compatible := len(missing) == 0 && readonly == "on"
	return map[string]any{
		"ok": compatible, "database": database, "user": user, "readOnly": readonly == "on",
		"schema": "SonarQube 9.9 compatible", "missing": missing,
		"message": map[bool]string{true: "连接与只读 schema 验证通过", false: "连接成功，但 schema 或只读设置不兼容"}[compatible],
	}, nil
}
