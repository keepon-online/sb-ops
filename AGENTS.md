# AGENTS.md

面向未来 ZCode agent 的仓库速览。改代码前先读 [README.md](README.md)、`docs/backlog.md`；改敏感区域（数据库迁移、认证、订阅产物、Clash/V2Ray 收敛）前读 `docs/api/openapi.management.yaml` 与对应包代码。

## 项目概览

- 单 Go 二进制（根 `main.go` 扁平入口）+ React/antd 前端（`frontend`），内嵌 sing-box 内核；单 VPS 单进程部署（systemd + Nginx 反代方案与已验证实例见 `docs/deployment.md`，仓库内不携带编排文件）。
- 个人使用、**单管理员**（无 RBAC/多账号，`internal/identity` 只做认证与会话）。
- 产品语言为中文（面板 UI、资源层校验/错误消息已中文化）。**统一响应信封**：管理 API 的 JSON 响应一律 `{code, message, data, requestId}`（校验失败附加 `fieldErrors`），`code` 是数字业务码——`0` 成功，`1xxx` 通用 / `2xxx` 认证账号 / `3xxx` 资源域 / `4xxx` 面板升级 / `5xxx` sing-box，注册表在 `internal/httpx/codes.go`（数值是契约，只增不改），前端对应表在 `services/http.ts` 的 `ApiCode`；HTTP 状态码保留 REST 语义，信封之外再无业务信息。订阅产物（clash.yaml）、SSE、健康检查、204 无体响应不走信封。请求体绑定统一走 `httpx.BindJSON`（`internal/httpx/bind.go`）：binding tag 校验失败自动附带字段级中文 `fieldErrors`（键为 JSON 字段名，中文模板集中在该文件），新 handler 不要再手写 `ShouldBindJSON`；成功响应用 `httpx.OK`/`httpx.Created`，勿用裸 `httpx.JSON`（保留给健康检查等非信封契约）。

## 常用命令（Go 命令必须带 build tags）

```bash
make build            # 前端构建 + go build → dist/sb-ops
make test             # go test -tags=with_quic,with_utls,with_clash_api,with_v2ray_api,with_wireguard,with_gvisor ./...
make vet              # 同上 vet
make frontend-check   # redocly lint openapi + tsc --noEmit + vite build（前端门禁）
make api              # openapi-typescript 重新生成 frontend/src/generated/management-api.ts
npm --prefix frontend test   # vitest 前端单测
go test -tags=with_quic,with_utls,with_clash_api,with_v2ray_api,with_wireguard,with_gvisor ./internal/<pkg>/...
```

## 架构与分层

- `internal/app`：装配（`newOps`）；`api/`：管理 API + 订阅端点（handler 层，一域一文件）；`internal/web`：前端静态资源 embed 与健康检查路由；`internal/httpx`：统一错误契约（`requestId`/`code`）与中间件。
- `internal/service/*`：业务服务（user / protocol / certificate / policy / singbox / share / secret / argo / upgrade / common），领域包内 `model.go`/`store.go`/`service.go`。
- `internal/traffic`：V2Ray 采集、历史聚合、保留清理；`internal/observability`：告警评估、readiness、设备平台画像、Webhook/Telegram 外发。
- `internal/deployment/compiler` + `singlevps`：资源 → `sb.json` 编译、同步与回滚；`internal/runtime/singbox`：内核生命周期。**内核日志统一进面板日志**——`kernelLogWriter` 经 sing-box 的 `log.PlatformWriter` 转发进 `internal/logging` 门面（剥 `INFO[  12]` 前缀与 ANSI 色码，级别映射 debug/info/warn/error），内核自身输出重定向 `/dev/null`；不要让内核直接写 stderr（硬编码 `+0800 …` 时间戳格式）。
- `internal/database`：SQLite 打开与迁移。
- 分层规则：HTTP handler 调 service，service 调 store；store 持有 `*sql.DB`。**写操作收尾统一走 `h.mutate`**（审计 + 内核同步一体，勿绕开）；更新类 handler 先取改前快照再传 `diffable(beforeErr, err, before, value)`，成功审计的 details 会带字段级 `{old,new}` diff（`resourceChanges` 自动忽略 revision/updatedAt；出站快照走脱敏视图）——新增更新端点照此接，别再退回只留 `{}`。审计读写统一在 `internal/audit`（Service 门面 + Store；留存走 `audit.retention_days`，0=永久），identity 只管账号与会话。

## 领域约定（勿破坏）

- **启动配置统一集中在 `internal/config`**：无环境变量、无命令行 flag、无面板运行时设置（`internal/settings` 已删除），`main.go` 固定读取工作目录下的 `config.yaml`，不要重新引入 `SB_*` 环境变量、`--config` flag 或 settings 表。所有 config.yaml 键 → `RuntimeConfig` 强类型字段（时长字段直接是 `time.Duration`，viper 的 mapstructure hook 从 "45s" 字符串解码；非法值让加载失败而不是静默回退）；默认值**只存在于 `DefaultRuntimeConfig()`**，不要重新引入 `viper.SetDefault` 点号路径副本或各消费方自己的回退默认。要热生效的运行时设置（Clash/V2Ray、分流规则等）归数据库面板管理，勿搬进 config.yaml。
- **部署参数在 `config.yaml` 的 `deployment` 段**（`public_address` / `panel_domain` / `tls_cert_path` / `tls_key_path`），由 `ApplyConfig` → `compiler.LoadInput` 传入；`panel_domain` 供订阅自环保护钉 DIRECT，**勿在编译器里硬编码任何具体域名**（曾因此把个人域名带进所有部署的订阅）。

- **Clash/V2Ray API 收敛**：地址与密钥由面板「服务端配置 → 基础配置」`singbox_config` 管理（**单一事实来源**）；不要在 config.yaml 引入 clash/v2ray 地址配置。
- **真实 `config.yaml` 不入库**（已在 `.gitignore`，含个人部署信息）：仓库只携带脱敏样例 `config.example.yaml`（复制为工作目录 `config.yaml` 后按环境修改）；样例只保留与默认值不同的键（server.listen / server.db_path / deployment.public_address / development.allow_private_address / logging 段）；其余键的默认值见 `internal/config/runtime.go` 的 `DefaultRuntimeConfig`。可选段（缺省即禁用）：`notifications` / `acme` / `updates`（面板自升级）。`cors_allowed_origins` 已删除（无 CORS 中间件消费，勿恢复）。
- **日志配置在 `config.yaml` 的 `logging` 段**（`level`：debug/info/warn/error，同时作用于双 sink；`file_format`：`text`（默认，无颜色可读文本）/`json`；`rotate_max_mb`/`rotate_backups`：文件轮转参数）：`internal/logging`（gookit/slog 流式门面）双 sink——stdout 彩色 text + `<data_dir>/logs/sb-control.log` 尺寸轮转（0600）；改动需重启生效。注意 gookit 级别数值**越小越严重**（Panic=100…Trace=800），过滤方向别写反。
- **流量采集走 V2Ray stats gRPC**（user-tag 按设备归因、经 deviceID→userID 映射汇总到用户并驱动额度扣减，额度单位 GB）；**Clash API 仅实时连接观测**（`/connections`）。采集地址每次周期动态解析（resolver），采集失败触发 `traffic_stalled` 告警。**sing-box v1.13 `/connections` 字段契约**（与 mihomo 不同，改 `singlevps/connections.go` 前必看）：端口在 `metadata.destinationPort`（不是 mihomo 的 `dstPort`）、命中规则在连接**顶层** `rule`（不在 metadata）、入站无独立字段（打包在 `metadata.type` 为 `"<类型>/<tag>"`）；`host` 取 `Metadata.Domain` 回退 `Destination.Fqdn`——客户端代理协议带域名即有值，**不需要 sniff 规则**。测试夹具要按真实载荷写键名，别照解析器写（曾因此假绿）。
- **凭据按「设备」维度**：`devices` 表——每设备独立 uuid/password/token，入站用户 name = 设备 id（即 V2Ray stats 标签）；`UNIQUE(user_id, name)`、上限 10 台/用户，设备**只**由用户级订阅链接自动发现（v0.2.0 已移除手动添加的前后端入口与 API；建号不建设备，用户级订阅 token 存 `users.subscription_token_*`，可轮换）；`ua_key`（部分唯一索引）非空的设备为自动发现设备，指纹优先级 = **ETag 摘要锚定**（客户端带 If-None-Match 时按订阅摘要直接认领既有设备，解决同款客户端多设备漫游误建）→ 回退 **UA + 来源 IP**（`share.ParseUserAgent` 识别客户端，同 UA 不同公网出口建独立设备；浏览器/curl/wget 样式的 UA **可匹配既有设备但不自动新建**（`UAInfo.AutoCreate=false`，走 `MatchDeviceForKey`——用户可能把客户端 UA 手动改成 Mozilla 样式长期使用，不能断其路由），自动设备名带来源后缀如 `Clash Verge · 203.0.113.10`；首次下载自动建设备并触发一次同步，失败回退默认设备）。原 `credentials` 表已删（未上线直接重建，无迁移数据，`runtime_artifacts` PK 为 user+device+format）。**平台画像**（`observability/deviceprofile.go`：每 2 分钟按实时连接的目标域名推断 unknown 平台，≥2 个不同信号域名才判定）依赖客户端系统流量进隧道——**系统代理模式下 NCSI/WindowsUpdate 不走 WinINET 代理，windows 信号永远观察不到（客户端需 TUN 模式）**；画像按**网络前缀归因**（IPv4 /24、IPv6 /48，`networkKey`）——家宽动态出口在前缀内跳动，设备记录的 `source_ip` 与实时连接源地址精确相等几乎不可达，同前缀多设备共享画像（比同 NAT 共享略宽，接受的取舍，勿改回精确匹配）；识别只填 unknown、永不覆盖 UA 识别值；`source_ip` 被自环污染的存量设备在**直连**拉取订阅时原地恢复（经隧道自环拉取按未观测处理）。
- **流量统计行模型（互斥）**：`traffic_hourly/daily` 写入两种行——设备行（`user_id≠''`、`device_id≠''`、`protocol_id=''`）与入站行（`user_id=''`、`device_id=''`、`protocol_id≠''`）。V2Ray user 统计天生无协议归属，因此「用户排行 × 协议过滤」（或反之）恒为空集：前端 TrafficPage 已做维度互斥（切维度清空+禁用另一侧过滤），后端 `ActiveUsers`/`ProtocolShares` 会忽略不适用的过滤——改查询时保持此语义。`AggregateRecent` 的聚合窗口**含当天进行中数据**（end=now 滚动重聚合），勿改回截止当天 00:00（会导致跨天查询缺当天）。
- **两套分流勿混淆**：「订阅分流」（`PoliciesPage`，写入 Clash 订阅、只影响单用户客户端；策略「默认动作」= 订阅里 `MATCH` 兜底，改 direct 会让未识别境外域名直连）vs「服务器分流」（`SingBoxPage` routes tab，作用于内核转发、影响所有用户）。服务器分流的**出站引用有全链路校验**：规则写入校验存在性，删除/改名被引用出站会被拦（路由规则、默认出站、DNS detour、规则集 `download_detour` 四类引用），编译期 `Input.Validate` 兜底——因为**悬空出站引用 sing-box check 不报错、内核照常启动、命中规则的连接拨号秒断**（`router: outbound not found`），别指望内核校验兜底。
- 告警外发配置在 `config.yaml` 的 `notifications` 段（`webhook_url` / `webhook_secret` / `telegram_bot_token` / `telegram_chat_id`，及国内文本渠道 `feishu_webhook_url`/`feishu_secret` / `dingtalk_webhook_url`/`dingtalk_secret` / `wecom_webhook_url`），未配置静默跳过；notifier 通过 `func() config.RuntimeConfig` 实时读最新配置（改 config.yaml 热重载生效）。国内渠道（`internal/observability/domestic.go`）**卡片优先、文本自动兜底**：飞书发 `interactive` 卡片（头部模板色随级别红/黄/蓝）、钉钉发 `markdown`、企业微信发 `markdown`，卡片展示级别/类型/消息/状态/时间；payload 无可展示内容（如空载荷）时降级 `formatNotificationText` 纯文本。飞书/钉钉开启签名时用标准库 `crypto/hmac` 生成时间戳签名拼进 URL；各 notifier（事件总线 / 通用 webhook / Telegram / 飞书 / 钉钉 / 企业微信）并行 fan-out——单个渠道失败不阻塞其它，重试与退避统一由 outbox 负责（notifier 内不加重试），重复投递对告警可接受。机器人 URL 与密钥属敏感配置，不写入日志或错误响应；**不做备份文件推送**（聊天附件仅限文本摘要）。
- **协议端到端拨测**（`internal/probe`）：监控第二档——「端口在听」之外验证「链路真的能通」。`Prober` 为每个协议起一个**一次性轻量 sing-box 客户端实例**（本地 mixed 入站 + 该协议出站 + inbound 路由规则，独立 box、不配 experimental/cache-file/DNS、不接 PlatformLogWriter），HTTP 请求经环回穿真实入站再由主内核出站打 `gstatic generate_204`。关键约定：**客户端出站镜像服务端 inbound**（`compiler.go` 的 builder），不是订阅方言（如 VMess 订阅恒不加密但服务端 certificate 模式必须 TLS）；**凭据借用**——按协议从「已分配该协议的可用用户」里取第一台启用设备（借用设备的微小拨测流量归因是接受的行为），无分配用户则写 `skipped` 行不告警；**内核未 running/配置未 applied 时整轮跳过且不落库**（防同步窗口误报，内核故障已有 `runtime_sync_failed`）；失败行驱动 `protocol_probe_failed`（critical）告警，行翻绿自动恢复。周期 5min/单协议超时 10s 是**常量硬编码**（照 `trafficStallThreshold` 先例，勿进 config.yaml）。`probe_results` 表的 DDL 归 `observability/schema.go`（`ProbeSchemaVersion=13`）而非 probe 包——probe 依赖 service 层模型，DDL 若留在 probe 会让 `database → probe → user` 与用户/编译器测试成 import 环。
- **VPS 月流量计量走提供商口径**（`internal/solusvm`，`config.yaml` 可选段 `solusvm`：endpoint/api_key/api_hash 三项同进同出，RackNerd 的 nerdvm 即 SolusVM 1）：每小时轮询 client API `action=info&bw=true`，**账单口径**（含全部网卡流量与宿主层开销）与 V2Ray stats 隧道口径刻意分离（overview 的 `bandwidth` vs `traffic24h`）。契约实测（2026-09）：响应是拼接的 XML 风格标签非标准 XML；`<bw>` 四元组**字节单位**、顺序 总量,已用,剩余,百分比（剩余=总量-已用 精确闭合）；`info` 属官方文档外 action（文档只承诺开关机），提供商可随时禁用——故 `vps_bandwidth_stalled`（持续 6h 无成功采样）告警兜底、解析要宽容。凭据 per-VPS 且**同时具备重启该 VPS 的权限**：只入 config.yaml（0600、不入库），endpoint 强制 HTTPS、凭据只经 POST 表单提交（不进访问日志）。阈值 80%/95%、轮询 1h、停滞 6h 均常量硬编码（勿进 config.yaml）。
- **面板自升级**：`config.yaml` 的 `updates` 段（`repo`/`token`，空即禁用）+ `internal/service/upgrade`（无状态服务，升级源经 `Source` 闭包注入、不 import internal/config，业务码 4xxx）。升级流程 = 下载 Release 资产 `sb-ops-linux-<GOARCH>` → sha256 校验（GitHub 资产 digest 优先、SHA256SUMS 回退）→ 备份 `.bak` → 原子 rename → `restarter` 钩子（main.go 置标志 + 自发 SIGTERM）走**既有优雅停机链**后 `syscall.Exec` 重生为新二进制；协议入站由启动时 `syncer.Start` 自动恢复。新版本起不来**不做自动回滚**——`.bak` 留在二进制旁供 SSH 手动换回，勿引入双进程看门狗之类的复杂化。**新版本自动发现**：ops_app 的 `runUpgradeCheckLoop` 每 6h（启动 2min 后首查）调 `Check` 把结果写进升级服务缓存（系统页展示同一份，页面挂载时缓存为空会自动补查），有可升级版本时经 `upgrade_available`（warning）告警外发、升级完成自动恢复；节奏是常量硬编码（勿进 config.yaml），检查失败只记日志不告警（手动检查仍可用）。注意面板重启会清内存缓存：有待升级告警时重启会先 resolve、2min 后重查再 open（重复一次外发是接受的行为）。
- **数据库一键备份**（`internal/service/backup`，`api/backups.go`）：`VACUUM INTO` 在线快照（WAL 安全，mattn v1.14.22 内嵌 SQLite 3.45 支持参数绑定）落 `<data_dir>/backups/`，目录 0700、文件 0600——**备份含 control_plane_secrets 主密钥与订阅 token**。**不走 `h.mutate`**（它成功后必然重编译并重启内核）：照 upgrade 先例 `h.audit` 直记（`system.backup.create/download/delete`，下载属敏感数据外发也记审计）；服务经 `dir func() string` 闭包注入 data_dir、不 import internal/config。保留 20 份（`maxBackups` 常量硬编码，勿进 config.yaml），超出裁最旧；文件名 `sb-ops-backup-YYYYMMDD-HHMMSS.db` 过严格正则（读取类操作先校验，杜绝路径穿越）。下载是二进制流（信封豁免）+ **前端 blob 下载**（认证只认 Bearer 头无 cookie，直链/URL token 都带不了或会泄进访问日志——blob 是仓库首个此模式，新下载需求照抄）。**定时备份**：每日 04:00 本地时区（`ScheduleHour` 常量）由 ops_app 的 `runBackupScheduleLoop` 复用 `Create`，失败经 `backup_failed` 告警；`ScheduleState` 纯内存，重启清零——告警评估在重启后首轮之前按 `StaleSnapshot()` 磁盘事实兜底（最新快照文件名时间戳超过 `SnapshotStaleAfter`=25h 未更新即产生候选，防告警被重启洗白、顺带兜住停机跨 04:00 漏跑；全新部署无快照不评估）；`Create` 失败路径必须清理 target（VACUUM INTO 中途失败 SQLite 不保证删目标文件，半成品快照会污染备份列表）。

## 数据库 / 迁移

- SQLite（mattn/go-sqlite3），DSN 固定 `busy_timeout/foreign_keys/WAL`；打开后 `SetMaxOpenConns(1)`。
- 迁移集中在 `internal/database/migrate.go`；schema 常量在各自包（`service/schema.go`、`traffic/schema.go`、`observability/schema.go`、`audit/schema.go`、`events/...`）。**schema_versions 的 version 是全局编号**：新迁移取全局下一个空闲值（勿与其它包已记录的号共用——条件迁移判定会恒真导致迁移体永不执行，audit=1 撞 identity=1 即此坑）；改最大版本号时 `CurrentSchemaVersion` 必须同步（readiness 按 `MAX(version)==expected` 严格比较）。
- 新增列/表用幂等 `ALTER TABLE` / `CREATE TABLE IF NOT EXISTS`，并在 `database_test.go` 补用例（含升级路径）。

## 契约与生成物

- API 契约 `docs/api/openapi.management.yaml`；改 API 必须同步契约，跑 `make api` 重生成 `frontend/src/generated/management-api.ts`，再 `make frontend-check`。
- 前端构建产物同步到 `internal/web/embedded/`（`index.html` 已跟踪、`assets/` 目录 gitignore）；前端改动后 `index.html` 的 asset hash 变更属正常，需一并提交。
- **订阅产物只有 `clash.yaml` 一种格式**（`runtime_artifacts` 落库硬编码）；前端订阅链接也只展示它。`sbox.json`/`clmi.yaml`/`jhsub.txt` 曾是 404 死链已删——除非后端真的实现生成器，不要再加回前端格式列表。
- **已删除勿恢复**：`config/validation.go`（agent 时代自由 sb.json 校验，仅测试引用）、流量归档/清理 API（`Archive`/`Purge`/`EstimateBefore` + `/traffic/archive|purge` 路由；`PurgeBefore` 保留供留存任务）、协议类型 `naive`/`shadowtls`（sing-box 独有协议，mihomo/clash 不支持、永远进不了 clash.yaml 订阅，2026-08 移除；保留 `anytls`——mihomo 支持）、流量/审计 CSV 导出（曾列 v0.4.0 路线，2026-09 评估价值不大明确不做，勿再提）。

## 前端视觉、交互与主题规范（勿破坏）

- **主题色与多套预设自适应**：面板支持 4 套主题色（`blue` 天蓝、`purple` 紫罗兰、`green` 翡翠绿、`orange` 日落橙）及深浅双模式（`dark` / `light`）。
  - **严禁硬编码单一色彩（如 `#0284c7`、`#1677ff`、`#1890ff`、`color="blue"`、`color="geekblue"`）**：Ant Design 强调态标签统一用语义化 `color="processing"`（自动映射当前主题 `colorPrimary`），成功用 `color="success"`、警告用 `color="warning"`、错误用 `color="error"`、停用用 `color="default"`；不要在组件里写死固定蓝色。
  - **渐变终点必须消费 `--primary-accent`**：双色渐变（品牌 Logo、登录页标题、总览核心指标）统一写 `linear-gradient(135deg, var(--primary-color), var(--primary-accent))`；`--primary-accent` 由 `ThemeContext` 随主题色动态匹配同系副色，严禁在 CSS 里将渐变终点硬编码为浅紫（`#a78bfa`）或其它固定色（会导致绿色/橙色主题严重撞色发灰）。
  - **语义状态消费 CSS 变量**：报警/错误与成功色统一消费 `var(--color-error)` / `var(--color-warning)` / `var(--color-success)`，不要在 inline style 里写死 `#cf1322` / `#389e0d`（在暗色模式 `#131b2e` 底色下对比度不足）。
- **双通道图表与图例文案规范**：
  - 流量与网络速率趋势图（`InteractiveChart`）上行线消费 `var(--primary-color)`，下行线消费 `var(--traffic-down)`（在 `green` 绿色主题下自动动态映射为高对比天蓝 `#38bdf8`，其他主题下为 `#42a56f`，避免双绿混淆）。
  - 图例与辅助说明统一使用语义化符号（如 `<LineChartOutlined /> 上行 (↑) · 下行 (↓)`），**严禁在界面文案中硬编码颜色名称**（曾硬编码「蓝线上行，绿线下行」，在绿/紫/橙主题下与实际线条颜色冲突）。
- **字体与排版规范**：
  - 全局西文字体优先 `Plus Jakarta Sans`，代码/密钥/IP/Diff 对账一律使用等宽字体 `'JetBrains Mono', ui-monospace, monospace`。
  - 字体栈必须保留系统级中文字体回退（`'PingFang SC', 'Hiragino Sans GB', 'Microsoft YaHei', sans-serif`），保证 Windows 与 macOS 汉字字重渲染均一。
- **交互与高频操作减阻**：
  - 用户订阅复制走轻量一步直达（优先从 React Query 缓存读取设备 Token，缺省异步静默加载，带有即时反馈提示，严禁强迫用户展开设备抽屉后再手工查找复制）。
  - 协议端到端拨测支持单行就地触发（复用 `POST /api/v1/protocols/probe?protocolId=...`）。
  - 审计日志变更记录必须展示字段级 `{old, new}` 结构化 Diff 对账抽屉（红底旧值、绿底新值、等宽显示）。
- **响应式与移动端体验**：
  - 表格水平滚动容器必须声明 `-webkit-overflow-scrolling: touch;`，消除移动端滑动手势卡顿。
  - 抽屉（Drawer）在平板/手机（`max-width: 768px`）下必须占满屏幕宽度（`width: 100% !important; max-width: 100vw;`），避免抽屉内表单拥挤截断。
  - 操作工具栏（`.page-toolbar`）小屏下必须弹性换行或纵向堆叠，输入框与选择器自动充满宽度。
- **单测安全兜底**：
  - `useAppTheme()` 必须保留未被 `<ThemeProvider>` 包裹时的默认降级返回对象，避免孤立单元测试渲染页面组件时因缺少 Context 抛出异常。测试断言若包含精确 `ariaLabel`（如 `monitor.test.tsx`），页面中的可访问性标签需严格对齐测试契约。

## 测试注意

- 全部 Go 测试需 build tags。
- **import 环**：`internal/database` 导入 `observability`（migrate 应用其 schema）→ `observability` / `traffic` 的测试**不要 import internal/database**，用本包 `Schema` + `internal/service.Schema` 直接建表。
- 业务测试构造走 `sql.Open("sqlite3", ":memory:")` + `SetMaxOpenConns(1)` + `database.Migrate`（包内不冲突时）。

## CI / lint

- `golangci-lint` 用 **v2 配置**（`.golangci.yml`，`run.build-tags` 已配齐六 tags，与 Makefile 一致）；CI 锁 v2.4.0。`~/go/bin/golangci-lint` 若是 v1 会拒绝 v2 配置，装 v2.4.0 对齐验证。
- **ST1005**：错误消息不能以大写拉丁字母开头——中文消息以 "Clash API…"/"Cloudflare…" 开头会挂，写成中文开头（如「监听地址无效（Clash API）」）。
- CI 还有 `gofmt -l ./*.go ./api ./internal` 检查；提交前跑一下。

## 运维 / 排障 gotchas

- **Reality 伪装站证书链上限 8192 字节**（metacubex/utls `realitySize`）：`www.microsoft.com` 等已部署后量子（MLKEM）证书的站点链超限，认证成功后握手必失败（客户端表现为「超时/EOF」）。伪装站选证书链小的（`www.apple.com`/`gateway.icloud.com` 实测 ≈4KB 内）。快速自检：`openssl s_client -connect 127.0.0.1:<port> -servername <sni>` 回真实证书 = 服务端 Reality 活着（认证前的 fallback 转发）。
- **协议端到端自测**（不装客户端）：用面板同版本内核跑官方 CLI —— `GOFLAGS=-tags=with_quic,with_utls go run github.com/sagernet/sing-box/cmd/sing-box@v1.13.19 run -c client.json`（本地 mixed 入站 + 目标出站），再 `curl -x http://127.0.0.1:2080 https://www.gstatic.com/generate_204`。
- **本地 WSL 开发**：后端进程需带 `https_proxy`（规则集/ACME 要访问 GitHub）且 `no_proxy` 覆盖本地端口；协议入站端口 <1024 需 root/setcap，本地用 ≥1024；`networkingMode=mirrored` 可解 Windows→WSL 的 UDP（hy2/tuic）转发问题。
- `traffic_stalled` 告警与「流量采集循环超过阈值未成功完成」错误日志测量的是**采集循环存活**（零流量的一轮查询也算心跳，见 `local_collector.go` 的 `RecordCollection`），并按 Clash API 活跃连接数区分空闲与故障（`ops_app.go` 的 `trafficStallError`）：无活跃连接（含 Clash API 不可观测）按空闲处理，不打错误日志、不告警、不降 readiness；有活跃连接但无采集心跳才是采集链路真的断了，根因看同期的「本地流量采集失败」日志。
- 诊断技巧：`control_plane_secrets` 表内存有各 AES 主密钥，可本地解密 `protocols.settings_json` 核对参数；面板 API 会脱敏私钥字段。流量/域名审计：内核 INFO 日志逐连接记域名（journalctl 搜 `outbound connection to`）、`inbound connection from` 是来源 IP；订阅拉取来源看 nginx access log 的 `/subscribe` 行（直连 = 客户端真实出口，VPS 自身公网地址 = 经隧道自环）。手工 sqlite3 查询列名写错只向 stderr 报 `no such column`，在多命令脚本里易被误读成「表为空」——先 `.schema` 再查。

## 提交约定

- 会话产物 `.zcode/plans/` 保持 untracked，不要提交。
- 提交按功能拆、用 conventional commit（`feat(scope):` / `fix` / `refactor` / `docs`）；文档改动（README/CHANGELOG/backlog）随功能或独立提交。
