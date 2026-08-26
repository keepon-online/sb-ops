# AGENTS.md

面向未来 ZCode agent 的仓库速览。改代码前先读 [README.md](README.md)、`docs/backlog.md`；改敏感区域（数据库迁移、认证、订阅产物、Clash/V2Ray 收敛）前读 `docs/api/openapi.management.yaml` 与对应包代码。

## 项目概览

- 单 Go 二进制（根 `main.go`，go-site 式扁平入口）+ React/antd 前端（`frontend`），内嵌 sing-box 内核；单 VPS 单进程，无独立 Docker/systemd 编排。
- 个人使用、**单管理员**（无 RBAC/多账号，`internal/identity` 只做认证与会话）。
- 产品语言为中文（面板 UI、资源层校验/错误消息已中文化）。**统一响应信封**：管理 API 的 JSON 响应一律 `{code, message, data, requestId}`（校验失败附加 `fieldErrors`），`code` 是数字业务码——`0` 成功，`1xxx` 通用 / `2xxx` 认证账号 / `3xxx` 资源域 / `5xxx` sing-box，注册表在 `internal/httpx/codes.go`（数值是契约，只增不改），前端对应表在 `services/http.ts` 的 `ApiCode`；HTTP 状态码保留 REST 语义，信封之外再无业务信息。订阅产物（clash.yaml）、SSE、健康检查、204 无体响应不走信封。请求体绑定统一走 `httpx.BindJSON`（`internal/httpx/bind.go`）：binding tag 校验失败自动附带字段级中文 `fieldErrors`（键为 JSON 字段名，中文模板集中在该文件），新 handler 不要再手写 `ShouldBindJSON`；成功响应用 `httpx.OK`/`httpx.Created`，勿用裸 `httpx.JSON`（保留给健康检查等非信封契约）。

## 常用命令（Go 命令必须带 build tags）

```bash
make build            # 前端构建 + go build → dist/sb-control
make test             # go test -tags=with_quic,with_utls,with_clash_api,with_v2ray_api,with_wireguard,with_gvisor ./...
make vet              # 同上 vet
make frontend-check   # redocly lint openapi + tsc --noEmit + vite build（前端门禁）
make api              # openapi-typescript 重新生成 frontend/src/generated/management-api.ts
npm --prefix frontend test   # vitest 前端单测
go test -tags=with_quic,with_utls,with_clash_api,with_v2ray_api,with_wireguard,with_gvisor ./internal/<pkg>/...
```

## 架构与分层

- `internal/app`：装配（`newOps`）；`api/`：管理 API + 订阅端点（handler 层，一域一文件）；`internal/web`：前端静态资源 embed 与健康检查路由；`internal/httpx`：统一错误契约（`requestId`/`code`）与中间件。
- `internal/service/*`：业务服务（user / protocol / certificate / policy / singbox / share / secret / common），领域包内 `model.go`/`store.go`/`service.go`。
- `internal/traffic`：V2Ray 采集、历史聚合、保留清理；`internal/observability`：告警评估、readiness、Webhook/Telegram 外发。
- `internal/deployment/compiler` + `singlevps`：资源 → `sb.json` 编译、同步与回滚；`internal/runtime/singbox`：内核生命周期。**内核日志统一进面板日志**——`kernelLogWriter` 经 sing-box 的 `log.PlatformWriter` 转发进 `internal/logging` 门面（剥 `INFO[  12]` 前缀与 ANSI 色码，级别映射 debug/info/warn/error），内核自身输出重定向 `/dev/null`；不要让内核直接写 stderr（硬编码 `+0800 …` 时间戳格式）。
- `internal/database`：SQLite 打开与迁移。
- 分层规则：HTTP handler 调 service，service 调 store；store 持有 `*sql.DB`（`internal/identity` 的 `h.store` 审计直连是例外）。

## 领域约定（勿破坏）

- **启动配置统一集中在 `internal/config`**：无环境变量、无命令行 flag、无面板运行时设置（`internal/settings` 已删除），`main.go` 固定读取工作目录下的 `config.yaml`，不要重新引入 `SB_*` 环境变量、`--config` flag 或 settings 表。所有 config.yaml 键 → `RuntimeConfig` 强类型字段（时长字段直接是 `time.Duration`，viper 的 mapstructure hook 从 "45s" 字符串解码；非法值让加载失败而不是静默回退）；默认值**只存在于 `DefaultRuntimeConfig()`**，不要重新引入 `viper.SetDefault` 点号路径副本或各消费方自己的回退默认。要热生效的运行时设置（Clash/V2Ray、分流规则等）归数据库面板管理，勿搬进 config.yaml。
- **部署参数在 `config.yaml` 的 `deployment` 段**（`public_address` / `tls_cert_path` / `tls_key_path`），由 `ApplyConfig` → `compiler.LoadInput` 传入。

- **Clash/V2Ray API 收敛**：地址与密钥由面板「服务端配置 → 基础配置」`singbox_config` 管理（**单一事实来源**）；不要在 config.yaml 引入 clash/v2ray 地址配置。
- **config.yaml 只保留与默认值不同的键**（server.listen / server.db_path（默认在 data_dir 下，此处显式放仓库根）/ deployment.public_address / development.allow_private_address / logging 段（与默认一致，显式写入便于自文档））；其余键的默认值见 `internal/config/runtime.go` 的 `DefaultRuntimeConfig`。可选段（缺省即禁用）：`notifications` / `acme`。`cors_allowed_origins` 已删除（无 CORS 中间件消费，勿恢复）。
- **日志配置在 `config.yaml` 的 `logging` 段**（`level`：debug/info/warn/error，同时作用于双 sink；`file_format`：`text`（默认，无颜色可读文本）/`json`；`rotate_max_mb`/`rotate_backups`：文件轮转参数）：`internal/logging`（gookit/slog 流式门面）双 sink——stdout 彩色 text + `<data_dir>/logs/sb-control.log` 尺寸轮转（0600）；改动需重启生效。注意 gookit 级别数值**越小越严重**（Panic=100…Trace=800），过滤方向别写反。
- **流量采集走 V2Ray stats gRPC**（user-tag 按设备归因、经 deviceID→userID 映射汇总到用户并驱动额度扣减，额度单位 GB）；**Clash API 仅实时连接观测**（`/connections`）。采集地址每次周期动态解析（resolver），采集失败触发 `traffic_stalled` 告警。
- **凭据按「设备」维度**：`devices` 表——每设备独立 uuid/password/token，入站用户 name = 设备 id（即 V2Ray stats 标签）；`UNIQUE(user_id, name)`、上限 10 台/用户，设备仅由链接导入自动发现或手动添加（建号不建设备，用户级订阅 token 存 `users.subscription_token_*`，可轮换）；`ua_key`（部分唯一索引）非空的设备为由订阅 UA 指纹自动发现（`share.DeviceKeyFromUserAgent` 客户端识别 + 首次下载自动建设备并触发一次同步，失败回退默认设备），设备列表 `auto` 字段标记。原 `credentials` 表已删（未上线直接重建，无迁移数据，`runtime_artifacts` PK 为 user+device+format）。
- **流量统计行模型（互斥）**：`traffic_hourly/daily` 写入两种行——设备行（`user_id≠''`、`device_id≠''`、`protocol_id=''`）与入站行（`user_id=''`、`device_id=''`、`protocol_id≠''`）。V2Ray user 统计天生无协议归属，因此「用户排行 × 协议过滤」（或反之）恒为空集：前端 TrafficPage 已做维度互斥（切维度清空+禁用另一侧过滤），后端 `ActiveUsers`/`ProtocolShares` 会忽略不适用的过滤——改查询时保持此语义。`AggregateRecent` 的聚合窗口**含当天进行中数据**（end=now 滚动重聚合），勿改回截止当天 00:00（会导致跨天查询缺当天）。
- **两套分流勿混淆**：「订阅分流」（`PoliciesPage`，写入 Clash 订阅、只影响单用户客户端）vs「服务器分流」（`SingBoxPage` routes tab，作用于内核转发、影响所有用户）。
- 告警外发配置在 `config.yaml` 的 `notifications` 段（`webhook_url` / `webhook_secret` / `telegram_bot_token` / `telegram_chat_id`），未配置静默跳过；notifier 通过 `func() config.RuntimeConfig` 实时读最新配置（改 config.yaml 热重载生效）。

## 数据库 / 迁移

- SQLite（mattn/go-sqlite3），DSN 固定 `busy_timeout/foreign_keys/WAL`；打开后 `SetMaxOpenConns(1)`。
- 迁移集中在 `internal/database/migrate.go`；schema 常量在各自包（`service/schema.go`、`traffic/schema.go`、`observability/schema.go`、`events/...`）。
- 新增列/表用幂等 `ALTER TABLE` / `CREATE TABLE IF NOT EXISTS`，并在 `database_test.go` 补用例（含升级路径）。

## 契约与生成物

- API 契约 `docs/api/openapi.management.yaml`；改 API 必须同步契约，跑 `make api` 重生成 `frontend/src/generated/management-api.ts`，再 `make frontend-check`。
- 前端构建产物同步到 `internal/web/embedded/`（`index.html` 已跟踪、`assets/` 目录 gitignore）；前端改动后 `index.html` 的 asset hash 变更属正常，需一并提交。
- **订阅产物只有 `clash.yaml` 一种格式**（`runtime_artifacts` 落库硬编码）；前端订阅链接也只展示它。`sbox.json`/`clmi.yaml`/`jhsub.txt` 曾是 404 死链已删——除非后端真的实现生成器，不要再加回前端格式列表。
- **已删除勿恢复**：`config/validation.go`（agent 时代自由 sb.json 校验，仅测试引用）、流量归档/清理 API（`Archive`/`Purge`/`EstimateBefore` + `/traffic/archive|purge` 路由；`PurgeBefore` 保留供留存任务）、协议类型 `naive`/`shadowtls`（sing-box 独有协议，mihomo/clash 不支持、永远进不了 clash.yaml 订阅，2026-08 移除；保留 `anytls`——mihomo 支持）。

## 测试注意

- 全部 Go 测试需 build tags。
- **import 环**：`internal/database` 导入 `observability`（migrate 应用其 schema）→ `observability` / `traffic` 的测试**不要 import internal/database**，用本包 `Schema` + `internal/service.Schema` 直接建表。
- 业务测试构造走 `sql.Open("sqlite3", ":memory:")` + `SetMaxOpenConns(1)` + `database.Migrate`（包内不冲突时）。

## CI / lint

- `golangci-lint` 用 **v2 配置**（`.golangci.yml`，`run.build-tags` 已配齐四 tags，与 Makefile 一致）；CI 锁 v2.4.0。`~/go/bin/golangci-lint` 若是 v1 会拒绝 v2 配置，装 v2.4.0 对齐验证。
- **ST1005**：错误消息不能以大写拉丁字母开头——中文消息以 "Clash API…"/"Cloudflare…" 开头会挂，写成中文开头（如「监听地址无效（Clash API）」）。
- CI 还有 `gofmt -l ./*.go ./api ./internal` 检查；提交前跑一下。

## 运维 / 排障 gotchas

- **Reality 伪装站证书链上限 8192 字节**（metacubex/utls `realitySize`）：`www.microsoft.com` 等已部署后量子（MLKEM）证书的站点链超限，认证成功后握手必失败（客户端表现为「超时/EOF」）。伪装站选证书链小的（`www.apple.com`/`gateway.icloud.com` 实测 ≈4KB 内）。快速自检：`openssl s_client -connect 127.0.0.1:<port> -servername <sni>` 回真实证书 = 服务端 Reality 活着（认证前的 fallback 转发）。
- **协议端到端自测**（不装客户端）：用面板同版本内核跑官方 CLI —— `GOFLAGS=-tags=with_quic,with_utls go run github.com/sagernet/sing-box/cmd/sing-box@v1.13.19 run -c client.json`（本地 mixed 入站 + 目标出站），再 `curl -x http://127.0.0.1:2080 https://www.gstatic.com/generate_204`。
- **本地 WSL 开发**：后端进程需带 `https_proxy`（规则集/ACME 要访问 GitHub）且 `no_proxy` 覆盖本地端口；协议入站端口 <1024 需 root/setcap，本地用 ≥1024；`networkingMode=mirrored` 可解 Windows→WSL 的 UDP（hy2/tuic）转发问题。
- `traffic_stalled` 告警与「流量采集循环超过阈值未成功完成」错误日志测量的是**采集循环存活**（零流量的一轮查询也算心跳，见 `local_collector.go` 的 `RecordCollection`），并按 Clash API 活跃连接数区分空闲与故障（`ops_app.go` 的 `trafficStallError`）：无活跃连接（含 Clash API 不可观测）按空闲处理，不打错误日志、不告警、不降 readiness；有活跃连接但无采集心跳才是采集链路真的断了，根因看同期的「本地流量采集失败」日志。
- 诊断技巧：`control_plane_secrets` 表内存有各 AES 主密钥，可本地解密 `protocols.settings_json` 核对参数；面板 API 会脱敏私钥字段。

## 提交约定

- 会话产物 `.zcode/plans/` 保持 untracked，不要提交。
- 提交按功能拆、用 conventional commit（`feat(scope):` / `fix` / `refactor` / `docs`）；文档改动（README/CHANGELOG/backlog）随功能或独立提交。
