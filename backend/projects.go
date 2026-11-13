package main

import (
	"context"
	"database/sql"
	"errors"
	"sort"
	"strings"
)

type rowScanner interface{ Scan(dest ...any) error }

func scanAnalysis(scanner rowScanner) (Analysis, error) {
	var a Analysis
	var version, revision, buildString sql.NullString
	var duration sql.NullInt64
	if err := scanner.Scan(&a.UUID, &version, &revision, &buildString, &a.AnalyzedAt, &a.Status, &duration); err != nil {
		return a, err
	}
	a.Version, a.Revision, a.BuildString = version.String, revision.String, buildString.String
	if duration.Valid {
		a.DurationMS = &duration.Int64
	}
	a.Metrics = map[string]float64{}
	return a, nil
}

const analysisColumns = `s.uuid,s.version,s.revision,s.build_string,s.build_date,s.status,
(SELECT ce.executed_at-ce.started_at FROM ce_activity ce WHERE ce.analysis_uuid=s.uuid AND ce.executed_at IS NOT NULL ORDER BY ce.executed_at DESC LIMIT 1)`

func (p *plugin) projects(ctx context.Context, cfg Config) ([]Project, error) {
	db, err := p.connector.connection(ctx, cfg)
	if err != nil {
		return nil, err
	}
	rows, err := db.QueryContext(ctx, `SELECT p.uuid,p.kee,p.name,s.uuid,s.version,s.revision,s.build_string,s.build_date,s.status,
(SELECT ce.executed_at-ce.started_at FROM ce_activity ce WHERE ce.analysis_uuid=s.uuid AND ce.executed_at IS NOT NULL ORDER BY ce.executed_at DESC LIMIT 1)
FROM projects p
LEFT JOIN LATERAL (SELECT * FROM snapshots s WHERE s.component_uuid=p.uuid ORDER BY s.build_date DESC LIMIT 1) s ON true
WHERE p.qualifier='TRK' ORDER BY p.name,p.kee`)
	if err != nil {
		return nil, errors.New("读取项目失败：" + safeDBError(err, cfg))
	}
	defer rows.Close()
	projects := []Project{}
	latest := map[string]*Analysis{}
	for rows.Next() {
		var project Project
		var aUUID sql.NullString
		var version, revision, buildString sql.NullString
		var analyzedAt sql.NullInt64
		var status sql.NullString
		var duration sql.NullInt64
		if err := rows.Scan(&project.UUID, &project.Key, &project.Name, &aUUID, &version, &revision, &buildString, &analyzedAt, &status, &duration); err != nil {
			return nil, err
		}
		project.Kind = projectKind(project.Key, cfg.ReviewSuffixes)
		project.Branches = []Branch{}
		if aUUID.Valid {
			a := &Analysis{UUID: aUUID.String, Version: version.String, Revision: revision.String, BuildString: buildString.String, AnalyzedAt: analyzedAt.Int64, Status: status.String, Metrics: map[string]float64{}}
			if duration.Valid {
				a.DurationMS = &duration.Int64
			}
			project.LastAnalysis = a
			latest[project.UUID] = a
		}
		projects = append(projects, project)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(projects) == 0 {
		return projects, nil
	}
	branchRows, err := db.QueryContext(ctx, `SELECT project_uuid,uuid,kee,(branch_type='BRANCH' AND merge_branch_uuid IS NULL) AS is_main FROM project_branches WHERE project_uuid=ANY($1) ORDER BY project_uuid,(branch_type='BRANCH' AND merge_branch_uuid IS NULL) DESC,kee`, projectUUIDs(projects))
	if err != nil {
		return nil, errors.New("读取项目分支失败：" + safeDBError(err, cfg))
	}
	defer branchRows.Close()
	byID := map[string]*Project{}
	for i := range projects {
		byID[projects[i].UUID] = &projects[i]
	}
	for branchRows.Next() {
		var projectUUID string
		var branch Branch
		if err := branchRows.Scan(&projectUUID, &branch.UUID, &branch.Key, &branch.IsMain); err != nil {
			return nil, err
		}
		if project := byID[projectUUID]; project != nil {
			project.Branches = append(project.Branches, branch)
		}
	}
	countRows, err := db.QueryContext(ctx, `SELECT pb.project_uuid,count(*) FROM issues i JOIN project_branches pb ON pb.uuid=i.project_uuid WHERE pb.project_uuid=ANY($1) AND i.resolution IS NULL GROUP BY pb.project_uuid`, projectUUIDs(projects))
	if err == nil {
		defer countRows.Close()
		for countRows.Next() {
			var uuid string
			var count int
			if countRows.Scan(&uuid, &count) == nil && byID[uuid] != nil {
				byID[uuid].OpenIssues = count
			}
		}
	}
	if len(latest) > 0 {
		_ = p.fillLatestMetrics(ctx, db, latest)
	}
	return projects, nil
}

func projectUUIDs(projects []Project) []string {
	out := make([]string, 0, len(projects))
	for _, p := range projects {
		out = append(out, p.UUID)
	}
	return out
}

func projectKind(key, suffixes string) string {
	for _, suffix := range strings.Split(suffixes, ",") {
		suffix = strings.TrimSpace(suffix)
		if suffix != "" && strings.HasSuffix(strings.ToLower(key), strings.ToLower(suffix)) {
			return "review"
		}
	}
	return "standard"
}

func (p *plugin) fillLatestMetrics(ctx context.Context, db *sql.DB, analyses map[string]*Analysis) error {
	ids := make([]string, 0, len(analyses))
	for _, a := range analyses {
		ids = append(ids, a.UUID)
	}
	metrics, gates, err := loadAnalysisMetrics(ctx, db, ids)
	if err != nil {
		return err
	}
	for projectUUID, a := range analyses {
		a.Metrics = metrics[a.UUID]
		if a.Metrics == nil {
			a.Metrics = map[string]float64{}
		}
		a.QualityGate = gates[a.UUID]
		if len(a.Metrics) == 0 {
			rows, err := db.QueryContext(ctx, `SELECT m.name,lm.value FROM live_measures lm JOIN metrics m ON m.uuid=lm.metric_uuid JOIN project_branches pb ON pb.uuid=lm.component_uuid WHERE pb.project_uuid=$1 AND lm.value IS NOT NULL`, projectUUID)
			if err == nil {
				for rows.Next() {
					var name string
					var value float64
					if rows.Scan(&name, &value) == nil {
						a.Metrics[name] = value
					}
				}
				rows.Close()
			}
		}
	}
	return nil
}

func loadAnalysisMetrics(ctx context.Context, db *sql.DB, ids []string) (map[string]map[string]float64, map[string]string, error) {
	metrics := map[string]map[string]float64{}
	gates := map[string]string{}
	if len(ids) == 0 {
		return metrics, gates, nil
	}
	rows, err := db.QueryContext(ctx, `SELECT pm.analysis_uuid,m.name,pm.value,pm.text_value FROM project_measures pm JOIN metrics m ON m.uuid=pm.metric_uuid WHERE pm.analysis_uuid=ANY($1)`, ids)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var id, name string
		var value sql.NullFloat64
		var text sql.NullString
		if err := rows.Scan(&id, &name, &value, &text); err != nil {
			return nil, nil, err
		}
		if metrics[id] == nil {
			metrics[id] = map[string]float64{}
		}
		if value.Valid {
			metrics[id][name] = value.Float64
		}
		if name == "alert_status" && text.Valid {
			gates[id] = text.String
		}
	}
	return metrics, gates, rows.Err()
}

func (p *plugin) project(ctx context.Context, cfg Config, uuid string) (Project, error) {
	if !validSonarID(uuid) {
		return Project{}, errors.New("projectUuid 格式不正确")
	}
	projects, err := p.projects(ctx, cfg)
	if err != nil {
		return Project{}, err
	}
	for _, project := range projects {
		if project.UUID == uuid {
			return project, nil
		}
	}
	return Project{}, errors.New("项目不存在或当前账号不可见")
}

func (p *plugin) analysis(ctx context.Context, cfg Config, projectUUID, analysisUUID string) (Analysis, error) {
	db, err := p.connector.connection(ctx, cfg)
	if err != nil {
		return Analysis{}, err
	}
	query := `SELECT ` + analysisColumns + ` FROM snapshots s WHERE s.component_uuid=$1`
	args := []any{projectUUID}
	if analysisUUID != "" && analysisUUID != "latest" {
		if !validSonarID(analysisUUID) {
			return Analysis{}, errors.New("analysisUuid 格式不正确")
		}
		query += ` AND s.uuid=$2`
		args = append(args, analysisUUID)
	}
	query += ` ORDER BY s.build_date DESC LIMIT 1`
	a, err := scanAnalysis(db.QueryRowContext(ctx, query, args...))
	if err == sql.ErrNoRows {
		return Analysis{}, errors.New("该项目尚无扫描记录")
	}
	if err != nil {
		return Analysis{}, err
	}
	metrics, gates, err := loadAnalysisMetrics(ctx, db, []string{a.UUID})
	if err != nil {
		return Analysis{}, err
	}
	a.Metrics = metrics[a.UUID]
	if a.Metrics == nil {
		a.Metrics = map[string]float64{}
	}
	a.QualityGate = gates[a.UUID]
	return a, nil
}

func (p *plugin) scans(ctx context.Context, cfg Config, projectUUID string, limit, offset int) ([]Analysis, int, error) {
	if !validSonarID(projectUUID) {
		return nil, 0, errors.New("projectUuid 格式不正确")
	}
	if limit <= 0 || limit > 100 {
		limit = 30
	}
	if offset < 0 || offset > 10000 {
		offset = 0
	}
	db, err := p.connector.connection(ctx, cfg)
	if err != nil {
		return nil, 0, err
	}
	var total int
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM snapshots WHERE component_uuid=$1`, projectUUID).Scan(&total); err != nil {
		return nil, 0, err
	}
	rows, err := db.QueryContext(ctx, `SELECT `+analysisColumns+` FROM snapshots s WHERE s.component_uuid=$1 ORDER BY s.build_date DESC LIMIT $2 OFFSET $3`, projectUUID, limit, offset)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	items := []Analysis{}
	ids := []string{}
	for rows.Next() {
		a, err := scanAnalysis(rows)
		if err != nil {
			return nil, 0, err
		}
		items = append(items, a)
		ids = append(ids, a.UUID)
	}
	metrics, gates, err := loadAnalysisMetrics(ctx, db, ids)
	if err != nil {
		return nil, 0, err
	}
	for i := range items {
		items[i].Metrics = metrics[items[i].UUID]
		if items[i].Metrics == nil {
			items[i].Metrics = map[string]float64{}
		}
		items[i].QualityGate = gates[items[i].UUID]
	}
	return items, total, nil
}

func metric(metrics map[string]float64, key string) float64 { return metrics[key] }
func deltaMetrics(current, previous map[string]float64) map[string]float64 {
	keys := []string{"bugs", "vulnerabilities", "code_smells", "security_hotspots", "coverage", "duplicated_lines_density", "sqale_index", "ncloc"}
	out := map[string]float64{}
	for _, key := range keys {
		if previous == nil {
			continue
		}
		if _, ok := current[key]; ok {
			if old, exists := previous[key]; exists {
				out[key] = current[key] - old
			}
		}
	}
	return out
}

func sortProjectsByRisk(projects []Project) {
	sort.SliceStable(projects, func(i, j int) bool { return projects[i].OpenIssues > projects[j].OpenIssues })
}
