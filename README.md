# sb-ops

自用的 sing-box 服务器管理面板：**一个 Go 二进制，内嵌 sing-box 内核与 Web 控制台**——用户、设备、协议、证书、订阅、分流、流量采集与审计全部在一个进程里管理。面向个人在单台 VPS 上的自建节点，不为多租户/多节点设计。

[![CI](https://github.com/keepon-online/sb-ops/actions/workflows/ci.yml/badge.svg)](https://github.com/keepon-online/sb-ops/actions/workflows/ci.yml)
[![License: GPL-3.0](https://img.shields.io/badge/License-GPL--3.0-blue.svg)](LICENSE)
[![Release](https://img.shields.io/github/v/release/keepon-online/sb-ops)](https://github.com/keepon-online/sb-ops/releases)

## 为什么选 sb-ops

在自用 VPS 上跑 sing-box，常见姿势是 x-ui 系面板或手写 `sb.json`。sb-ops 提供第三种选择：

- **单进程、零外部依赖**：没有容器、没有独立内核进程、没有数据库服务——二进制 + SQLite + systemd 就够了。前端静态资源也内嵌，发布物只有一个文件。
- **订阅即产物**：创建用户即得一条订阅链接（`clash.yaml`），Clash Verge 等 mihomo 系客户端导入即用。设备由订阅链接自动发现：客户端首次拉取时按「UA + 来源 IP」指纹登记，每台设备独立凭据、独立轮换。
- **流量看得见**：内嵌内核的 V2Ray stats 按设备归因、汇总到用户，驱动 GB 额度扣减与超量阻断；Clash API 提供实时连接观测（入站 / 出口 / 命中规则）。
- **改配置不掉链子**：保存资源即编译配置并热重建内核，新配置起不来自动回滚上一份成功配置；悬空出站引用有全链路校验（这类错误 sing-box 自检不报、内核照常启动、命中才断连）；全部管理操作留审计，更新类操作记录字段级 `{old, new}` diff。
- **面板自升级**：配置 GitHub Release 源后，网页上一键检查并升级——sha256 校验、备份 `.bak`、原子替换、优雅重启，协议入站由启动同步自动恢复。

## 特性

### 用户与订阅

- 建号即得用户级订阅链接，token 可轮换；产物为 `clash.yaml` 一种格式。
- 设备自动发现（上限 10 台/用户）：记录平台、客户端、来源地址与最近活跃，支持停用 / 删除 / 凭据轮换；订阅弹窗提供导入二维码与客户端指引。
- V2Ray stats 流量归因到设备再汇总到用户，GB 额度扣减、到期 / 超量阻断；流量分析页支持用户排行、协议份额与活跃趋势。

### 协议与证书

- 七种接入协议：VLESS（含 Reality）/ VMess / Trojan / Hysteria2 / TUIC / AnyTLS / Shadowsocks 2022，支持协议克隆与密钥轮换。
- 证书：自签（IP / DNS）与 ACME（Cloudflare DNS-01，自动续期）两种来源；Hysteria2 / TUIC 支持端口跳跃（iptables REDIRECT）。
- 协议运行状态监控：每个协议的端口监听状态（TCP LISTEN / UDP 绑定 / 跳跃规则）与按入站聚合的活跃连接数。

### 分流（两套概念，作用域不同）

- **订阅分流**：写入单个用户的 Clash 订阅，只影响该用户客户端；策略 + 规则 + 内置规则集，支持 dry-run 命中测试。
- **服务器分流**：作用于内核转发、影响所有用户；路由规则与规则集管理，测试路由本地评估请求会命中哪条规则。

### 高级用法

- **WARP 出站**：注册免费 Cloudflare WARP 并编译为出站（构建已带 gVisor）。
- **AI 解锁模板**：一键生成面向 AI 服务的服务器分流规则。
- **Argo 隧道**：临时隧道或固定域名（Token）模式，cloudflared 由面板托管，域名变化自动重生成订阅。

### 观测与审计

- 服务器监控仪表盘（CPU / 内存 / 磁盘 / 网络，每核占用与趋势）。
- `traffic_stalled` 采集自检告警 + Webhook / Telegram 外发（未配置静默跳过）。
- 全部管理操作审计留痕，可查询。

## 快速开始

### 1. 获取二进制

从 [Releases](https://github.com/keepon-online/sb-ops/releases) 下载对应架构资产并校验（也可源码构建，见「开发」）：

```bash
curl -LO https://github.com/keepon-online/sb-ops/releases/latest/download/sb-ops-linux-amd64
curl -LO https://github.com/keepon-online/sb-ops/releases/latest/download/SHA256SUMS
sha256sum -c --ignore-missing SHA256SUMS
chmod +x sb-ops-linux-amd64
```

### 2. 写配置

复制 [`config.example.yaml`](config.example.yaml) 为工作目录下的 `config.yaml`，至少修改 `deployment.public_address`（客户端连接本节点所用的公网 IPv4 或域名）。全部键见下方「配置参考」。

```bash
cp config.example.yaml config.yaml
$EDITOR config.yaml
```

### 3. 启动并初始化

```bash
./sb-ops-linux-amd64        # 读取工作目录下的 config.yaml
```

浏览器打开 `http://127.0.0.1:8030`（或 `server.listen` 指定的地址），经 `/setup` 创建管理员账号。

### 4. 建用户、发订阅

1. 「协议」页确认协议节点（端口默认大于 1024，无需 root）；
2. 「用户」页创建用户并**勾选全部需要的协议**——漏勾的协议不会出现在订阅中，且该入站无此用户、连接会被拒；
3. 复制订阅链接到客户端拉取验证。

本地快速体验允许私网地址：`config.yaml` 中开启 `development.allow_private_address: true`（生产部署不要携带 `development` 段）。正式上公网请按 [生产部署指南](docs/deployment.md) 操作——面板应置于 Nginx 等 TLS 反代之后，协议端口直连。

## 配置参考

| 段 | 键 | 说明 |
|---|---|---|
| `server` | `listen` / `data_dir` / `db_path` | 监听地址（默认 `127.0.0.1:8030`）、数据目录（默认 `./data`）、数据库路径 |
| `logging` | `level` / `file_format` / `rotate_max_mb` / `rotate_backups` | 双 sink 日志（stdout + 文件轮转），改动需重启 |
| `singbox` | `certificate_dir` | 生成证书材料的存放目录 |
| `traffic` | `interval` / `retention_days` | 流量采集周期（默认 `1m`）、历史保留天数（默认 90，0 为永久） |
| `deployment` | `public_address` / `tls_cert_path` / `tls_key_path` | 客户端连接的公网地址、可选外部 TLS 证书路径 |
| `notifications` | `webhook_url` / `webhook_secret` / `telegram_bot_token` / `telegram_chat_id` | 告警外发（Webhook / Telegram，均选填） |
| `updates` | `repo` / `token` | 面板自升级源（GitHub `owner/repo` 与读取用 PAT，选填，缺省即禁用；公开仓库 token 可留空） |
| `panel` | `session_ttl` | 会话有效期（默认 `24h`） |
| `development` | `allow_private_address` / `frontend_dev_proxy` | 允许私网地址作为公网地址（本地测试）、前端开发代理地址 |

> Clash API 与 V2Ray API 的地址与密钥由**面板管理**（「服务端配置 → 基础配置」），是单一事实来源：Clash API 用于实时连接观测，V2Ray API 用于按用户归因的流量采集。`config.yaml` 不包含这两类地址。

保存受管资源会自动编译并重建内嵌内核，现有代理连接会短暂中断；若新内核启动失败，应用立即恢复上一份成功配置并记录失败原因。

## 面板自升级

配置 `updates` 段后，「系统 → 系统升级」页面可直接从 GitHub Releases 检查并升级面板自身：

```yaml
updates:
  repo: "<github owner>/<repo>"
  token: "<PAT>"   # 具备该仓库 Contents 只读权限的令牌，公开仓库可留空
```

升级流程：下载对应架构 Release 资产 → sha256 校验（优先 GitHub 资产摘要，缺失时回退 SHA256SUMS）→ 旧二进制备份为 `<二进制>.bak` → 原子替换 → 优雅停机并以新版本重启，协议入站由启动同步自动恢复。下载或校验失败保持旧版本运行并在页面展示原因；新版本起不来时，登录服务器执行 `cp <二进制>.bak <二进制>` 手动回滚。

## 安全模型

sb-ops 面向**个人自用**场景，以下均为有意的设计取舍而非缺陷，选型前请确认与你的场景匹配：

- **单管理员、无 RBAC/多租户**：整个面板只有一个管理员账号，不存在多用户权限边界。不要把它当作面向不可信用户的多租户服务使用。
- **单 VPS 单进程**：面板、内嵌内核与流量采集在同一进程，不支持多节点联邦；协议由内核直接监听公网端口。
- **订阅链接即长期凭证**：掌握用户订阅 URL 即可获取其全部节点凭据，怀疑泄露时在面板轮换订阅 token；设备凭据可独立停用/轮换，设备上限 10 台/用户。
- **面板必须置于 TLS 反代之后**：进程默认只监听 `127.0.0.1`，应按部署文档经 Nginx 等 TLS 反代对外；管理 API、订阅端点都不应明文暴露在公网。

## 常见问题

- **只支持 clash.yaml 订阅吗？** 是，当前唯一产物格式，适配 Clash Verge / mihomo 系客户端。
- **支持多人合租 / 多管理员吗？** 不支持。用户体系面向「一个管理员的家人朋友」，管理面只有单账号（见「安全模型」）。
- **必须有域名吗？** 不是必须——IP + 自签证书 + 客户端跳过校验也能跑。但推荐域名：ACME 可信证书、Reality 伪装 SNI、Argo 隧道都依赖域名。
- **客户端需要 TUN 模式吗？** 不需要，普通系统代理即可用。仅「设备平台画像」的 windows 判定依赖系统流量进隧道（需要 TUN）。
- **Reality 协议需要配证书吗？** 不需要。Hysteria2 / TUIC / AnyTLS / Trojan 才需要（面板 ACME、外部证书、自签三选一）。
- **防火墙放行哪些端口？** 面板经 Nginx 的 80/443，加上「协议」页实际使用的协议端口（TCP/UDP 按协议而定）。

## 开发

需要 Go 1.25 与 Node.js 22：

```bash
npm --prefix frontend ci
make build          # 前端构建 + go build → dist/sb-ops
make test           # go test（build tags 由 Makefile 统一携带）
make vet
make frontend-check # openapi lint + tsc + vite build
```

本地联调前端：`npm --prefix frontend run dev`，并在 `config.yaml` 配 `development.frontend_dev_proxy: "http://127.0.0.1:5173"`。

管理 API 契约见 [`docs/api/openapi.management.yaml`](docs/api/openapi.management.yaml)，前端类型由契约生成（`make api` 更新）。全部命令与约定见 [AGENTS.md](AGENTS.md)。

## 项目结构

- `main.go`（根）：单进程应用入口。
- `api/`：管理与订阅 HTTP API（handler 层）。
- `internal/web`：前端静态资源 embed 与健康检查。
- `internal/runtime/singbox`：内嵌内核生命周期管理。
- `internal/deployment/compiler`：受管资源 → sing-box 配置编译器。
- `internal/deployment/singlevps`：配置同步、回滚、端口跳跃与协议运行状态探测。
- `internal/service`：用户、协议、证书、策略、订阅等业务服务（领域包内 model/store/service）。
- `internal/traffic`：V2Ray 流量采集、历史聚合与保留清理。
- `internal/monitor`：服务器监控指标采样（gopsutil）。
- `internal/observability`：告警评估、readiness 与 Webhook/Telegram 外发。
- `internal/identity`：单管理员认证与会话。
- `frontend`：React 管理控制台。

## 许可证

Copyright (C) 2026 keepon-online

本项目以 [GPL-3.0](LICENSE) 发布。项目以 Go 库形式链接 [sing-box](https://github.com/SagerNet/sing-box)（GPL-3.0）并内嵌于分发二进制，因此 sb-ops 整体采用 GPL 兼容许可证；二次分发时请一并遵守 GPL-3.0 的源码提供义务。
