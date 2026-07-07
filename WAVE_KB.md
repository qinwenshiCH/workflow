# Wave 知识库

本文件记录 Wave 项目的关键事实，用于 spec 阶段自动核对。涉及 Wave 落地的 artifact 必须对照此文件，未覆盖的假设需先探索 Wave 代码库核实，核实结果同步追加到这里。

## Schema

| 类型 | 命名 | 示例 |
|------|------|------|
| global | `schema_global` | `schema_global` |
| meta（per-project） | `schema_{projectID}` | `schema_3208` |
| data（per-project） | `schema_{projectID}` | `schema_3208` |

- meta 和 data 前缀相同（`schema_`），但在不同数据库（`sw_meta` vs `sw_data`）
- 配置项：`pg_global_schema: schema_global`、`pg_meta_schema_prefix: schema_`、`pg_data_schema_prefix: schema_`
- 代码位置：`pkg/config/inf_cfg.go`

## Migration

- **迁移脚本目录**：`script/migration/scripts/`
- **命名格式**：`{dbtype}_v{version}_{description}.{sql|go}`
  - 示例：`global_v20260604_account_api_token_expires_at.sql`、`meta_v20260602_tracking_plan_archive.sql`
- **bootstrap DDL**：`script/sql/pgsql/global.sql` / `meta.sql` / `pgdata.sql`
  - global.sql 以 `SET search_path TO schema_global;` 开头
  - 通过 `go:embed` 嵌入到 Go 二进制（`script/sql/pgsql/tables.go`）
- **框架**：Go migration framework（`script/migration/`）
  - `ExecGlobalSQL(ctx, sql)` 自动设 `search_path`，SQL 里不需要 schema 前缀
  - SQL-only 迁移脚本由 `loader.go` 自动加载并包装 schema 上下文
  - 注册式：实现 `Migration` 接口后注册到 `registry.go`
  - ORM/DAO 层：`fmt.Sprintf("%s.%s", schema, table)`
- **禁止 `CREATE INDEX CONCURRENTLY`**：现有 migration 运行在事务中；新建表用普通 `CREATE INDEX IF NOT EXISTS`
- **数据库**：global schema 在 `sw_meta` 数据库中

## 配置

- 配置文件：`configs/inf/inf.{env}.yml`
- 配置结构体：`pkg/config/inf_cfg.go`
