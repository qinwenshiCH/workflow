# AddAuditLog 调研资料索引

本目录保存活动日志方案设计过程中的背景调研。  
如果你是第一次进入这组材料，**不要随机挑一篇看**，建议按下面的顺序读。

---

## 建议阅读顺序

### 第零层：先理解 PostgreSQL 数据记录体系

| 文件 | 作用 |
|------|------|
| [postgresql-tracking-landscape.md](./postgresql-tracking-landscape.md) | PostgreSQL 所有"留痕"机制全景（WAL/逻辑解码/pgAudit/Trigger/Event Trigger），澄清 pgAudit ≠ binlog 等常见误解 |

### 第一层：先建立 Wave 的问题空间

| 文件 | 作用 |
|------|------|
| [wave-research.md](./wave-research.md) | 先看清 Wave 当前有哪些历史机制、债务和对象范围 |
| [external-products/README.md](./external-products/README.md) | 再看外部产品被分成了哪几类 archetype，各自解决什么问题 |

### 第二层：看最像 Wave 的参考

| 文件 | 作用 |
|------|------|
| [external-products/harbor.md](./external-products/harbor.md) | 最接近“产品内可查询的资源审计表” |
| [external-products/grafana.md](./external-products/grafana.md) | 最接近“应用层生成审计事件，但 sink 偏日志系统” |

### 第三层：看为什么不能照搬安全审计

| 文件 | 作用 |
|------|------|
| [external-products/kubernetes.md](./external-products/kubernetes.md) | 理解 request 审计、policy、level、stage 的边界 |
| [external-products/vault.md](./external-products/vault.md) | 理解敏感字段和 fail-closed 为什么会变成系统边界问题 |

### 第四层：看为什么 trigger 不是主方案

| 文件 | 作用 |
|------|------|
| [external-products/go-history.md](./external-products/go-history.md) | 理解 row history 和 business activity 的本质差异 |

---

## 这组文档统一回答什么

每篇外部调研文档现在都尽量统一回答下面 6 个问题：

1. 这个系统审计的真相对象是什么
2. 它的审计入口放在哪一层
3. 它有没有内建审计表
4. 它有哪些核心模块和写入/查询流程
5. 它为什么会做出这种设计
6. 这些选择对 Wave 有什么启发或排除意义

如果某篇还不能回答这 6 个问题，它就还不够支撑设计评审。

---

## Wave 本地调研

| 文件 | 内容 |
|------|------|
| [wave-research.md](./wave-research.md) | Wave 现有操作记录、历史债务、项目对象范围 |

---

## 外部产品调研

| 文件 | 内容 |
|------|------|
| [external-products/README.md](./external-products/README.md) | 外部产品横向对比、archetype 分类、Wave 总结 |
| [external-products/harbor.md](./external-products/harbor.md) | Harbor：产品内可查询的资源审计表 |
| [external-products/grafana.md](./external-products/grafana.md) | Grafana：应用层审计事件 + file/Loki/console exporter |
| [external-products/kubernetes.md](./external-products/kubernetes.md) | Kubernetes：API server request 审计 |
| [external-products/vault.md](./external-products/vault.md) | Vault：secret 系统的强安全审计 |
| [external-products/go-history.md](./external-products/go-history.md) | go-history：PostgreSQL trigger 行级历史 |

---

## 非 Go 参考

| 文件 | 内容 |
|------|------|
| [posthog-research.md](./posthog-research.md) | PostHog activity log / Django 自动采集模式 |
| [posthog-usecase-prd.md](./posthog-usecase-prd.md) | PostHog 参考下的用例和 PRD 分析 |
