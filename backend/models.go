package main

import "encoding/json"

// Branch is a SonarQube project branch. CE installations commonly expose main only.
type Branch struct {
	UUID     string `json:"uuid"`
	Key      string `json:"key"`
	IsMain   bool   `json:"isMain"`
	BranchID string `json:"branchId,omitempty"`
}

type Project struct {
	UUID         string    `json:"uuid"`
	Key          string    `json:"key"`
	Name         string    `json:"name"`
	Kind         string    `json:"kind"`
	Branches     []Branch  `json:"branches"`
	LastAnalysis *Analysis `json:"lastAnalysis,omitempty"`
	OpenIssues   int       `json:"openIssues"`
}

type Analysis struct {
	UUID        string             `json:"uuid"`
	Version     string             `json:"version"`
	Revision    string             `json:"revision"`
	BuildString string             `json:"buildString"`
	AnalyzedAt  int64              `json:"analyzedAt"`
	Status      string             `json:"status"`
	DurationMS  *int64             `json:"durationMs,omitempty"`
	QualityGate string             `json:"qualityGate"`
	Metrics     map[string]float64 `json:"metrics"`
}

type IssueSummary struct {
	Total           int   `json:"total"`
	CodeSmells      int   `json:"codeSmells"`
	Bugs            int   `json:"bugs"`
	Vulnerabilities int   `json:"vulnerabilities"`
	Hotspots        int   `json:"hotspots"`
	Open            int   `json:"open"`
	Resolved        int   `json:"resolved"`
	EffortMinutes   int64 `json:"effortMinutes"`
}

type GroupCount struct {
	ID    string `json:"id"`
	Label string `json:"label"`
	Hint  string `json:"hint,omitempty"`
	Count int    `json:"count"`
}

type Overview struct {
	Project        Project            `json:"project"`
	Analysis       Analysis           `json:"analysis"`
	Previous       *Analysis          `json:"previous,omitempty"`
	Issues         *IssueSummary      `json:"issues,omitempty"`
	Severities     map[string]int     `json:"severities"`
	Metrics        map[string]float64 `json:"metrics"`
	Delta          map[string]float64 `json:"delta"`
	Trend          []Analysis         `json:"trend"`
	TopFiles       []GroupCount       `json:"topFiles"`
	TopRules       []GroupCount       `json:"topRules"`
	IssuesCaptured bool               `json:"issuesCaptured"`
	CapturedAt     string             `json:"capturedAt,omitempty"`
}

type Issue struct {
	Key               string          `json:"key"`
	RuleUUID          string          `json:"ruleUuid"`
	RuleKey           string          `json:"ruleKey"`
	RuleName          string          `json:"ruleName"`
	RuleNameZh        string          `json:"ruleNameZh"`
	Type              int             `json:"type"`
	TypeKey           string          `json:"typeKey"`
	TypeLabel         string          `json:"typeLabel"`
	Severity          string          `json:"severity"`
	SeverityLabel     string          `json:"severityLabel"`
	Message           string          `json:"message"`
	MessageZh         string          `json:"messageZh,omitempty"`
	FileUUID          string          `json:"fileUuid"`
	FilePath          string          `json:"filePath"`
	Directory         string          `json:"directory"`
	Line              *int            `json:"line,omitempty"`
	Effort            int             `json:"effort"`
	Status            string          `json:"status"`
	StatusLabel       string          `json:"statusLabel"`
	Resolution        string          `json:"resolution,omitempty"`
	Assignee          string          `json:"assignee,omitempty"`
	Author            string          `json:"author,omitempty"`
	Tags              []string        `json:"tags"`
	SecurityStandards []string        `json:"securityStandards"`
	CreatedAt         int64           `json:"createdAt"`
	UpdatedAt         int64           `json:"updatedAt"`
	ClosedAt          int64           `json:"closedAt,omitempty"`
	Locations         json.RawMessage `json:"locations,omitempty"`
}

type IssueQuery struct {
	ProjectUUID  string   `json:"projectUuid"`
	AnalysisUUID string   `json:"analysisUuid"`
	Types        []int    `json:"types"`
	Severities   []string `json:"severities"`
	Statuses     []string `json:"statuses"`
	RuleUUID     string   `json:"ruleUuid"`
	RuleUUIDs    []string `json:"ruleUuids"`
	Directory    string   `json:"directory"`
	Directories  []string `json:"directories"`
	FileUUID     string   `json:"fileUuid"`
	Assignee     string   `json:"assignee"`
	Search       string   `json:"search"`
	Sort         string   `json:"sort"`
	Scope        string   `json:"scope"`
	Limit        int      `json:"limit"`
	Offset       int      `json:"offset"`
}

type IssueList struct {
	Items          []Issue      `json:"items"`
	Total          int          `json:"total"`
	Limit          int          `json:"limit"`
	Offset         int          `json:"offset"`
	Facets         FilterFacets `json:"facets"`
	AnalysisUUID   string       `json:"analysisUuid"`
	IssuesCaptured bool         `json:"issuesCaptured"`
}

type IssueExport struct {
	Markdown   string   `json:"markdown"`
	Filename   string   `json:"filename"`
	Total      int      `json:"total"`
	Exported   int      `json:"exported"`
	Truncated  bool     `json:"truncated"`
	Severities []string `json:"severities"`
}

type FilterFacets struct {
	Rules       []GroupCount `json:"rules"`
	Directories []GroupCount `json:"directories"`
	Assignees   []GroupCount `json:"assignees"`
}

type RuleSection struct {
	Key     string `json:"key"`
	Content string `json:"content"`
}

type IssueChange struct {
	User      string `json:"user,omitempty"`
	ChangedAt int64  `json:"changedAt"`
	Diff      string `json:"diff"`
}

type SourceLine struct {
	Number  int    `json:"number"`
	Text    string `json:"text"`
	Current bool   `json:"current"`
}

type SourceContext struct {
	Revision  string       `json:"revision"`
	Lines     []SourceLine `json:"lines"`
	Truncated bool         `json:"truncated"`
	Note      string       `json:"note,omitempty"`
}

type IssueDetail struct {
	Issue        Issue          `json:"issue"`
	RuleSections []RuleSection  `json:"ruleSections"`
	Changes      []IssueChange  `json:"changes"`
	Source       *SourceContext `json:"source,omitempty"`
	SonarURL     string         `json:"sonarUrl,omitempty"`
	Guidance     []string       `json:"guidance"`
}

type issueSnapshot struct {
	SchemaVersion int     `json:"schemaVersion"`
	ProjectUUID   string  `json:"projectUuid"`
	AnalysisUUID  string  `json:"analysisUuid"`
	CapturedAt    string  `json:"capturedAt"`
	Issues        []Issue `json:"issues"`
}

type CompareResult struct {
	From            string  `json:"from"`
	To              string  `json:"to"`
	Added           int     `json:"added"`
	Persistent      int     `json:"persistent"`
	NoLongerPresent int     `json:"noLongerPresent"`
	AddedItems      []Issue `json:"addedItems"`
	RemovedItems    []Issue `json:"removedItems"`
	Note            string  `json:"note"`
}
