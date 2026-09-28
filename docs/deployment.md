# 生产部署指南（单 VPS · 面板自带 TLS · 协议直连）

面向单台境外 VPS 的正式部署。面板（管理 UI / API / 订阅）自带 TLS 直听 443（证书由面板 ACME DNS-01 自动签发续期）；代理协议由内嵌 sing-box 内核直接监听公网端口。

## 架构总览

```
公网
├── 443/tcp   面板进程直接监听（TLS，ACME 自动证书）   面板 UI + 管理 API + SSE + 订阅
├── 80/tcp    面板进程 ──> 301 跳转 443（可选）
├── 8443/tcp  vless-reality    ┐
├── 2083/tcp  trojan           │
├── 2087/tcp  anytls           ├── 内嵌 sing-box 内核直接监听（端口自定，以面板「协议」页为准）
├── 2096/tcp  vmess(ws)        │
├── 2443/tcp  shadowsocks      │
├── 1443/udp  hysteria2        │
└── 3443/udp  tuic             ┘
```

要点：

- **没有反代层**。vless/trojan/vmess 等是自定义 TCP/UDP 协议本就直连；面板 HTTPS 由进程自身终结，SSE/上传/证书全在面板内闭环。
- **Reality 协议（vless/trojan）不需要证书**；hysteria2 / tuic / anytls / trojan(TLS) 需要证书（见「协议证书」）。
- 面板进程只监听 `127.0.0.1:8030`，公网不可直达。
- 面板以 root 运行（系统管理功能所需，见「部署目录与 systemd」）；协议端口仍要求 ≥1024 的约定不变。

## 前置条件

- 一台境外 VPS（Ubuntu 22.04/24.04 为例），1C1G 起步即可；
- 一个域名，A 记录指向 VPS 公网 IP（推荐托管在 Cloudflare：面板 ACME 的 DNS-01 签发与 certbot DNS-01 都依赖它）；
- 如选择源码构建：Go 1.25+、Node.js 22+（仅在构建机需要，VPS 只需二进制）。

**服务器时区即业务时区**：流量日粒度统计的日界、每日报告的「上一自然日」、定时备份与各类本地时间展示，都锚定服务器系统时区。部署时请设为管理员所在时区（建议无夏令时的固定偏移时区）：

```bash
timedatectl set-timezone Asia/Shanghai   # 例：北京时间；改后需重启 sb-ops 生效
```

时区设置错误不会丢数据——`traffic_daily` 只是 `traffic_hourly` 的日聚合视图，sb-ops 每次启动会按当前时区全量重建日桶。

## 一、安装

### 方式一：Release 二进制（推荐）

```bash
curl -LO https://github.com/keepon-online/sb-ops/releases/latest/download/sb-ops-linux-amd64
curl -LO https://github.com/keepon-online/sb-ops/releases/latest/download/SHA256SUMS
sha256sum -c --ignore-missing SHA256SUMS
```

> 替换 `amd64` 为实际架构（`uname -m`：`x86_64` → `amd64`，`aarch64` → `arm64`）。

### 方式二：源码构建

在任意 Linux 环境（本机或 WSL）：

```bash
git clone https://github.com/keepon-online/sb-ops.git
cd sb-ops
npm --prefix frontend ci
make release VERSION=v1.0.0        # 产物 dist/release/sb-ops-linux-amd64 + SHA256SUMS
```

> 构建标签由 Makefile 统一携带（含 `with_wireguard`、`with_gvisor`，WARP 出站依赖后者），不要手工拼 `go build` 以免漏标签——`buildtags.go` 守卫会让缺标签的构建直接编译失败。

## 二、部署目录与 systemd

```bash
sudo mkdir -p /opt/sb-ops /var/lib/sb-ops
sudo install -m 0755 sb-ops-linux-amd64 /opt/sb-ops/sb-ops
```

> 面板按 **root** 运行：系统管理功能（WARP 客户端一键安装、warp-cli 直执行、
> iptables 端口跃迁等）需要它。协议入站端口本就要求 ≥1024，root 并非监听所需；
> 代价是面板漏洞即整机失守——面板经自带 TLS 暴露 443、单管理员强
> 认证，这个权衡与常见单机管理面板（1Panel 等）一致。

创建 `/opt/sb-ops/config.yaml`（复制仓库中的 `config.example.yaml` 修改，只列必改项）：

```yaml
server:
  listen: "127.0.0.1:8030"        # 明文回环通道（本地 curl / 健康检查）
  tls_listen: ":443"              # 面板 TLS 直听（详见第三章）
  data_dir: "/var/lib/sb-ops/data"
  db_path: "/var/lib/sb-ops/sb-ops.db"

deployment:
  public_address: "panel.example.com"   # 客户端订阅里拨打的地址（域名或公网 IP）

# 可选：告警外发
notifications:
  telegram_bot_token: "..."
  telegram_chat_id: "..."

# 可选：协议证书自动签发（域名托管在 Cloudflare 时推荐，见「协议证书」）
acme:
  email: "you@example.com"
  dns_provider: "cloudflare"
  api_token: "<通过密码管理器注入>"
```

> `development` 段（`allow_private_address` 等）是内网测试开关，生产配置中不要出现。

`/etc/systemd/system/sb-ops.service`：

```ini
[Unit]
Description=Sing-box Ops control plane
After=network-online.target
Wants=network-online.target

[Service]
WorkingDirectory=/opt/sb-ops
ExecStart=/opt/sb-ops/sb-ops
Restart=on-failure
RestartSec=5
# root 运行：WARP 客户端安装等系统管理功能需要真实文件系统与系统命令，
# 不能用 ProtectSystem=strict / NoNewPrivileges（会挡住 apt 与 systemctl）。
PrivateTmp=true

[Install]
WantedBy=multi-user.target
```

```bash
sudo systemctl daemon-reload
sudo systemctl enable --now sb-ops
```

> 面板自升级要求 `/opt/sb-ops` 可写（升级时会写 `.download` 与 `.bak`）；数据（SQLite、证书、cloudflared 二进制、日志）都在 `data_dir` 下。

## 三、面板自带 TLS（无需 Nginx）

面板直接以 TLS 监听 443，证书由面板自身的 ACME（**DNS-01，域名托管在 Cloudflare**）自动签发与续期（续期热载，无需重启）。`config.yaml`：

```yaml
server:
  listen: "127.0.0.1:8030"       # 明文回环通道：本地 curl / 健康检查 / 回滚
  tls_listen: ":443"              # 面板 TLS 直听（空 = 禁用）
  redirect_listen: ":80"          # 可选：80 → 443 纯 301 跳转

deployment:
  panel_domain: "panel.example.com"   # TLS 证书域名（启用 tls_listen 必填）

acme:                                  # 启用 tls_listen 必填（DNS-01 凭据）
  email: "you@example.com"
  dns_provider: "cloudflare"
  api_token: "<token>"
```

要点：

- 首次启动会阻塞数十秒完成首签（DNS-01 写 TXT + 等传播），属预期；证书存在 `certificates` 表（私钥 AES 加密），不落盘。
- SSE / 订阅 / 上传限制都在面板自身处理（无 WriteTimeout + handler 层 `MaxBytesReader`），不需要任何反代缓冲调优。
- **Cloudflare 橙云（代理）与面板直听不配套**：面板按对端地址做设备指纹与限速，经 CF 代理会看到轮换的边缘 IP。面板域名与订阅域名都应**直连 DNS（灰云）**——需要隐藏源站 IP 的场景再评估，个人部署通常不值当（协议端口全开，扫描本就能识别主机）。

### 从 Nginx 反代部署迁移

```bash
sudo systemctl disable --now nginx      # 停用保留安装，作为回滚通道
# config.yaml 按上文补 server.tls_listen / redirect_listen 后重启面板
sudo systemctl restart sb-ops
curl -sI https://panel.example.com/health/ready   # 验证直听生效
```

### 可选：WARP MASQUE 出站（Google 域名解锁）

机房 IP 段常被 Google 判为受限地区（google.com 跳 google.com.hk、Gemini 地区拦截，该判定全年周期性漂移、数周自愈）。解法：官方 cloudflare-warp 客户端以 **proxy 模式**（仅本机 SOCKS5、无 TUN、不碰系统路由）建立 MASQUE 隧道——出口协议族由 Cloudflare 在隧道内部调度，**IPv4-only 的 VPS 也常被分到干净的 IPv6 出口**；面板侧加一个 socks 出站指向它，Google 域名分流规则即可借道。

**推荐：面板一键完成**（需按上文以 root 运行）。「系统管理 → WARP 客户端」区块点「一键安装 WARP 客户端」——自动完成 apt 源添加（公钥下载 + gpg dearmor + sources 条目按发行版代号生成）、软件包安装、`warp-svc` 自启、注册、proxy 模式（端口 40000）与连接；装完点「创建出站并应用 AI 解锁」即完成 warp-masque 出站 + OpenAI/Gemini/Claude 分流（也可手动在「服务配置 → 出站」建 socks 出站，标签必须是 `warp-masque`——健康监控按此 tag 门控，服务器 `127.0.0.1`、端口 `40000`——再到「路由规则」把 Google 系域名指向它）。配置漂移时区块内「重新配置」幂等修复。

手工部署（或面板不可用时）等价命令：

```bash
# 1. 安装官方客户端（Ubuntu 22.04 示例；CODENAME 按 lsb_release -cs 替换）
curl -fsSL https://pkg.cloudflareclient.com/pubkey.gpg | sudo gpg --yes --dearmor -o /usr/share/keyrings/cloudflare-warp-archive-keyring.gpg
echo "deb [signed-by=/usr/share/keyrings/cloudflare-warp-archive-keyring.gpg] https://pkg.cloudflareclient.com/ jammy main" | sudo tee /etc/apt/sources.list.d/cloudflare-client.list
sudo apt-get update && sudo apt-get install -y cloudflare-warp

# 2. 注册并切换为本地代理模式（先切模式再连接，全程不装 TUN 路由）
sudo warp-cli --accept-tos registration new
sudo warp-cli --accept-tos mode proxy
sudo warp-cli --accept-tos proxy port 40000
sudo warp-cli --accept-tos connect
sudo warp-cli status

# 3. 验收（四项全过才算通）：trace warp=on + 出口族 + Google 判定
curl -s --socks5-hostname 127.0.0.1:40000 https://www.cloudflare.com/cdn-cgi/trace | grep -E "^(ip|warp|loc)="
# Google 判定必须用 GET：HEAD 不触发 CN 区 302，会把误判误报为正常
curl -s -o /dev/null -w "%{http_code} %{redirect_url}\n" --socks5-hostname 127.0.0.1:40000 https://www.google.com/
```

配好后总览页出现「WARP 解锁链路」卡片（每 15 分钟自动探测，异常推送 `warp_masque_degraded` 告警）。维护：出口判定再漂移时先点卡片上的「换出口」（面板以 root 直接执行 warp-cli 断开重连，无需 sudoers）；卸载恢复：面板删除出站与规则后 `sudo warp-cli disconnect && sudo apt remove -y cloudflare-warp`。

## 四、面板初始化

1. 浏览器打开 `https://panel.example.com`，经 `/setup` 创建管理员；
2. 「协议」页确认/创建协议节点（端口与防火墙放行一致）；
3. 「用户」页创建订阅用户并**勾选全部需要的协议**（漏勾的协议不会出现在订阅中，且该入站无用户、连接会被拒）；
4. 复制订阅链接到客户端，拉取验证：

```text
https://panel.example.com/subscribe/<subscription-token>/clash.yaml
```

### 协议证书（hysteria2 / tuic / anytls / trojan）

三选一：

| 方案 | 适用 | 操作 |
|---|---|---|
| **面板 ACME（推荐）** | 域名托管在 Cloudflare | `config.yaml` 配 `acme` 段（DNS-01，不开端口、不与协议端口冲突），面板自动签发续期；协议使用 `tlsMode: certificate` 指向自动管理证书 |
| 外部证书 | 已有证书/其它 CA | `deployment.tls_cert_path` / `tls_key_path` 指向证书文件，进程需可读 |
| 自签 + 跳过校验 | 仅 IP、快速验证 | 默认行为，客户端 `skip-cert-verify: true` |

Reality 协议（vless/trojan）不需要以上任何一种。

注意事项：

- 签发前清理同一 `_acme-challenge.<domain>` 下残留的旧 TXT 记录——多个旧 challenge 值会使 Let's Encrypt 返回 `Incorrect TXT record`；DNS 记录写入后需等待传播（面板会轮询 TXT 记录，最长 60 秒再提交 challenge）。
- 签发后在「证书」页把证书绑定到各 TLS 协议，并把 `serverName` 设为证书域名；使用可信域名证书时订阅不应设 `skip-cert-verify: true`，IP 自签证书才保留跳过校验。

### 可选：高级功能在 VPS 上的差异

- **WARP 出站 / AI 解锁**：VPS 直连 `api.cloudflareclient.com` 通常无障碍；「注册免费 WARP」或手工粘贴 wgcf 配置皆可。AI 解锁规则在「服务端配置 → 服务器分流」一键生成。
- **Argo 隧道**：cloudflared 二进制首次自动从 GitHub 下载（`data/runtime/bin/cloudflared`）。生产建议固定域名 Token 模式：面板创建隧道绑定 VMess WebSocket 协议，并在 Cloudflare Tunnel 配置 Public Hostname（如 `sb.example.com` → `http://localhost:2096`）；connector running 后用客户端测试，**浏览器访问节点域名返回 404 属正常现象**。
  - Argo 节点不要把面板 `8030` 作为 ingress 目标；面板/订阅走 `panel.example.com`，节点域名独立。
  - 从其它部署迁移 Argo 时：先停旧 cloudflared（避免同一 Tunnel 双连接），再在面板建隧道。**不要用 `pkill -f cloudflared` 清理进程**——会连 sb-ops 托管的 cloudflared 一起杀掉；优先在面板禁用隧道，或停旧部署自己的服务。
- **DNS**：VPS 在境外时无污染问题，服务器分流 DNS 可保持为空（用系统解析器）。

## 五、上线验证清单

```bash
systemctl status sb-ops
curl -s http://127.0.0.1:8030/health/ready       # 期望 status":"ready"
sudo journalctl -u sb-ops -n 50 --no-pager       # 无 ERROR
printf '' | openssl s_client -connect panel.example.com:443 \
  -servername panel.example.com 2>/dev/null | grep -E 'subject=|issuer=|Verify return code'
```

- 浏览器打开 `https://panel.example.com` 登录正常；
- 客户端拉取订阅，逐节点延迟测试通过；
- 协议页各协议「运行状态」列显示监听正常；
- Argo 节点（如有）在客户端可见且可连；
- 访问目标站点并核对日志中出站是否符合预期分流。

## 六、运维

### 升级

**推荐：面板自升级**。`config.yaml` 配置 `updates` 段（公开部署的 sb-ops 可不填 token），在「系统 → 系统升级」页一键完成：下载 → sha256 校验 → 备份 `.bak` → 原子替换 → 优雅重启；下载或校验失败时保持旧版本运行并在页面展示原因。新版本起不来时手动回滚：

```bash
sudo systemctl stop sb-ops
sudo cp /opt/sb-ops/sb-ops.bak /opt/sb-ops/sb-ops
sudo systemctl start sb-ops
```

**兜底：手动替换二进制**（自升级不可用时）：

```bash
sudo systemctl stop sb-ops
sudo install -m 0755 sb-ops-linux-amd64 /opt/sb-ops/sb-ops
sudo systemctl start sb-ops
```

数据库迁移在启动时自动执行（幂等）。

### 备份

停机或低峰执行均可（SQLite WAL 下建议先停服务）：

```bash
sudo systemctl stop sb-ops
sudo tar czf sb-ops-backup-$(date +%F).tar.gz /var/lib/sb-ops /opt/sb-ops/config.yaml
sudo systemctl start sb-ops
```

备份包含全部状态：用户与设备凭据、订阅 token、审计与流量账目、证书材料。建议异地保存。

### 排障

| 现象 | 入口 |
|---|---|
| 内核同步失败、面板报「上一份成功配置仍在运行」 | 日志搜 `同步失败`；查 `runtime_state.failure_reason` |
| 面板通知/实时刷新不动 | 面板自带 SSE 无需配置；检查 /health/ready 与事件流网络 |
| 节点测不通 | 先看协议是否分配给用户 → 防火墙端口 → 协议「运行状态」列 → 日志中该入站的认证错误 |
| 命中分流规则的连接秒断 | 日志搜 `outbound not found`——服务器分流规则引用了不存在的出站 |
| WARP 出站不可用 | 构建是否带 `with_gvisor`（看构建日志或重新 `make release`） |
| 证书签发失败 `Incorrect TXT record` | 清理 `_acme-challenge` 下残留 TXT 记录后重试 |
