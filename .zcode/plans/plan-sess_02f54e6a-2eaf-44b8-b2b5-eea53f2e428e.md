# 订阅规则方案优化

按你确认的四个决策落地：**完整订阅隔离 + RULE-SET dry-run + 镜像全可配置 + 一次性提交**。跨 4 个功能块，全部完成后一次性提交推送。

> 所有改动遵循现有代码风格：gorm 模型 + service 层校验 + 薄 HTTP handler + react-query 单 key 刷新。每块附测试。

---

## 块 1：规则组织维度

**后端**
- `internal/repository/rules.go`：新增 `SetSubscriptionRulesEnabled(ctx, ids []int64, enabled bool)`（事务批量更新 enabled+updated_at，复刻 `SetOutboundEnabled` 范式）；`UpdateSubscriptionRulePriorities(ctx, idPriority map[int64]int)`（批量改 priority）
- `internal/repository/rules.go:104` `SubscriptionRules`：加可选 `category` 过滤参数（新增重载或 `SubscriptionRulesFilter` 结构）
- `internal/service/rules.go` `RulesOverview`：聚合返回 `categories []string`（从已加载 rules 派生去重排序，避免新查询），在 `RulesOverview` 结构体加字段
- `internal/http/rules.go` + `router.go`：新增 `POST /rules/batch-enable`、`POST /rules/batch-disable`（body `{ids:[]}`）、`PUT /rules/priorities`（body `{items:[{id,priority}]}`）

**前端**
- `frontend/src/features/rules/constants.ts`：新增 `PROVIDER_FORMAT_OPTIONS`（修块 2 的 bug 顺带做）
- `RulesPage.tsx`：
  - 加 category 筛选 `<Select>`（options 来自 overview.categories）+ 规则类型筛选
  - Table 加 `rowSelection`（多选）+ 批量启用/禁用按钮
  - category 输入控件从 `<Input>` 改 `<AutoComplete>`（既允许自定义又给已有分类建议）
- `client.ts`：加 `batchEnableRules/batchDisableRules/updateRulePriorities` 方法

---

## 块 2：前后端一致性 + 按订阅隔离

**2a. format 漏 mrs 修复（最小）**
- `frontend/src/features/rules/constants.ts`：新增 `PROVIDER_FORMAT_OPTIONS = [{yaml},{text},{mrs}]`
- `ProviderEditButton.tsx:77`：内联 options 改为引用常量（对齐第 7 行 behavior import）

**2b. ProxyGroup/RuleProvider 按订阅隔离（完整）**

后端新增两张 binding 表（复刻现有 `subscription_rule_bindings`）：
- `models.go`：新增 `SubscriptionGroupBindingRow{SubscriptionID, GroupID}`（表 `subscription_group_bindings`）、`SubscriptionProviderBindingRow{SubscriptionID, ProviderID}`（表 `subscription_provider_bindings`），注册进 `allModels()`
- `rules.go`：复刻 4 个函数 `Set/SubscriptionGroupBindings`、`Set/SubscriptionProviderBindings`；`DeleteProxyGroup`、`DeleteRuleProvider` 加事务级联删 binding
- `subscriptions.go`：`Subscription` 加 `GroupIDs/ProviderIDs []int64`；`hydrateProtocols` 扩展批量加载两组 binding（复刻 291-298 行 rule 写法）；新增 `subscriptionGroupBindingsBySubscriptionIDs`、`subscriptionProviderBindingsBySubscriptionIDs`（复刻 327-347）；`DeleteSubscription` 补删两张新表
- `service/subscriptions.go`：`SubscriptionUpsertRequest` 加 `GroupIDs/ProviderIDs`；Create/Update 调用对应 Set 方法 + 回填
- `service/rules_clash.go:61-85` `ActiveRuleSetForSubscription`：**核心改动**——在 Rules 过滤后，对 `rs.ProxyGroups` 和 `rs.RuleProviders` 做同样的白名单过滤（调新 binding 查询，空=继承全部，语义对齐 70-72 行）。下游 `share_clash.go` 无需改

前端：
- `types/index.ts`：`Subscription`、`SubscriptionUpsertRequest` 加 `groupIds/providerIds: number[]`
- `SubscriptionsPage.tsx`：加 groupOptions/providerOptions（数据已在 overview）、两个 `Form.Item`（多选）、openCreate/openEdit 补初值
- `utils.ts buildPayload`：透传两字段

---

## 块 3：dry-run 覆盖增强

**后端 RULE-SET 本地求值**（技术已验证，依赖 `github.com/sagernet/sing-box/common/srs` 已在 go.mod）
- `internal/service/rules_matcher.go:80-83`：新增 `case "RULE-SET"` 分支 → 新函数 `evalRuleSet(host, providerName, cache)`
  - 通过 provider 名查 `RuleProviders` 拿 ID+Format+缓存路径
  - mrs 走 `srs.Read(file, true)` → `Upgrade()` → 遍历 `HeadlessRule` 用 `strings`/正则做 domain 匹配（避免引入重的 `route/rule` 包，手写 domain/suffix/keyword 三类匹配覆盖 90% 场景）
  - yaml/text 按行解析 `TYPE,VALUE` 同样匹配
  - 缓存缺失/解析失败 → 新状态 `ruleset-not-cached`
- `rules_matcher.go` 需要拿到 `*ruleProviderCache` 和 provider 列表：通过给 matcher 增加依赖注入（构造时传入 cache 引用 + providers 快照）

**前端 dry-run UI**（当前完全缺失）
- `client.ts`：加 `dryRunRules(host)`、`geoStatus()`、`ensureGeoDatabases()` 方法
- `RulesPage.tsx`：规则 Tab 顶部加「规则探测」区——`Input` 输入域名 + 按钮，调 `dryRunRules`，展示命中规则（高亮）+ trace 列表（每条规则的状态：match/miss/not-supported/ruleset-not-cached）

---

## 块 4：健壮性 — 镜像可配置 + re-host 引导

**4a. 镜像/上游全部接通 settings 表**

新增 settings 键（走现有 `repository.Setting/SetSetting` 键值存储）：
- `rule_provider.mirror_prefix`（默认 `https://gh-proxy.com/`）→ `rule_provider_presets.go` 的 `mirrorPrefix` 改成运行时从 settings 读，`const` 降级为默认值常量
- `clash.geox_url_geoip/geosite/mmdb/asn`（默认当前 jsdelivr 值）→ `share_clash.go:57-62` geox-url 从 settings 读，缺失用硬编码默认
- `geo.database_url_prefix`（默认空）→ **修复 `geo_database.go` 的挂羊头卖狗肉 bug**：`config/runtime.go` 加 `GeoDatabaseURLPrefix` 字段，`app.go:58` 传入，让注释承诺的配置真正生效

前端：分享设置页（SubscriptionsPage 的 settingsForm）加这些字段的输入（带"留空用默认"提示）

**4b. re-host baseURL 引导**
- `share_subscription.go` `ShareSettings()` 结构加 `BaseURL` 字段并读取 `subscription.base_url`；`UpdateShareSettings` 接收并持久化
- 前端分享设置页加 baseURL 输入框 + 提示文案：「留空则 Clash 订阅直接用规则集上游 URL；配置本面板外网地址后，客户端从本服务器拉取 re-host 规则集（需先在规则集页同步）」

**4c. seed 守卫收紧**（不加 UNIQUE，按探索结论）
- `migrations.go:190-198` NOT EXISTS 守卫：收紧空 match_value 判定——仅当 `type='MATCH'` 时才允许 match_value 为空参与幂等判断，避免空 DOMAIN 规则的歧义匹配

---

## 测试

每块补充对应测试：
- 块 1：批量启停、priority 批量更新、category 聚合的 repo 测试
- 块 2：`ActiveRuleSetForSubscription` 对 groups/providers 的隔离单测（空 binding=全量、非空=子集）、binding 级联删
- 块 3：`evalRuleSet` 对 mrs/yaml 缓存的匹配单测（用小型 fixture 规则集）
- 块 4：settings 读取默认值回退、ShareSettings baseURL 往返、seed 守卫收紧后的幂等测试

---

## 风险与缓解

| 风险 | 缓解 |
|------|------|
| 新 binding 表需 AutoMigrate 建表 | 项目 pre-release，fresh DB 重建策略已确立（migrations.go:16 注释）|
| RULE-SET 手写匹配覆盖不全 | 优先实现 domain/suffix/keyword（覆盖 .mrs 主流内容），IP 类返回 not-supported 可接受 |
| 一次性提交改动大 | 全部完成后跑 `go build ./... && go test ./internal/... && cd frontend && npm run build` 验证，再提交推送 |

完成后一次性 commit + push。预计涉及 ~25 个文件。