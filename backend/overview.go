package main

import "context"

func (p *plugin) overview(ctx context.Context, cfg Config, projectUUID, analysisUUID string) (Overview, error) {
	var result Overview
	project, err := p.project(ctx, cfg, projectUUID)
	if err != nil {
		return result, err
	}
	analysis, err := p.analysis(ctx, cfg, projectUUID, analysisUUID)
	if err != nil {
		return result, err
	}
	latest, err := p.analysis(ctx, cfg, projectUUID, "latest")
	if err != nil {
		return result, err
	}
	result.Project, result.Analysis, result.Metrics = project, analysis, analysis.Metrics
	result.Severities = map[string]int{}
	result.TopFiles = []GroupCount{}
	result.TopRules = []GroupCount{}
	result.Trend = []Analysis{}
	scans, _, err := p.scans(ctx, cfg, projectUUID, 2, 0)
	if analysis.UUID == latest.UUID && err == nil && len(scans) > 1 {
		result.Previous = &scans[1]
	} else {
		previous, _, scanErr := p.previousScan(ctx, cfg, projectUUID, analysis.AnalyzedAt)
		if scanErr == nil {
			result.Previous = previous
		}
	}
	if result.Previous != nil {
		result.Delta = deltaMetrics(analysis.Metrics, result.Previous.Metrics)
	} else {
		result.Delta = map[string]float64{}
	}
	trend, _, err := p.scans(ctx, cfg, projectUUID, 30, 0)
	if err == nil {
		result.Trend = trend
		for i, j := 0, len(trend)-1; i < j; i, j = i+1, j-1 {
			trend[i], trend[j] = trend[j], trend[i]
		}
	}
	var snapshot *issueSnapshot
	if analysis.UUID == latest.UUID {
		snapshot, _ = p.history.ensureProjectLatest(ctx, cfg, p.connector, projectUUID, analysis)
	} else {
		snapshot, _ = p.history.read(projectUUID, analysis.UUID)
	}
	if snapshot != nil {
		summary, severities, files, rules := summarizeIssues(snapshot.Issues)
		result.Issues = &summary
		result.Severities = severities
		result.TopFiles = files
		result.TopRules = rules
		result.IssuesCaptured = true
		result.CapturedAt = snapshot.CapturedAt
	} else if analysis.UUID == latest.UUID {
		summary, severities, files, rules, aggregateErr := p.liveSummary(ctx, cfg, projectUUID)
		if aggregateErr != nil {
			return result, aggregateErr
		}
		result.Issues = &summary
		result.Severities = severities
		result.TopFiles = files
		result.TopRules = rules
	}
	return result, nil
}

func (p *plugin) previousScan(ctx context.Context, cfg Config, projectUUID string, before int64) (*Analysis, int, error) {
	db, err := p.connector.connection(ctx, cfg)
	if err != nil {
		return nil, 0, err
	}
	a, err := scanAnalysis(db.QueryRowContext(ctx, `SELECT `+analysisColumns+` FROM snapshots s WHERE s.component_uuid=$1 AND s.build_date<$2 ORDER BY s.build_date DESC LIMIT 1`, projectUUID, before))
	if err != nil {
		return nil, 0, nil
	}
	metrics, gates, err := loadAnalysisMetrics(ctx, db, []string{a.UUID})
	if err != nil {
		return nil, 0, err
	}
	a.Metrics = metrics[a.UUID]
	if a.Metrics == nil {
		a.Metrics = map[string]float64{}
	}
	a.QualityGate = gates[a.UUID]
	return &a, 1, nil
}

func (p *plugin) liveSummary(ctx context.Context, cfg Config, projectUUID string) (IssueSummary, map[string]int, []GroupCount, []GroupCount, error) {
	query := IssueQuery{ProjectUUID: projectUUID, Scope: "all", Limit: 200000, Offset: 0, Sort: "file"}
	items, _, _, err := p.liveIssues(ctx, cfg, query, false)
	if err != nil {
		return IssueSummary{}, nil, nil, nil, err
	}
	summary, severities, files, rules := summarizeIssues(items)
	return summary, severities, files, rules, nil
}

func (p *plugin) global(ctx context.Context, cfg Config, includeReview bool) (map[string]any, error) {
	projects, err := p.projects(ctx, cfg)
	if err != nil {
		return nil, err
	}
	rows := []map[string]any{}
	totals := IssueSummary{}
	excluded := 0
	for _, project := range projects {
		if project.Kind == "review" && !includeReview {
			excluded++
			continue
		}
		summary := IssueSummary{}
		if project.LastAnalysis != nil {
			if snapshot, snapshotErr := p.history.ensureProjectLatest(ctx, cfg, p.connector, project.UUID, *project.LastAnalysis); snapshotErr == nil {
				summary, _, _, _ = summarizeIssues(snapshot.Issues)
			} else {
				live, _, _, _, liveErr := p.liveSummary(ctx, cfg, project.UUID)
				if liveErr == nil {
					summary = live
				}
			}
		}
		rows = append(rows, map[string]any{"project": project, "issues": summary, "qualityGate": qualityGateForProject(project), "coverage": metricForProject(project, "coverage"), "duplication": metricForProject(project, "duplicated_lines_density")})
		totals.Total += summary.Total
		totals.CodeSmells += summary.CodeSmells
		totals.Bugs += summary.Bugs
		totals.Vulnerabilities += summary.Vulnerabilities
		totals.Hotspots += summary.Hotspots
		totals.EffortMinutes += summary.EffortMinutes
	}
	return map[string]any{"projects": rows, "totals": totals, "excludedReviewProjects": excluded, "note": "全局合计按 SonarQube 项目求和，不跨项目去重；评审项目可单独排除。"}, nil
}

func qualityGateForProject(project Project) string {
	if project.LastAnalysis == nil {
		return ""
	}
	return project.LastAnalysis.QualityGate
}
func metricForProject(project Project, key string) float64 {
	if project.LastAnalysis == nil {
		return 0
	}
	return metric(project.LastAnalysis.Metrics, key)
}
