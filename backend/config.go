package main

import (
	"encoding/json"
	"errors"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"

	dbxpluginsdk "github.com/t8y2/dbx/plugins/sdk/go/dbx-plugin-sdk"
)

type Config struct {
	Host           string `json:"host"`
	Port           int    `json:"port"`
	Database       string `json:"database"`
	Username       string `json:"username"`
	Password       string `json:"password"`
	SSLMode        string `json:"sslmode"`
	SonarURL       string `json:"sonarUrl"`
	ReviewSuffixes string `json:"reviewSuffixes"`
	RefreshSeconds int    `json:"refreshSeconds"`
	SnapshotDays   int    `json:"snapshotDays"`
}

func defaultConfig() *Config {
	return &Config{Host: "127.0.0.1", Port: 5432, Database: "sonar", SSLMode: "prefer", RefreshSeconds: 60, SnapshotDays: 90}
}

var configMutex sync.Mutex

func configPath() string {
	if dir := dbxpluginsdk.DataDir(); dir != "" {
		return filepath.Join(dir, "config.json")
	}
	return ""
}

func loadConfig() *Config {
	configMutex.Lock()
	defer configMutex.Unlock()
	cfg := defaultConfig()
	path := configPath()
	if path == "" {
		return cfg
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return cfg
	}
	if json.Unmarshal(data, cfg) != nil {
		return defaultConfig()
	}
	normalizeConfig(cfg)
	return cfg
}

func saveConfig(cfg *Config) error {
	configMutex.Lock()
	defer configMutex.Unlock()
	path := configPath()
	if path == "" {
		return errors.New("DBX 未提供插件数据目录，无法保存配置")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func normalizeConfig(cfg *Config) {
	cfg.Host = strings.TrimSpace(cfg.Host)
	cfg.Database = strings.TrimSpace(cfg.Database)
	cfg.Username = strings.TrimSpace(cfg.Username)
	cfg.SSLMode = strings.ToLower(strings.TrimSpace(cfg.SSLMode))
	cfg.SonarURL = strings.TrimRight(strings.TrimSpace(cfg.SonarURL), "/")
	cfg.ReviewSuffixes = strings.TrimSpace(cfg.ReviewSuffixes)
	if cfg.Host == "" {
		cfg.Host = "127.0.0.1"
	}
	if cfg.Port == 0 {
		cfg.Port = 5432
	}
	if cfg.Database == "" {
		cfg.Database = "sonar"
	}
	if cfg.SSLMode == "" {
		cfg.SSLMode = "prefer"
	}
	if cfg.RefreshSeconds == 0 {
		cfg.RefreshSeconds = 60
	}
	if cfg.SnapshotDays == 0 {
		cfg.SnapshotDays = 90
	}
}

func validateConfig(cfg Config) error {
	normalizeConfig(&cfg)
	if cfg.Port < 1 || cfg.Port > 65535 {
		return errors.New("端口必须在 1–65535 之间")
	}
	if len(cfg.Host) > 255 || len(cfg.Database) > 128 || len(cfg.Username) > 128 {
		return errors.New("连接字段过长")
	}
	if strings.ContainsAny(cfg.Host+cfg.Database+cfg.Username, "\x00\r\n") {
		return errors.New("连接字段包含非法字符")
	}
	switch cfg.SSLMode {
	case "disable", "allow", "prefer", "require", "verify-ca", "verify-full":
	default:
		return errors.New("sslmode 仅支持 disable / allow / prefer / require / verify-ca / verify-full")
	}
	if cfg.Username == "" {
		return errors.New("请填写只读数据库用户")
	}
	if cfg.RefreshSeconds < 15 || cfg.RefreshSeconds > 3600 {
		return errors.New("刷新间隔必须在 15–3600 秒之间")
	}
	if cfg.SnapshotDays < 1 || cfg.SnapshotDays > 3650 {
		return errors.New("快照保留天数必须在 1–3650 之间")
	}
	if cfg.SonarURL != "" {
		u, err := url.Parse(cfg.SonarURL)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.User != nil {
			return errors.New("SonarQube 地址必须是有效的 http(s) URL，且不能包含用户信息")
		}
	}
	return nil
}

func (c *Config) publicConfig() map[string]any {
	return map[string]any{
		"host": c.Host, "port": c.Port, "database": c.Database, "username": c.Username,
		"sslmode": c.SSLMode, "sonarUrl": c.SonarURL, "reviewSuffixes": c.ReviewSuffixes,
		"refreshSeconds": c.RefreshSeconds, "snapshotDays": c.SnapshotDays,
		"hasPassword": c.Password != "", "dataDir": dbxpluginsdk.DataDir(),
	}
}

func (c *Config) dsn() string {
	u := &url.URL{Scheme: "postgres", Host: net.JoinHostPort(c.Host, strconv.Itoa(c.Port)), Path: "/" + c.Database}
	if c.Password == "" {
		u.User = url.User(c.Username)
	} else {
		u.User = url.UserPassword(c.Username, c.Password)
	}
	q := u.Query()
	q.Set("sslmode", c.SSLMode)
	q.Set("connect_timeout", "5")
	q.Set("statement_timeout", "8000")
	q.Set("default_transaction_read_only", "on")
	q.Set("application_name", "dbx-sonar-quality-readonly")
	u.RawQuery = q.Encode()
	return u.String()
}
