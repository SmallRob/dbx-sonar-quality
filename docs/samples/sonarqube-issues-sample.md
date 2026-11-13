# SonarQube 问题详情导出

> 只读导出，不影响 SonarQube 数据。

## 导出范围

- 项目：示例项目（`demo-project`）
- 扫描：最新扫描 `demo-analysis` · 版本 demo_20261113
- 严重度：严重（CRITICAL）、主要（MAJOR）
- 范围：仅未解决问题
- 数据来源：SonarQube PostgreSQL 实时问题表
- 导出时间：2026-11-13 18:40
- 记录数：2

## 严重度汇总

| 严重度 | 条数 |
| --- | ---: |
| 严重（CRITICAL） | 1 |
| 主要（MAJOR） | 1 |

## 严重（CRITICAL） · 1 条

### 1. Resources should be closed

- 问题键：`demo-issue-1`
- 规则键：`java:S2095`
- 类型 / 严重度 / 状态：Bug · 严重（CRITICAL） · 未处理（OPEN）
- 文件：`src/main/java/example/StorageService.java:130`
- 工作量：5 分钟
- 发现时间：2026-11-11 11:50
- 标签：cert、cwe、leak
- 安全标准：cwe:459、cwe:772
- 问题描述：Use try-with-resources or close this "Stream" in a "finally" clause.
- 处理建议：
  - 先结合问题行与调用上下文复现或推演缺陷，再补充针对该路径的回归测试。
  - 优先处理阻断、严重以及本次新增的问题，避免只修改表面告警。

## 主要（MAJOR） · 1 条

### 1. Cognitive Complexity of methods should not be too high

- 问题键：`demo-issue-2`
- 规则键：`java:S3776`
- 类型 / 严重度 / 状态：代码异味 · 主要（MAJOR） · 未处理（OPEN）
- 文件：`src/main/java/example/OrderService.java:88`
- 工作量：15 分钟
- 发现时间：2026-11-10 09:20
- 标签：brain-overload
- 问题描述：Refactor this method to reduce its Cognitive Complexity from 21 to the 15 allowed.
- 处理建议：
  - 阅读规则说明与问题上下文，优先减少重复、嵌套和不必要的复杂度。
  - 保持改动聚焦并补充验证；英文原文是最终规则依据。
