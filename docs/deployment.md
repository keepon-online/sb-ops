# 生产部署指南（单 VPS · Nginx 反代面板 · 协议直连）

面向单台境外 VPS 的正式部署。面板（管理 UI / API / 订阅）经 Nginx 以 HTTPS 对外；代理协议由内嵌 sing-box 内核直接监听公网端口，不经过 Nginx。

## 架构总览

```
公网
├── 443/tcp   Nginx (TLS) ──反代──> 127.0.0.1:8030   面板 UI + 管理 API + SSE
├── 80/tcp    Nginx ──> 301 跳转 443（或 certbot http-01 校验）
├── 8443/tcp  vless-reality    ┐
├── 2083/tcp  trojan           │
├── 2087/tcp  anytls           ├── 内嵌 sing-box 内核直接监听（端口自定，以面板「协议」页为准）
├── 2096/tcp  vmess(ws)        │
├── 2443/tcp  shadowsocks      │
├── 1443/udp  hysteria2        │
└── 3443/udp  tuic             ┘
```

要点：

- **Nginx 只负责面板**。vless/trojan/vmess 等是自定义 TCP/UDP 协议，Nginx 无法也不需要反代。
- **Reality 协议（vless/trojan）不需要证书**；hysteria2 / tuic / anytls / trojan(TLS) 需要证书（见「协议证书」）。
- 面板进程只监听 `127.0.0.1:8030`，公网不可直达。
- 协议端口均大于 1024 时无需 root / setcap。

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
sudo useradd -r -s /usr/sbin/nologin sbops
sudo mkdir -p /opt/sb-ops /var/lib/sb-ops
sudo install -m 0755 sb-ops-linux-amd64 /opt/sb-ops/sb-ops
sudo chown -R sbops:sbops /var/lib/sb-ops
```

创建 `/opt/sb-ops/config.yaml`（复制仓库中的 `config.example.yaml` 修改，只列必改项）：

```yaml
server:
  listen: "127.0.0.1:8030"        # 只监听本机，由 Nginx 反代
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
User=sbops
Group=sbops
WorkingDirectory=/opt/sb-ops
ExecStart=/opt/sb-ops/sb-ops
Restart=on-failure
RestartSec=5
# 基础加固
NoNewPrivileges=true
ProtectSystem=strict
ReadWritePaths=/var/lib/sb-ops /opt/sb-ops
PrivateTmp=true

[Install]
WantedBy=multi-user.target
```

```bash
sudo systemctl daemon-reload
sudo systemctl enable --now sb-ops
```

> 面板自升级要求 `/opt/sb-ops` 对运行用户可写（升级时会写 `.download` 与 `.bak`）——上面的 `install` 已将二进制属主设为 sbops，目录本身也需保持可写。数据（SQLite、证书、cloudflared 二进制、日志）都在 `data_dir` 下。

## 三、Nginx 与面板证书

```bash
sudo apt install -y nginx certbot
```

`/etc/nginx/sites-available/sb-ops`：

```nginx
server {
    listen 80;
    listen [::]:80;
    server_name panel.example.com;
    return 301 https://$host$request_uri;
}

server {
    listen 443 ssl http2;
    listen [::]:443 ssl http2;
    server_name panel.example.com;

    ssl_certificate     /etc/letsencrypt/live/panel.example.com/fullchain.pem;
    ssl_certificate_key /etc/letsencrypt/live/panel.example.com/privkey.pem;

    client_max_body_size 16m;

    # SSE 实时事件流：必须关缓冲、拉长超时，否则面板通知卡住
    location /api/v1/events {
        proxy_pass http://127.0.0.1:8030;
        proxy_http_version 1.1;
        proxy_set_header Connection "";
        proxy_set_header Host $host;
        proxy_set_header X-Real-IP $remote_addr;
        proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Proto $scheme;
        proxy_buffering off;
        proxy_cache off;
        proxy_read_timeout 3600s;
        proxy_send_timeout 3600s;
    }

    location / {
        proxy_pass http://127.0.0.1:8030;
        proxy_http_version 1.1;
        proxy_set_header Host $host;
        proxy_set_header X-Real-IP $remote_addr;
        proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Proto $scheme;
    }
}
```

域名不在 Cloudflare 或想走 http-01 时，可先只配 80 段反代，用 `certbot --nginx -d panel.example.com` 签发并自动改写。域名托管在 Cloudflare 时推荐 DNS-01（不依赖 80 端口可达性）：

```bash
sudo apt install -y certbot python3-certbot-dns-cloudflare
sudo install -d -m 0700 /root/.secrets/certbot
# 写入 /root/.secrets/certbot/cloudflare.ini：
#   dns_cloudflare_api_token = <token>
sudo chmod 600 /root/.secrets/certbot/cloudflare.ini
sudo certbot certonly --dns-cloudflare \
  --dns-cloudflare-credentials /root/.secrets/certbot/cloudflare.ini \
  --non-interactive --agree-tos -m you@example.com \
  -d panel.example.com
```

启用站点并验证续期（续期后必须 reload Nginx，用 deploy hook 自动化）：

```bash
sudo ln -s /etc/nginx/sites-available/sb-ops /etc/nginx/sites-enabled/
sudo nginx -t && sudo systemctl reload nginx
sudo install -d /etc/letsencrypt/renewal-hooks/deploy
printf '#!/bin/sh\nsystemctl reload nginx\n' | sudo tee /etc/letsencrypt/renewal-hooks/deploy/reload-nginx.sh
sudo chmod +x /etc/letsencrypt/renewal-hooks/deploy/reload-nginx.sh
sudo certbot renew --dry-run
```

### 域名经 Cloudflare 代理（橙云）时必须还原真实来源 IP

面板若挂在 CF 代理后面（订阅域名想借 CF 隐藏源站 IP 时常见），Nginx 看到的客户端地址是**轮换的 CF 边缘节点 IP**，而面板的设备自动发现（UA + 来源 IP 指纹、平台画像前缀归因）、登录限速键、审计 IP 全依赖真实客户端地址——不还原的话设备名后缀与来源地址会变成无意义且不停漂移的 CF 地址，平台画像永远匹配不上，同款多客户端折进同一行设备。在每个经 CF 的 server 块（443）加：

```nginx
    # 从 CF 专属头还原真实客户端 IP；网段列表见 https://www.cloudflare.com/ips（变更不频繁，定期对齐）
    real_ip_header CF-Connecting-IP;
    set_real_ip_from 173.245.48.0/20;
    # ……其余 CF IPv4/IPv6 网段逐行列出
```

要点：`set_real_ip_from` 只信任 CF 网段，非 CF 来源伪造 `CF-Connecting-IP` 头不会生效（realip 模块只对可信来源采信该头）；配置后 `$remote_addr`、`X-Real-IP`、`X-Forwarded-For` 末端与 access log 全部恢复真实地址，存量设备的污染地址会在下次订阅拉取时自动修正（改名 + `source_ip` 更新）。订阅若专门走独立域名（如 `sub.example.com`），该域名的 server 块同样要配。

## 四、防火墙

```bash
sudo ufw allow 22/tcp
sudo ufw allow 80,443/tcp          # Nginx
sudo ufw allow 8443,2083,2087,2096,2443/tcp    # 以面板「协议」页实际端口为准
sudo ufw allow 1443,3443/udp
sudo ufw enable
# 8030 不放行：面板只应经 Nginx 访问
```

## 五、面板初始化

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

## 六、上线验证清单

```bash
systemctl status sb-ops nginx
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

## 七、运维

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
| 面板通知/实时刷新不动 | Nginx SSE 段配置（`proxy_buffering off`、长超时） |
| 节点测不通 | 先看协议是否分配给用户 → 防火墙端口 → 协议「运行状态」列 → 日志中该入站的认证错误 |
| 命中分流规则的连接秒断 | 日志搜 `outbound not found`——服务器分流规则引用了不存在的出站 |
| WARP 出站不可用 | 构建是否带 `with_gvisor`（看构建日志或重新 `make release`） |
| 证书签发失败 `Incorrect TXT record` | 清理 `_acme-challenge` 下残留 TXT 记录后重试 |
