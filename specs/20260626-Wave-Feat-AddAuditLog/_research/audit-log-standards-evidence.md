# 技术调研：审计日志标准要求与字段证据映射

**日期**: 2026-07-03  
**适用范围**: `20260626-Wave-Feat-AddAuditLog`  
**目的**: 把“第三方审计要看的审计日志”拆成两层：

1. **标准/法规直接要求什么**
2. **为了让第三方审计公司更容易使用，我们额外建议什么**

---

## 一、先说结论

### 1.1 哪些来源最能直接落到审计日志设计

- **最能直接落到字段和能力设计的公开主依据**：`NIST SP 800-53 Rev. 5` 的 `AU` 控制族 + `NIST SP 800-92`
- **最能直接落到“行业里日志长什么样”的公开官方实现**：`Kubernetes audit event` 和 `Grafana audit logs`
- **最能约束“日志里不能乱记什么”的法规**：`GDPR`，以及中国的 `PIPL / DSL / CSL`
- **最能解释“为什么客户和第三方审计公司会关心这些东西”**：`SOC 2`

### 1.2 一个重要澄清

我前面给的 `P0` 字段列表，**不是每一项都能在某个标准原文里找到一条一模一样的硬性规定**。

更准确地说，它们分 3 类：

- **标准直接要求**：例如 `timestamp`、`actor`、`action`、`target`、`result`
- **强烈建议但不是所有标准都逐字要求**：例如 `source_ip`、`user_agent`、`event_id`
- **Wave 自身多租户/导出/排查场景需要**：例如 `tenant/org_id`、`request_id/correlation_id`

所以如果要对外说法务/审计口径，应该说：

> 本方案基于 NIST / GDPR / SOC 2 的公开要求与公开官方产品实现做证据映射；其中字段级 schema 同时包含标准硬要求和为了第三方审计可用性补充的产品设计字段。

---

## 二、证据来源与可见性

| 来源 | 角色 | 能确认什么 | 公开可见性 |
| --- | --- | --- | --- |
| [NIST SP 800-92](https://csrc.nist.gov/pubs/sp/800/92/final) | 日志管理总纲 | 审计日志需要有健全的 log management 生命周期 | 完全公开 |
| [NIST SP 800-53 Rev. 5](https://csrc.nist.gov/pubs/sp/800/53/r5/upd1/final) | 控制框架 | 事件选择、记录内容、时间戳、保护、保留、审查、报表 | 完全公开 |
| [NIST 官方 OSCAL 控制文本](https://raw.githubusercontent.com/usnistgov/oscal-content/v1.4.0/src/nist.gov/SP800-53/rev5/xml/NIST_SP-800-53_rev5_catalog.xml) | 控制细节 | 可直接提取 `AU-2/AU-3/AU-7/AU-8/AU-9/AU-11/AU-12` 的文字 | 完全公开 |
| [AICPA SOC Suite](https://www.aicpa-cima.com/resources/landing/system-and-organization-controls-soc-suite-of-services) | 第三方 assurance 语境 | SOC 是给外部用户评估外包服务风险的 assurance 报告体系 | 官方公开概览；详细 criteria 非全文公开 |
| [ISO/IEC 27001:2022](https://www.iso.org/standard/27001) | 国际安全管理基线 | 是国际化安全合规基线之一 | 官方页公开；详细条文需购标 |
| [ISO/IEC 27701:2025](https://www.iso.org/standard/71670.html) | 国际隐私管理基线 | 审计日志涉及个人信息时的隐私管理基线 | 官方页公开；详细条文需购标 |
| [GDPR 官方文本](https://eur-lex.europa.eu/legal-content/EN/TXT/?uri=CELEX:32016R0679) | 欧盟法律约束 | 数据最小化、存储期限、安全措施、privacy by design | 完全公开 |
| [Kubernetes Auditing](https://kubernetes.io/docs/tasks/debug/debug-cluster/audit/) | 官方实现先例 | 审计记录要回答 who / what / when / where / from where | 完全公开 |
| [Kubernetes audit API](https://kubernetes.io/docs/reference/config-api/apiserver-audit.v1/) | 官方事件 schema | `auditID`、`user`、`sourceIPs`、`userAgent`、`objectRef`、`responseStatus`、`requestReceivedTimestamp` | 完全公开 |
| [Grafana Audit Logs](https://grafana.com/docs/grafana/latest/setup-grafana/configure-security/audit-grafana/) | 官方产品 schema | `timestamp`、`user`、`action`、`requestUri`、`resources`、`result`、`ipAddress`、`userAgent` | 完全公开 |
| [Amplitude Audit Logs API](https://amplitude.com/docs/apis/audit-logs) | 官方产品定位 | 审计日志导出用于 security monitoring、compliance、operational reporting | 完全公开 |
| [中国个人信息保护法](https://flk.npc.gov.cn/detail?id=ZmY4MDgxODE3YjY0NzJhMzAxN2I2NTZjYzIwNDAwNDQ) | 中国个人信息法域 | 日志本身若含个人信息，受最小必要、处理目的等约束 | 官方公开 |
| [中国数据安全法](https://flk.npc.gov.cn/detail?id=ZmY4MDgxODE3OWY1ZTA4MDAxNzlmODg1YzdlNzAzOTI) | 中国数据治理法域 | 日志涉及数据分类分级、重要数据治理时受约束 | 官方公开 |
| [中国网络安全法](https://flk.npc.gov.cn/detail?id=MmM5MDlmZGQ2NzhiZjE3OTAxNjc4YmY4Mjc2ZjA5M2Q%3D) | 中国网络安全法域 | 网络运营、安全留痕、运营审计相关义务背景 | 官方公开 |

> 说明：
>
> - `SOC 2`、`ISO 27001`、`ISO 27701` 的**详细控制条文**不是都能在官网免费全文查看，所以本文件只把它们作为“应对外部审计/国际采购的框架来源”，字段级证据主要落在公开可读的 `NIST + 官方产品文档 + GDPR` 上。
> - 中国法律更多是**约束边界**，例如日志里能不能放敏感个人信息、保留多久、能否跨境；它们不是字段 schema 规范。

---

## 三、标准明确要求了什么

下面只列和审计日志设计最相关的公开要求。

### 3.1 事件范围要先定义清楚

依据：

- [NIST AU-2 Event Logging](https://raw.githubusercontent.com/usnistgov/oscal-content/v1.4.0/src/nist.gov/SP800-53/rev5/xml/NIST_SP-800-53_rev5_catalog.xml)
- [NIST AU-12 Audit Record Generation](https://raw.githubusercontent.com/usnistgov/oscal-content/v1.4.0/src/nist.gov/SP800-53/rev5/xml/NIST_SP-800-53_rev5_catalog.xml)

能确认的要求：

- 系统要先定义“哪些事件要记”
- 要能说明这些事件为什么足够支撑事后调查
- 不是所有事件都记，而是有选择地记

对 Wave 的含义：

- `created / updated / deleted / logged_in / logged_out` 这种管理面事件目录要先锁定
- “读操作要不要记”“内部任务要不要记”不能凭感觉，要能给出 rationale

### 3.2 审计记录至少要能说明 who / what / when / where / result

依据：

- [NIST AU-3 Content of Audit Records](https://raw.githubusercontent.com/usnistgov/oscal-content/v1.4.0/src/nist.gov/SP800-53/rev5/xml/NIST_SP-800-53_rev5_catalog.xml)
- [Kubernetes Auditing](https://kubernetes.io/docs/tasks/debug/debug-cluster/audit/)
- [Grafana Audit Logs](https://grafana.com/docs/grafana/latest/setup-grafana/configure-security/audit-grafana/)

能确认的要求：

- 记录要说明：发生了什么、何时发生、发生在何处、事件来源、结果、相关主体/对象
- Kubernetes 官方把这个问题写得非常直白：要能回答 `what happened / when / who / on what / where / from where / to where`
- Grafana 的官方 schema 也直接把这些维度展开成结构化字段

对 Wave 的含义：

- 这就是 `actor + action + target + timestamp + result + source` 的直接来源

### 3.3 时间戳要规范，最好统一用 UTC

依据：

- [NIST AU-8 Time Stamps](https://raw.githubusercontent.com/usnistgov/oscal-content/v1.4.0/src/nist.gov/SP800-53/rev5/xml/NIST_SP-800-53_rev5_catalog.xml)
- [Grafana Audit Logs](https://grafana.com/docs/grafana/latest/setup-grafana/configure-security/audit-grafana/)

能确认的要求：

- 审计记录必须带时间戳
- 时间戳要使用系统时钟并满足组织定义的精度要求
- 时间表达要使用 `UTC`、带 `UTC offset`，或本地时间附带偏移
- Grafana 官方明确写明 `timestamp` 使用 `UTC/RFC3339`

对 Wave 的含义：

- `created_at TIMESTAMPTZ` 是必须项
- 导出时应显式说明时区，优先 `UTC`

### 3.4 审计日志要防未授权访问、修改、删除

依据：

- [NIST AU-9 Protection of Audit Information](https://raw.githubusercontent.com/usnistgov/oscal-content/v1.4.0/src/nist.gov/SP800-53/rev5/xml/NIST_SP-800-53_rev5_catalog.xml)
- [NIST AU-10 Non-repudiation](https://raw.githubusercontent.com/usnistgov/oscal-content/v1.4.0/src/nist.gov/SP800-53/rev5/xml/NIST_SP-800-53_rev5_catalog.xml)

能确认的要求：

- 审计信息和审计工具要防未授权访问、修改、删除
- 审计要支持事后不可抵赖

对 Wave 的含义：

- `append-only` 不是标准原文逐字出现，但它是满足这类要求的很自然实现
- 导出包最好带 checksum / manifest
- 谁查看、谁导出审计日志，本身也要进审计

### 3.5 审计日志必须有保留策略

依据：

- [NIST AU-11 Audit Record Retention](https://raw.githubusercontent.com/usnistgov/oscal-content/v1.4.0/src/nist.gov/SP800-53/rev5/xml/NIST_SP-800-53_rev5_catalog.xml)
- [GDPR 官方文本](https://eur-lex.europa.eu/legal-content/EN/TXT/?uri=CELEX:32016R0679)

能确认的要求：

- 审计日志要保留到足以支撑事后调查和监管/组织保留要求
- 但保留也不是无限期；GDPR 同时要求存储期限限制

对 Wave 的含义：

- 审计日志要支持 retention policy
- 第三方审计需要的保留期和“隐私最小化”之间要做平衡，不能无上限堆明细

### 3.6 审计日志要支持审查、分析、报表，不得破坏原始时序

依据：

- [NIST AU-6 Audit Record Review, Analysis, and Reporting](https://raw.githubusercontent.com/usnistgov/oscal-content/v1.4.0/src/nist.gov/SP800-53/rev5/xml/NIST_SP-800-53_rev5_catalog.xml)
- [NIST AU-7 Audit Record Reduction and Report Generation](https://raw.githubusercontent.com/usnistgov/oscal-content/v1.4.0/src/nist.gov/SP800-53/rev5/xml/NIST_SP-800-53_rev5_catalog.xml)
- [Amplitude Audit Logs API](https://amplitude.com/docs/apis/audit-logs)

能确认的要求：

- 日志不只是“写下来”，还要能被 review / analyze / report
- 报表和缩减能力不能改变原始内容或时间顺序
- Amplitude 官方把 audit logs API 直接定位为可用于 security monitoring、compliance、operational reporting

对 Wave 的含义：

- 第三方审计导出不是“附加功能”，而是审计日志闭环的一部分
- 导出包应说明时间范围、过滤条件、记录数、生成时间、原始顺序

### 3.7 审计日志本身也受隐私最小化约束

依据：

- [NIST AU-3 Guidance](https://raw.githubusercontent.com/usnistgov/oscal-content/v1.4.0/src/nist.gov/SP800-53/rev5/xml/NIST_SP-800-53_rev5_catalog.xml)
- [GDPR 官方文本](https://eur-lex.europa.eu/legal-content/EN/TXT/?uri=CELEX:32016R0679)
- [中国个人信息保护法](https://flk.npc.gov.cn/detail?id=ZmY4MDgxODE3YjY0NzJhMzAxN2I2NTZjYzIwNDAwNDQ)

能确认的要求：

- NIST 明确提醒：审计记录可能暴露个人信息，需要考虑如何降低隐私风险
- GDPR 会约束 `data minimisation`、`storage limitation`、`security of processing`
- 中国法域下，如果日志包含个人信息，也会进入个人信息处理约束

对 Wave 的含义：

- 不要把明文密码、token、secret、敏感配置原样塞进 `detail`
- `before/after` 必须支持敏感字段 masked

---

## 四、P0 字段逐条证据映射

下面这张表专门回答：“前面提的字段，到底哪些有依据？”

| 字段 | 结论级别 | 依据 | 说明 |
| --- | --- | --- | --- |
| `event_id` | 强烈建议 | [Kubernetes audit API](https://kubernetes.io/docs/reference/config-api/apiserver-audit.v1/) 中有 `auditID` | 不是所有标准逐字要求，但官方实现普遍会有稳定事件 ID，便于导出校验、去重、链路引用 |
| `tenant_id / org_id / project_id` | Wave 产品必需 | [Grafana Audit Logs](https://grafana.com/docs/grafana/latest/setup-grafana/configure-security/audit-grafana/) 中 `user.orgId` 是官方多租户先例 | 这不是国际标准硬要求，而是多租户产品为了权限隔离、按租户导出、审计范围控制必须有的维度 |
| `timestamp (UTC)` | 标准直接要求 | [NIST AU-8](https://raw.githubusercontent.com/usnistgov/oscal-content/v1.4.0/src/nist.gov/SP800-53/rev5/xml/NIST_SP-800-53_rev5_catalog.xml), [Grafana Audit Logs](https://grafana.com/docs/grafana/latest/setup-grafana/configure-security/audit-grafana/) | 这是硬要求，不建议省略 |
| `actor` | 标准直接要求 | [NIST AU-3](https://raw.githubusercontent.com/usnistgov/oscal-content/v1.4.0/src/nist.gov/SP800-53/rev5/xml/NIST_SP-800-53_rev5_catalog.xml), [Kubernetes audit API](https://kubernetes.io/docs/reference/config-api/apiserver-audit.v1/), [Grafana Audit Logs](https://grafana.com/docs/grafana/latest/setup-grafana/configure-security/audit-grafana/) | 至少要能识别是谁发起的；对 API token 场景还要能区分 token 主体 |
| `action` | 标准直接要求 | [NIST AU-3](https://raw.githubusercontent.com/usnistgov/oscal-content/v1.4.0/src/nist.gov/SP800-53/rev5/xml/NIST_SP-800-53_rev5_catalog.xml), [Kubernetes audit API](https://kubernetes.io/docs/reference/config-api/apiserver-audit.v1/), [Grafana Audit Logs](https://grafana.com/docs/grafana/latest/setup-grafana/configure-security/audit-grafana/) | 对应 `what type of event occurred`；Wave 里的 `created/updated/deleted/logged_in/logged_out` 有依据 |
| `target` | 标准直接要求 | [NIST AU-3](https://raw.githubusercontent.com/usnistgov/oscal-content/v1.4.0/src/nist.gov/SP800-53/rev5/xml/NIST_SP-800-53_rev5_catalog.xml), [Kubernetes objectRef](https://kubernetes.io/docs/reference/config-api/apiserver-audit.v1/), [Grafana resources](https://grafana.com/docs/grafana/latest/setup-grafana/configure-security/audit-grafana/) | 需要知道是对哪个对象做了操作 |
| `result` | 标准直接要求 | [NIST AU-3](https://raw.githubusercontent.com/usnistgov/oscal-content/v1.4.0/src/nist.gov/SP800-53/rev5/xml/NIST_SP-800-53_rev5_catalog.xml), [Kubernetes responseStatus](https://kubernetes.io/docs/reference/config-api/apiserver-audit.v1/), [Grafana result](https://grafana.com/docs/grafana/latest/setup-grafana/configure-security/audit-grafana/) | 这是我认为当前 spec 里最值得补的一项；只有“做了什么”不够，审计还需要知道成功/失败 |
| `source_ip` | 强烈建议，接近硬要求 | [NIST AU-3 Guidance](https://raw.githubusercontent.com/usnistgov/oscal-content/v1.4.0/src/nist.gov/SP800-53/rev5/xml/NIST_SP-800-53_rev5_catalog.xml), [Kubernetes sourceIPs](https://kubernetes.io/docs/reference/config-api/apiserver-audit.v1/), [Grafana ipAddress](https://grafana.com/docs/grafana/latest/setup-grafana/configure-security/audit-grafana/) | 标准强调来源和地址信息；产品实现里几乎都会带 IP |
| `user_agent` | 建议 | [Kubernetes userAgent](https://kubernetes.io/docs/reference/config-api/apiserver-audit.v1/), [Grafana userAgent](https://grafana.com/docs/grafana/latest/setup-grafana/configure-security/audit-grafana/) | 不是标准必须，但对第三方排查很有价值；同时要注明它是客户端自报、不可完全信任 |
| `request_id / correlation_id` | 建议 | [Kubernetes auditID](https://kubernetes.io/docs/reference/config-api/apiserver-audit.v1/), [NIST AU-7](https://raw.githubusercontent.com/usnistgov/oscal-content/v1.4.0/src/nist.gov/SP800-53/rev5/xml/NIST_SP-800-53_rev5_catalog.xml) | 标准不会逐字要求 correlation id，但导出、去重、串联批操作和 API 请求时非常有价值 |
| `changed_fields` | 建议 | [NIST AU-3](https://raw.githubusercontent.com/usnistgov/oscal-content/v1.4.0/src/nist.gov/SP800-53/rev5/xml/NIST_SP-800-53_rev5_catalog.xml), [Kubernetes Request / RequestResponse 审计级别](https://kubernetes.io/docs/tasks/debug/debug-cluster/audit/) | 标准要求记录内容足以支撑事后调查，但不强制字段级 diff。对配置型实体，至少记录 `changed_fields` 会显著提高外审可读性 |
| `before / after` 摘要 | 建议，需配合敏感字段掩码 | [NIST AU-3 Guidance](https://raw.githubusercontent.com/usnistgov/oscal-content/v1.4.0/src/nist.gov/SP800-53/rev5/xml/NIST_SP-800-53_rev5_catalog.xml), [GDPR 官方文本](https://eur-lex.europa.eu/legal-content/EN/TXT/?uri=CELEX:32016R0679) | 不是所有标准硬要求；它是“证明到底改了什么”的高价值设计，但必须避免把敏感值原样写入 |

---

## 五、对当前 Wave spec 的直接启发

### 5.1 当前 spec 里已经有依据的点

- `append-only`
- `ip_address`
- `created_at`
- `account_id`
- `action`
- `domain + feature + target_id`
- `detail.changes[]`
- 导出能力

这些都能在上面的公开依据里找到支撑。

### 5.2 当前 spec 里我建议再补强的点

#### A. 建议补 `result`

原因：

- `NIST AU-3` 明确要求记录事件结果
- `Kubernetes` 有 `responseStatus`
- `Grafana` 有 `result.statusType / statusCode / failureMessage`

建议：

- 至少增加 `result_status`：`success / failure`
- 如果失败有业务错误码，可再加 `result_code` / `failure_reason`

#### B. 建议补稳定 `event_id`

原因：

- 第三方审计导出时，事件稳定标识很有价值
- 对分页导出、对账、外部引用、去重都更稳

建议：

- 即使 DB 主键是自增 `id`，对外导出也最好暴露稳定事件 ID

#### C. 建议保留 `changed_fields`，但把 `before/after` 作为“有条件增强”

原因：

- 第三方审计公司真正常看的，是“哪些控制对象被谁改了”
- 全量 before/after 很容易放大隐私和敏感配置风险

建议：

- 最低版：`changes[].field`
- 增强版：对非敏感字段记录 `before/after`
- 敏感字段统一输出 `masked`

#### D. `tenant/org_id/project_id` 要明确标注为“产品架构字段”，不是“国际标准强制字段”

这样后续对内对外表述会更稳，不会把产品判断说成法规原文。

---

## 六、给第三方审计导出时，最低应保证什么

如果目标很明确，就是“导出给第三方审计公司”，我建议最低交付包包含：

1. **原始数据文件**：`csv` 或 `jsonl`
2. **字段字典**：每列含义、枚举值、脱敏规则
3. **导出说明**：导出时间、时间范围、过滤条件、时区
4. **完整性信息**：记录数、checksum、导出批次 ID
5. **访问痕迹**：谁触发了这次导出，这次导出本身也进审计

这组内容虽然不是某一条标准逐字写死，但和 `AU-6/AU-7/AU-9` 的精神完全一致，也最符合第三方审计的使用方式。

---

## 七、落地建议：把字段分成三层

### 7.1 标准硬要求层

- `timestamp`
- `actor`
- `action`
- `target`
- `result`

### 7.2 高价值审计增强层

- `source_ip`
- `user_agent`
- `event_id`
- `changed_fields`
- `before/after` 摘要（敏感字段 masked）

### 7.3 Wave 场景层

- `org_id`
- `project_id`
- `source = ui / api_token`
- `request_id / correlation_id`

---

## 八、最终回答：前面那组 P0 有没有依据

有依据，但要分层说，不能说成“每个字段都是标准硬要求”。

- `timestamp / actor / action / target / result`：可以直接说是有公开标准依据
- `source_ip / user_agent / event_id`：可以说是官方实现中非常常见、对第三方审计高度有用
- `org_id / project_id / correlation_id / before_after 摘要`：更准确地说是为了 Wave 的多租户与外审可用性做的产品设计增强

如果后面要把这套写进 `spec.md` 或 `decisions.md`，建议把措辞改成：

> 审计日志字段分为“标准必需字段”和“产品增强字段”。标准必需字段满足公开控制要求；增强字段服务于多租户隔离、导出可复核性和第三方审计效率。
