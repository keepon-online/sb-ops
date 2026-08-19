# 产品优化 Backlog

> 场景：单人管理员（自己）+ 分享订阅链接给用户/朋友/家人。
> 优先级按「对日常使用的影响」排序。完成即勾选。

## 已完成

- [x] **P0-2 订阅链接只读复制**（不轮换、不踢人）。
- [x] **移除 RBAC / 多账号管理**：删除 admin/operator 角色与权限体系、`roles`/`account_roles` 表、`/api/v1/accounts` 接口与「账号权限」页，简化为单管理员（保留登录/登出/改密/会话与审计日志）。
- [x] **P0-1 告警外发（Webhook / Telegram）**：新增 `WebhookNotifier`（每次派发实时读取设置，改配置无需重启）与 `CompositeNotifier`（SSE 事件总线 + 外发并行）；设置项 `notifications.webhookURL` / `webhookSecret`（X-Webhook-Secret 头）/ `telegramBotToken` / `telegramChatID`；webhook 未配置时静默跳过。设置页由既有通用列表自动展示。
- [x] **P1-3 收敛 Clash API 配置 + 默认可用**：以面板管理的 `singbox_config` 为唯一事实来源 —— 启动自动生成 Clash API secret（安全默认）；实时连接观测与流量采集都从 DB 派生地址与 secret（`config.yaml` 仅作可选覆盖）；采集器每次重连动态解析（面板改动无需重启）；采集请求带 `Authorization: Bearer` 认证；概览页「实时连接不可用」提示带直达配置按钮。
- [x] **P1-4 流量采集自检 + 采集失败告警**：采集器记录「最后成功采集时间」；Clash 断流即报错（自愈式重连，采集恢复自动解决）；新增 `traffic_stalled` 告警（readiness `trafficCollection` 检查 + 概览页「最近采集时间」展示）；setup 完成前不误报。
- [x] **P1-5 路由规则概念澄清**：服务端路由 →「服务器分流」、订阅规则 →「订阅分流」，两页各加固定说明与互跳按钮（`/singbox?tab=routes` 深链）；用户表单文案同步。
- [x] **P2-6 订阅方导入引导 + 二维码**：凭据弹窗为 4 种订阅格式各加客户端导入提示与可展开二维码（antd 内置 QRCode）。
- [x] **P2-7 流量数据保留策略**：设置项 `traffic.retentionDays`（默认 90 天，0 为永久），后台每 6 小时清理历史与采集批次；DB 容量告警文案改为建议缩短保留期。
- [x] **P2-8 个人场景复杂度税（UI 收敛）**：审计日志与系统设置折叠进主导航「系统」分组，高频操作区收敛为 7 项。
- [x] **配置统一 config.yaml，删除 settings 机制**：移除 `internal/settings` 包、`settings` 表、`/api/v1/settings` 接口、前端「系统设置」页与导航；`notifications.*`、`traffic.retention_days`、`deployment.*` 全部迁入 `config.yaml`（viper 热重载，notifier/retention 经 `func() config.RuntimeConfig` 实时读）；删除僵尸键 `panel.siteTitle`。

---

## P0 —— 现在就该做

### P0-1 告警外发（Webhook / Telegram）

**现状**：`internal/observability/outbox.go` 的 `EventNotifier` 只把告警 `bus.Publish` → SSE 推给打开中的面板；告警只有 3 类（`runtime_sync_failed` / `database_capacity` / `aggregation_failed`）。出问题只有登录面板才看得见。

**改动**
- [x] 新增真正的 `Notifier` 实现（`WebhookNotifier`），`Dispatch` 时把 `alert.open` / `alert.resolved` POST 到配置的 URL，内置 Telegram Bot `sendMessage` 模板。
- [x] 设置项 `notifications.webhookURL` / `webhookSecret` / `telegramBotToken` / `telegramChatID`（复用 `internal/settings`，设置页通用列表自动展示）。
- [x] `internal/app/ops_app.go` 用 `CompositeNotifier` 把 SSE 事件总线与 webhook/Telegram 并行派发，设置每次派发实时读取。

**验收**：故意触发一次 `runtime_sync_failed`，Telegram/Webhook 收到消息，面板内告警仍正常。

### P0-2 订阅链接只读复制（不轮换、不踢人）

**现状**：token 只存 hash（`internal/service/user/store.go` 的 `TokenHash`），明文不可恢复；前端 `users.ts` 只有 `rotateCredential`。想重发链接 = 轮换 = 对方掉线。

**改动**
- [x] `storedCredential` 增加 `TokenCiphertext`，复用 `user-credential-aes-256` 密钥加密，与 hash 并存。
- [x] DB 迁移：`credentials` 表新增 `token_ciphertext` 列。
- [x] 新增 `GET /api/v1/users/:id/subscription` 只读接口，返回 token + 4 种订阅 URL。
- [x] 前端用户行加「复制订阅链接」按钮，弹可复制的 URL（非一次性）。

**验收**：创建用户后任意时刻可拿到原 token，旧客户端不受影响；旧数据（无密文 token）提示"请先轮换一次"。

## P1 —— 第二批

### P1-3 收敛 Clash API 配置 + 默认可用

**现状**：配置散在 3 处 —— `config.yaml` 的 `singbox.clash_api_url`、`traffic.clash_api_url` / `v2ray_api_address`、前端 `SingBoxPage.tsx` 基础配置表单。

- [x] 确认运行时唯一事实来源（以面板管理的 `singbox_config` 为准，`config.yaml` 仅作可选覆盖），流量采集与实时连接共用同一地址与 secret。
- [x] Clash API 默认开启（`127.0.0.1:9090`）并在启动时自动生成随机 secret（面板「基础配置」留空保持原值）。
- [x] 概览页「实时连接不可用」提示加直达配置的跳转。

### P1-4 流量采集自检 + 采集失败告警

**现状**：采集依赖 Clash API，失效是静默的；额度阻断粒度 ~1 分钟滞后。

- [x] 采集器暴露「最后成功采集时间」（`traffic.Service.RecordCollection`/`LastCollectedAt`）到 readiness（`trafficCollection` 检查）；Clash 断流时采集循环报错（不再以冻结计数继续上报），采集器每次成功周期记录一次。
- [x] `alerts.go` 增加 `traffic_stalled` 告警类型（warning，setup 完成前不判定），采集恢复后自动解决。
- [x] 概览页显示「最近采集时间」；`traffic_stalled` 告警会随 P0-1 外发到 Webhook/Telegram。

### P1-5 路由规则概念澄清（服务器侧 vs 订阅侧）

**现状**：两套规则（服务端路由 / 订阅规则策略），边界靠 `UsersPage.tsx` 一句 form extra 兜底。

- [x] 文案统一：服务端路由 →「服务器分流」（服务端配置页 tab），订阅规则 →「订阅分流」（页面标题 + 导航 + 用户表单），各加一句固定说明（作用于谁、不作用于谁）。
- [x] 两页互加「前往订阅分流 / 前往服务器分流」跳转，并支持 `/singbox?tab=routes` 深链直达服务器分流 tab。

## P2 —— 锦上添花

### P2-6 订阅方导入引导 + 二维码

- [x] 凭据/复制链接弹窗为每种格式加导入提示 + 二维码（antd 内置 QRCode，无新依赖；每行可展开扫码）。

### P2-7 流量数据保留策略

- [x] 设置项 `traffic.retentionDays`（默认 90 天，0 为永久保留），后台每 6 小时清理 `traffic_hourly` / `traffic_daily` / `traffic_batches`（采集批次此前无清理、每天约 1440 行，一并老化）。
- [x] DB 容量告警文案从"迁 PG"改为"缩短流量保留期或清理历史数据"。

### P2-8 个人场景复杂度税（UI 层收敛，可选）

- [x] 移除 RBAC/多账号（「账号权限」页与角色/权限体系已删除）。
- [x] 审计日志与系统设置折叠进主导航底部「系统」分组，高频操作区只保留 7 项。

---

## 技术债 —— 后端架构评审（2026-08）

> 结论：分层纪律、依赖方向、事务边界、outbox/事件回放设计均健康，不动骨架；以下按影响排序。
> 编号 A 系列，与上文产品项区分。

### A 系列立即可做（本轮已完成的勾选）

- [x] **A1 Recovery 中间件 + gin Release 模式**：`gin.New()` 无 Recovery，任一 handler panic 会杀死同进程的 sing-box 内核（`ops_app.go`）；且全仓未设 ReleaseMode，生产跑 debug 模式。新增 `httpx.RecoveryMiddleware()` 返回标准错误信封（500 `INTERNAL`）并记录栈。
- [x] **A2 优雅停机 join 后台 goroutine**：`OpsApp.Close()` cancel 后立即 `db.Close()`，运维循环与流量采集 goroutine 可能正对已关闭的库执行 SQL。增加 WaitGroup + 5 秒有界等待。
- [x] **A3 内核健康探测 + 自愈**：`Manager.Status()` 只要 `instance != nil` 即报 running，内核静默死亡面板仍显示正常。在每分钟运维循环里对面板管理的 Clash API 拨号（`GET /version`），running 但探测失败 → 自动 `Reconcile`。
- [x] **A4 回滚盲区：close 旧内核失败不再放弃**：`Manager.Apply` 中 `closeLocked` 出错直接返回、内核停在 stopped；改为继续启动新配置，失败再回滚旧配置。
- [x] **A5 审计写入失败留痕**（本轮完成「失败记日志」部分）：`router.go` 的审计写入原为 `_ = AppendAuditLog` 静默吞错，现失败时以 slog 记录 action/resource/requestId。**遗留**：`audit_logs` 表 identity 写 / audit 读两套模型靠复制维持，写所有权收敛待做。
- [x] **A6 sessions 清理 + reload 分类修正**：新增 `identity.Store.DeleteStaleSessions`（过期或已撤销），并入 6 小时 housekeeping；`panel.session_ttl` 与 `development.frontend_dev_proxy` 的 reload 分类从 Immediate 修正为 RestartRequired（两者均构造时固化）。
- [x] **A7 outbox / 事件表清理**：新增 `observability.PurgeNotifications`（delivered/终态 failed 超过 7 天清理，重试中的行不动），并入 6 小时 housekeeping；`system_events` 的保留清理从 Publish 热路径移到每分钟运维循环（`Bus.Retention`）。
- [x] **A8 采集维度剪枝 + 游标重置**（前提修正：入库失败**不丢**区间流量——累计计数 + 游标差分，下次成功批次自动补差）。真正的问题：用户/协议删除后采集 map 旧 key 永久保留，且批次带上已删除维度会被 `validateDimensions` 拒绝、采集持续失败直至重启。现连续 3 个周期未出现在内核响应的维度从累计 map 剪枝，并同步删除其游标（防止同 ID 重建触发计数回退守卫）。

### A 系列结构性重构（本轮已完成）

- [x] **A9 写 handler 模板收敛**：新增 `handler.mutate` 统一「失败记审计并写错误 / 成功记审计并同步内嵌内核」的骨架，约 30 处写 handler 改为经此收尾；`synchronize` 不再靠约定散落手工调用。顺带补齐了 deletePolicy/rule 与 clone/rotate 等此前只记成功、漏记失败审计的不一致。
- [x] **A10 N+1 查询收敛**：user/protocol/policy/certificate 的 List 由「查 id 列再逐条 Get」改为单条批量主查询（user 另以两条 IN 查询批量加载引用），并提取 scan 辅助函数复用字段解码；protocol 的解密延后到 rows 关闭后，规避单连接 sqlite 死锁。
- [x] **A11 mapError×5 + 工具函数收敛**：`common.MapSQLiteError` 用 sqlite3 扩展错误码（唯一/PK→ErrDuplicate、外键→ErrReferenced）替代错误文本匹配；`FormatTime`/`ParseTime`/`Scanner` 收敛各 store 重复工具；protocol/policy 保留各自特定唯一索引的语义化映射。
- [x] **A12 compiler.go 拆分**：Clash 客户端订阅生成（约 370 行）拆到 `subscription.go`，服务端 sb.json 编译保留在 `compiler.go`，共享 Input 与 Compile 入口。

### A 系列待办（A13、A15 起）

- [ ] **A13 secret 主密钥外部化（可选）**：主密钥明文与密文同库（`secret/store.go`），对整库被拿走零防护、无版本化。支持可选 env/文件注入外部 master key。
- [x] **A14 死代码清理**：删除 `internal/http/response.go` 数字错误信封（静态资源/开发代理改走 `httpx.Error` 统一契约）；删除从未读写的 `resource_revisions` 死表（schema 移除建表 + 迁移幂等 DROP）。
- [ ] **A15 测试盲区**：v1 handler 层（users/traffic/overview/events）零测试；后台任务（留存/续期/停机）与 reload 分类无测试。
- [x] **A16 其余小项**：
  - singbox handler `err.Error()` 原文外泄 + 409 滥用 → 运行时操作失败改用通用文案（完整错误进 slog/audit），状态码纠正为 500（check 用 `CheckResult.Message` 诊断信息 + 422）；`synchronize` 的 `SINGBOX_SYNC_FAILED` 保留 409 与 code（前端依赖「已保存未应用」语义）但 message 改通用。
  - `writeTrafficError` 一刀切 400 → 新增 `traffic.ErrInvalidRequest` 哨兵：参数错误 400、内部错误 500（不透传 err.Error()）。
  - `alerts` resourceID 恒为 '1' → 改为 `'runtime'`。
  - resource schema 对 `accounts` 的外键依赖 → 已被 A14 顺带解决（删除 `resource_revisions` 后 resource schema 不再引用 accounts）。
  - **iptables 端口跳跃无事务性（评估后暂不做）**：`reconcilePortHop` 是 best-effort（失败仅记日志、不影响 sync），且每次 sync 全量重建规则可自愈半配置状态；真正原子化需 `iptables-restore` 整表替换，风险高于收益，且无 CI 测试环境。

## 执行顺序

1. 第一批（P0-1 + P0-2）
2. 第二批（P1-3 + P1-4）
3. 第三批（P1-5 + P2-6）
4. 架构评审第一批（A1–A4）/ 第二批（A5–A8）/ 重构批（A9–A12），均已完成
