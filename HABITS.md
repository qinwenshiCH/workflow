# 用户习惯档案

你说"以后都…"时 AI 立即写入对应分类。每次会话自动加载此文件。

## 编码 & 技术

(空 — 等你说"以后都用…")

## 已安装第三方技能集

- [Matt Pocock /skills](https://github.com/mattpocock/skills)（2026-07-03）：38 个技能，包括 TDD、PRD 编写、代码审查、架构改进、调试等
- [Ponytail](https://github.com/DietrichGebert/ponytail)（2026-07-03）：6 个技能，AI 代码精简懒人模式（/ponytail, /ponytail-review, /ponytail-audit 等）

路径：`.agents/skills/`（Codex CLI / Claude Code 均可用）
可用命令参见 `npx skills@latest list`

## 流程 & 方法

- 确认(2026-06-26): simplify、review 等审查工作使用独立子上下文（sub-agent / background task）执行，当前会话不直接运行；待子任务完成后获取结果，在当前会话汇总呈现给用户
- 确认(2026-06-26): 本项目（workflow）的改动自动 commit；其他项目的改动等一批流程结束、人确认后再提交
- 确认(2026-06-26): Spec 阶段决策即时写入 decisions.md（详见 CLAUDE.md「决策记录纪律」）

## Spec & 设计

(空 — 等你说"以后都…")

---

条目格式：`- 确认(YYYY-MM-DD): 具体内容`，例如 `- 确认(2026-06-25): commit 使用中文描述`
旧偏好被覆盖时更新日期而非重复添加。
