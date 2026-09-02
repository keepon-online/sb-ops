# Sing-box Ops

Sing-box Ops 是单一 Go 进程，内嵌 sing-box 内核与 Web 管理控制台。用户、设备、协议、证书、订阅、分流规则、内核配置、流量采集与审计都在一个应用内管理。

没有独立的 sing-box 可执行文件、安装器、systemd 单元、Docker 镜像、Compose 文件、Nginx 配置或特权助手。

版本变更见 [CHANGELOG.md](CHANGELOG.md)。

## 功能概览

### 管理与用户

- **单管理员**：首次运行 `/setup` 创建管理员账号；登录 / 会话 / 修改密码，无多账号与角色权限体系。
- **用户与设备**：建号即得一条用户级订阅链接（产物为 `clash.yaml` 一种格式，token 可重置）。凭据按「设备」维度管理：客户端导入时按订阅 UA 指纹 + 来源 IP 自动发现设备并记录平台、客户端与最近活跃（不同公网出口的相同客户端创建独立设备，同一局域网同出口合并），上限 10 台/用户；支持设备停用、删除、凭据轮换；订阅弹窗提供导入二维码与客户端指引。
- **流量与计费**：内嵌内核的 V2Ray stats API 按 user-tag 归因流量到设备再汇总到用户，驱动 GB 额度扣减与到期 / 超量状态阻断。流量分析页支持用户排行、协议份额与活跃用户趋势（小时 / 天聚合）。

### 协议与证书

- 七种接入协议：VLESS（含 Reality）/ VMess / Trojan / Hysteria2 / TUIC / AnyTLS / Shadowsocks 2022；支持协议克隆与密钥轮换。
- 证书：自签名（IP / DNS）与 ACME（Cloudflare DNS-01，自动续期）。
- Hysteria2 / TUIC 支持端口跳跃（iptables REDIRECT 把端口段映射到监听端口）。
- **协议运行状态监控**：协议列表实时显示每个协议的监听端口是否在监听（TCP 查 LISTEN、UDP 查绑定、端口跳跃协议额外核对 iptables 规则）与按入站聚合的活跃连接数，用于排查服务端问题（内核未同步、端口被占、跳跃规则缺失等）。

### 分流（两套概念分离）

- **订阅分流**：写入单个用户下载的 Clash 订阅，只影响该用户的客户端。策略 + 规则 + 内置规则集（广告 / AI / 流媒体 / 社交 / 开发者 / 国内直连等），支持 dry-run 命中测试（面板下载规则集本地评估）与策略冲突验证。
- **服务器分流**：作用于内核转发（影响所有用户）。路由规则与规则集管理，测试路由本地评估请求会命中哪条规则并给出原因。

### 高级用法

- **WARP 出站**：注册免费 Cloudflare WARP 账户并编译为出站。
- **AI 解锁模板**：一键应用面向 AI 服务的分流出站模板。
- **Argo 隧道**：临时隧道或固定域名（Token）模式，域名变化自动重新生成订阅。

### 观测与告警

- **服务器监控**：CPU / 内存 / 磁盘 / 网络实时仪表盘、每核占用与 10 分钟趋势（gopsutil）。
- **实时连接观测**：Clash API 连接明细（入站 / 出口 / 规则链），可单条断开。
- **流量采集自检**：`traffic_stalled` 告警 + 概览页最近采集时间。
- **告警外发**：Webhook / Telegram（`config.yaml` 配置，未配置静默跳过）。
- **审计**：全部管理操作留痕，可在「审计日志」查询。

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

复制 [`config.example.yaml`](config.example.yaml) 为工作目录下的 `config.yaml` 并按环境编辑（配置路径固定为工作目录下的 `config.yaml`，无命令行 flag、无环境变量覆盖；真实 `config.yaml` 已被 gitignore，不会进入仓库）：

```bash
./dist/sb-ops
```

管理 API 监听于 `server.listen`（默认 `127.0.0.1:8030`）。SQLite 数据、生成的证书材料与运行时状态存储在 `server.data_dir`（默认 `./data`）。

### 配置说明

| 段 | 键 | 说明 |
|---|---|---|
| `server` | `listen` / `data_dir` / `db_path` | 监听地址、数据目录、数据库路径 |
| `logging` | `level` / `file_format` / `rotate_max_mb` / `rotate_backups` | 双 sink 日志（stdout + 文件轮转），改动需重启 |
| `singbox` | `certificate_dir` | 生成证书材料的存放目录 |
| `traffic` | `interval` / `retention_days` | 流量采集周期（默认 `1m`）、历史保留天数（默认 90，0 为永久） |
| `deployment` | `public_address` / `tls_cert_path` / `tls_key_path` | 客户端连接的公网地址、可选外部 TLS 证书路径 |
| `notifications` | `webhook_url` / `webhook_secret` / `telegram_bot_token` / `telegram_chat_id` | 告警外发（Webhook / Telegram，均选填） |
| `updates` | `repo` / `token` | 面板自升级源（GitHub `owner/repo` 与读取用 PAT，选填，缺省即禁用） |
| `panel` | `session_ttl` | 会话有效期（默认 `24h`） |
| `development` | `allow_private_address` / `frontend_dev_proxy` | 允许私网地址作为公网地址（本地开发）、前端开发代理地址 |

> Clash API 与 V2Ray API 的地址与密钥由**面板管理**（「服务端配置 → 基础配置」），是单一事实来源：Clash API 用于实时连接观测并自动生成访问密钥，V2Ray API 用于按用户归因的流量采集。`config.yaml` 不包含这两类地址。

`deployment.public_address` 是订阅生成的公网地址（客户端连接本节点所用的 IPv4 或域名），须在启动前配置；`tls_cert_path` / `tls_key_path` 留空则使用面板生成的自签证书。进程所有者需保证证书文件可读、入站端口可绑定（1024 以下端口需相应宿主权限）。

保存受管资源会自动编译并重建内嵌内核，现有代理连接会短暂中断。若新内核启动失败，应用立即恢复上一份成功配置并记录失败到运行时状态。

### 面板自升级

配置 `updates` 段后，「系统 → 系统升级」页面可直接从 GitHub Releases 检查并升级面板自身，无需手动上传二进制：

```yaml
updates:
  repo: "<github owner>/<repo>"   # 托管 Release 资产的仓库
  token: "<PAT>"                   # 具备该仓库 Contents 只读权限的令牌，公开仓库可留空
```

推送 `v*` 标签触发 CI 构建并发布 `sb-ops-linux-<arch>` 与 `SHA256SUMS` 资产，随后在面板点击升级：服务下载对应架构资产并校验 sha256（优先 GitHub 资产摘要，缺失时回退 SHA256SUMS），通过后将当前二进制备份为 `<二进制>.bak` 并原子替换，面板随后优雅停机、以新版本自动重启，协议入站由启动同步自动恢复。下载或校验失败时面板保持旧版本运行并在页面展示原因；若新版本起不来，登录服务器执行 `cp <二进制>.bak <二进制>` 手动回滚即可。

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

## 安全模型

sb-ops 面向**个人自用**场景，以下均为有意的设计取舍而非缺陷，选型前请确认与你的场景匹配：

- **单管理员、无 RBAC/多租户**：整个面板只有一个管理员账号，不存在多用户权限边界。不要把它当作面向不可信用户的多租户服务使用。
- **单 VPS 单进程**：面板、内嵌内核与流量采集在同一进程，不支持多节点联邦；协议由内核直接监听公网端口。
- **订阅链接即长期凭证**：掌握用户订阅 URL 即可获取其全部节点凭据，怀疑泄露时在面板轮换订阅 token；设备凭据可独立停用/轮换，设备上限 10 台/用户。
- **面板必须置于 TLS 反代之后**：进程默认只监听 `127.0.0.1`，应按部署文档经 Nginx 等 TLS 反代对外；管理 API、订阅端点都不应明文暴露在公网。

## 许可证

Copyright (C) 2026 keepon-online

本项目以 [GPL-3.0](LICENSE) 发布。项目以 Go 库形式链接 [sing-box](https://github.com/SagerNet/sing-box)（GPL-3.0）并内嵌于分发二进制，因此 sb-ops 整体采用 GPL 兼容许可证；二次分发时请一并遵守 GPL-3.0 的源码提供义务。

## 项目结构

- `main.go`（根）：单进程应用入口。
- `api/`：管理与订阅 HTTP API（handler 层）。
- `internal/web`：前端静态资源 embed 与健康检查。
- `internal/runtime/singbox`：内嵌内核生命周期管理。
- `internal/deployment/compiler`：受管资源 → sing-box 配置编译器。
- `internal/deployment/singlevps`：配置同步、回滚、端口跳跃与协议运行状态探测。
- `internal/service`：用户、协议、证书、策略、订阅等业务服务（领域包内 model/store/service）。
- `internal/traffic`：V2Ray 流量采集、历史聚合与保留清理。
- `internal/monitor`：服务器监控指标采样（CPU / 内存 / 磁盘 / 网络，gopsutil）。
- `internal/observability`：告警评估、readiness 与 Webhook/Telegram 外发。
- `internal/identity`：单管理员认证与会话。
- `frontend`：React 管理控制台。
