# Sing-box Ops

Sing-box Ops 是单一 Go 进程，内嵌 sing-box 内核与 Web 管理控制台。用户、协议、证书、订阅、分流规则、内核配置、流量采集与审计都在一个应用内管理。

没有独立的 sing-box 可执行文件、安装器、systemd 单元、Docker 镜像、Compose 文件、Nginx 配置或特权助手。

版本变更见 [CHANGELOG.md](CHANGELOG.md)。

## 功能概览

- **单管理员**：首次运行 `/setup` 创建管理员账号，证书、协议与订阅均在其后经面板配置。登录/会话/修改密码，无多账号与角色权限体系。
- **用户与订阅**：创建订阅用户并下发订阅链接，产物为 `clash.yaml` 一种格式；建号即得一条用户级订阅链接：客户端导入时按 UA 自动发现设备并分别统计（记录平台与客户端、最近活跃），也可手动添加/停用/轮换/删除设备，链接可重置，支持导入二维码与客户端指引。
- **流量与计费**：通过内嵌内核的 V2Ray stats API 按 user-tag 归因流量到具体设备再汇总到用户，驱动额度扣减（GB）与到期/超量状态阻断；每设备用量独立统计，历史流量按天聚合。
- **分流**：两套概念分离 ——「订阅分流」写入用户的 Clash 订阅（只影响单个用户客户端），「服务器分流」作用于内核转发（影响所有用户）。
- **观测与告警**：服务器监控（CPU / 内存 / 磁盘 / 网络实时指标与 10 分钟趋势，gopsutil）；实时连接观测（Clash API）；流量采集自检（`traffic_stalled` 告警 + 概览最近采集时间）；告警外发到 Webhook / Telegram。
- **审计**：全部管理操作留痕，可在「审计日志」查看。

## 构建

需要 Go 1.25 与 Node.js 22，先安装前端依赖一次：

```bash
npm --prefix frontend ci
make build
```

产物为 `dist/sb-ops`。构建内嵌前端与带 QUIC、uTLS、Clash API、V2Ray API、WireGuard（WARP 出站，含 gVisor 用户态栈）支持的 sing-box 内核。

Linux 发布二进制：

```bash
make release VERSION=v2.0.0
```

产物与校验和写入 `dist/release/`。

## 运行

复制并编辑 [`config.yaml`](config.yaml)，直接启动二进制（配置路径固定为工作目录下的 `config.yaml`，无命令行 flag、无环境变量覆盖）：

```bash
./dist/sb-ops
```

管理 API 监听于 `server.listen`（默认 `127.0.0.1:8030`）。SQLite 数据、生成的证书材料与运行时状态存储在 `server.data_dir`（默认 `./data`）。

### 配置说明

| 段 | 键 | 说明 |
|---|---|---|
| `server` | `listen` / `data_dir` / `db_path` | 监听地址、数据目录、数据库路径 |
| `singbox` | `certificate_dir` | 生成证书材料的存放目录 |
| `traffic` | `interval` / `retention_days` | 流量采集周期（默认 `1m`）、历史保留天数（默认 90，0 为永久） |
| `deployment` | `public_address` / `tls_cert_path` / `tls_key_path` | 客户端连接的公网地址、可选外部 TLS 证书路径 |
| `notifications` | `webhook_url` / `webhook_secret` / `telegram_bot_token` / `telegram_chat_id` | 告警外发（Webhook / Telegram，均选填） |
| `panel` | `session_ttl` | 会话有效期（默认 `24h`） |
| `development` | `frontend_dev_proxy` | 开发模式前端代理地址（默认空：始终由内嵌产物提供服务） |

> Clash API 与 V2Ray API 的地址与密钥由**面板管理**（「服务端配置 → 基础配置」），是单一事实来源：Clash API 用于实时连接观测并自动生成访问密钥，V2Ray API 用于按用户归因的流量采集。`config.yaml` 不包含这两类地址。

`deployment.public_address` 是订阅生成的公网地址（客户端连接本节点所用的 IPv4 或域名），须在启动前配置；`tls_cert_path` / `tls_key_path` 留空则使用面板生成的自签证书。进程所有者需保证证书文件可读、入站端口可绑定（1024 以下端口需相应宿主权限）。

保存受管资源会自动编译并重建内嵌内核，现有代理连接会短暂中断。若新内核启动失败，应用立即恢复上一份成功配置并记录失败到运行时状态。

## 开发

```bash
npm --prefix frontend run dev
go run -tags=with_quic,with_utls,with_clash_api,with_v2ray_api,with_wireguard,with_gvisor .
```

后端默认始终服务内嵌前端产物；本地联调 Vite 时在 `config.yaml` 加上 `development.frontend_dev_proxy: "http://127.0.0.1:5173"` 即切换为代理模式。

运行校验：

```bash
make test
make vet
make frontend-check
```

管理 API 契约见 [`docs/api/openapi.management.yaml`](docs/api/openapi.management.yaml)，前端类型由契约生成（`make api` 更新）。

## 部署

生产部署（VPS + Nginx 反代面板、协议端口直连）见 [`docs/deployment.md`](docs/deployment.md)。

## 项目结构

- `main.go`（根）：单进程应用入口。
- `api/`：管理与订阅 HTTP API（handler 层）。
- `internal/web`：前端静态资源 embed 与健康检查。
- `internal/runtime/singbox`：内嵌内核生命周期管理。
- `internal/deployment/compiler`：受管资源 → sing-box 配置编译器。
- `internal/service`：用户、协议、证书、策略、订阅等业务服务（领域包内 model/store/service）。
- `internal/traffic`：V2Ray 流量采集、历史聚合与保留清理。
- `internal/monitor`：服务器监控指标采样（CPU / 内存 / 磁盘 / 网络，gopsutil）。
- `internal/observability`：告警评估、readiness 与 Webhook/Telegram 外发。
- `internal/identity`：单管理员认证与会话。
- `frontend`：React 管理控制台。
