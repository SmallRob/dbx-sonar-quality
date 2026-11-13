package main

import (
	"fmt"
	"regexp"
	"strings"
)

var typeLabels = map[int]string{1: "代码异味", 2: "Bug", 3: "漏洞", 4: "安全热点"}
var typeKeys = map[int]string{1: "CODE_SMELL", 2: "BUG", 3: "VULNERABILITY", 4: "SECURITY_HOTSPOT"}
var severityLabels = map[string]string{
	"BLOCKER": "阻断", "CRITICAL": "严重", "MAJOR": "主要", "MINOR": "次要", "INFO": "提示",
}
var statusLabels = map[string]string{
	"OPEN": "未处理", "CONFIRMED": "已确认", "REOPENED": "已重开", "RESOLVED": "已解决",
	"CLOSED": "已关闭", "TO_REVIEW": "待复核", "REVIEWED": "已复核", "ACCEPTED": "已接受",
}

func localizeIssue(issue *Issue) {
	issue.TypeKey = typeKeys[issue.Type]
	issue.TypeLabel = typeLabels[issue.Type]
	if issue.TypeLabel == "" {
		issue.TypeLabel = "其他"
	}
	issue.SeverityLabel = severityLabels[issue.Severity]
	if issue.SeverityLabel == "" {
		issue.SeverityLabel = issue.Severity
	}
	issue.StatusLabel = statusLabels[issue.Status]
	if issue.StatusLabel == "" {
		issue.StatusLabel = issue.Status
	}
	issue.RuleNameZh = translateRuleName(issue.RuleKey, issue.RuleName)
	issue.MessageZh = translateMessage(issue.RuleKey, issue.Message)
	issue.Tags = splitList(issue.Tags)
	issue.SecurityStandards = splitList(issue.SecurityStandards)
}

func splitList(values []string) []string {
	out := []string{}
	seen := map[string]bool{}
	for _, value := range values {
		for _, item := range strings.Split(value, ",") {
			item = strings.Trim(strings.TrimSpace(item), "{}\"")
			if item != "" && !seen[item] {
				seen[item] = true
				out = append(out, item)
			}
		}
	}
	return out
}

var suffixRuleNames = map[string]string{
	":S1135": "跟踪并处理 TODO 待办标记",
	":S125":  "移除注释掉的代码",
	":S1186": "为空方法补充实现或说明",
	":S3358": "避免嵌套三元表达式",
	":S1444": "限制全局变量的直接赋值",
	":S7773": "优先使用 Number.isNaN",
	":S3776": "降低函数认知复杂度",
	":S4325": "移除不必要的类型断言",
	":S1192": "提取重复字面量为常量",
	":S1068": "移除未使用的成员",
	":S1481": "移除未使用的局部变量",
	":S1854": "移除无效赋值",
	":S108":  "补充空代码块或说明原因",
	":S3923": "移除重复的分支结构",
	":S2583": "修正恒真或恒假的条件",
	":S2589": "移除恒真的布尔表达式",
}

func translateRuleName(ruleKey, fallback string) string {
	for suffix, translated := range suffixRuleNames {
		if strings.HasSuffix(ruleKey, suffix) {
			return translated
		}
	}
	return fallback
}

func translateMessage(ruleKey, message string) string {
	lower := strings.ToLower(message)
	switch {
	case strings.Contains(lower, "define a constant instead of using this literal"):
		return "将重复字面量提取为常量，统一维护并降低修改遗漏风险。"
	case strings.Contains(lower, "complete the task associated to this todo"):
		return "确认该待办是否仍需处理：完成实现、创建整改任务，或移除过期注释。"
	case strings.Contains(lower, "remove this commented out code"):
		return "删除已注释代码；历史实现由版本控制系统保留。"
	case strings.Contains(lower, "add a nested comment explaining why this method is empty"):
		return "实现方法逻辑；若刻意为空，请添加注释说明原因。"
	case strings.Contains(lower, "extract this nested ternary operation"):
		return "拆分为 if/else 或提前返回，避免多层三元表达式降低可读性。"
	case strings.Contains(lower, "refactor this method to reduce its cognitive complexity"):
		return "通过提取函数、提前返回和减少嵌套降低认知复杂度。"
	case strings.Contains(lower, "use number.isnan"):
		return "使用 Number.isNaN 判断 NaN，避免全局 isNaN 的隐式类型转换。"
	case strings.Contains(lower, "remove this type assertion"):
		return "移除不改变表达式类型的断言，让类型意图更清晰。"
	}
	_ = ruleKey
	return ""
}

func guidanceFor(issue Issue) []string {
	if issue.MessageZh != "" {
		return []string{issue.MessageZh, "修改后重新扫描，并在本次与上次扫描对比中确认问题不再出现。"}
	}
	switch issue.Type {
	case 2:
		return []string{"先结合问题行与调用上下文复现或推演缺陷，再补充针对该路径的回归测试。", "优先处理阻断、严重以及本次新增的问题，避免只修改表面告警。"}
	case 3:
		return []string{"核对输入来源、数据流和安全边界，按规则说明修复根因，不要仅隐藏告警。", "补充攻击路径或边界条件测试，并确认修复没有引入新的安全热点。"}
	case 4:
		return []string{"安全热点需要人工复核，不等同于已确认漏洞；结合业务场景判断风险。", "复核后通过 SonarQube 官方界面或 API 记录结论，本插件不会直写数据库。"}
	default:
		return []string{"阅读规则说明与问题上下文，优先减少重复、嵌套和不必要的复杂度。", "保持改动聚焦并补充验证；英文原文是最终规则依据。"}
	}
}

func formatEffort(minutes int) string {
	if minutes < 60 {
		return fmt.Sprintf("%d 分钟", minutes)
	}
	if minutes < 480 {
		return fmt.Sprintf("%.1f 小时", float64(minutes)/60)
	}
	return fmt.Sprintf("%.1f 人日", float64(minutes)/480)
}

var sonarIDPattern = regexp.MustCompile(`^[A-Za-z0-9_.:-]{1,160}$`)

func validSonarID(value string) bool { return sonarIDPattern.MatchString(value) }
