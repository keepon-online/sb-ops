# VPS 生产部署指南（Nginx 反代面板 + 协议直连）

面向单台境外 VPS 的正式部署。面板（管理 UI / API）经 Nginx 以 HTTPS 对外；代理协议由内嵌 sing-box 内核直接监听公网端口，不经过 Nginx。

## 架构总览

```
公网
├── 443/tcp   Nginx (TLS) ──反代──> 127.0.0.1:8030   面板 UI + 管理 API + SSE
├── 80/tcp    Nginx ──> 301 跳转 443（或 certbot http-01 校验）
├── 8443/tcp  vless-reality    ┐
├── 2083/tcp  trojan(reality)  │
├── 2087/tcp  anytls           ├── 内嵌 sing-box 内核直接监听（端口以面板「协议」页为准）
├── 2096/tcp  vmess            │
├── 2443/tcp  shadowsocks      │
├── 1443/udp  hysteria2        │
└── 3443/udp  tuic             ┘
```

要点：

- **Nginx 只负责面板**。vless/trojan/vmess 等是自定义 TCP/UDP 协议，Nginx 无法也不需要反代。
- **Reality 协议（vless/trojan）不需要证书**；hysteria2 / tuic / anytls 需要证书（见「协议证书」一节）。
- 面板进程只监听 `127.0.0.1:8030`，公网不可直达。

## 前置条件

- 一台境外 VPS（Ubuntu 22.04/24.04 为例），1C1G 起步即可；
- 一个域名，A 记录指向 VPS 公网 IP（推荐托管在 Cloudflare，协议证书的 DNS-01 签发依赖它）；
- Go 1.25+、Node.js 22+（仅在构建机需要；VPS 只需二进制）。

## 一、构建

在 Linux 环境（VPS 本机或 WSL）构建：

```bash
git clone git@github.com:keepon-online/sb-ops.git
cd sb-ops
npm --prefix frontend ci
make release VERSION=v1.0.0        # 产物 dist/release/sb-ops-linux-amd64 + SHA256SUMS
```

> 构建标签由 Makefile 统一携带（含 `with_wireguard`、`with_gvisor`，WARP 出站依赖后者），不要手工拼 `go build` 以免漏标签——`buildtags.go` 守卫会让缺标签的构建直接编译失败。

## 二、部署目录与 systemd

```bash
sudo useradd -r -s /usr/sbin/nologin sbops
sudo mkdir -p /opt/sb-ops /var/lib/sb-ops
sudo install -m 0755 dist/release/sb-ops-linux-amd64 /opt/sb-ops/sb-ops
sudo chown -R sbops:sbops /var/lib/sb-ops
```

`/opt/sb-ops/config.yaml`（生产版示例，只列必改项，其余用默认值）：

```yaml
server:
  listen: "127.0.0.1:8030"        # 只监听本机，由 Nginx 反代
  db_path: "/var/lib/sb-ops/singbox-web.db"

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
  api_token: "CF_API_TOKEN"
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

> 所有协议端口均大于 1024，无需 root / setcap。数据（SQLite、证书、cloudflared 二进制、日志）都在 `/var/lib/sb-ops` 下（`data_dir` 默认 `./data`，按 WorkingDirectory 落盘；如显式设置 `server.data_dir: /var/lib/sb-ops/data` 更直观）。

## 三、Nginx + 面板证书

```bash
sudo apt install -y nginx certbot python3-certbot-nginx
```

`/etc/nginx/sites-available/sb-ops`：

```nginx
server {
    listen 80;
    server_name panel.example.com;
    location / { proxy_pass http://127.0.0.1:8030; }   # 供 certbot http-01 临时使用
}

server {
    listen 443 ssl http2;
    server_name panel.example.com;

    ssl_certificate     /etc/letsencrypt/live/panel.example.com/fullchain.pem;
    ssl_certificate_key /etc/letsencrypt/live/panel.example.com/privkey.pem;

    client_max_body_size 16m;

    # 面板 UI 与管理 API
    location / {
        proxy_pass http://127.0.0.1:8030;
        proxy_http_version 1.1;
        proxy_set_header Host $host;
        proxy_set_header X-Real-IP $remote_addr;
        proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Proto $scheme;
    }

    # SSE 实时事件流：必须关缓冲、拉长超时，否则面板通知卡住
    location /api/v1/events {
        proxy_pass http://127.0.0.1:8030;
        proxy_http_version 1.1;
        proxy_set_header Connection "";
        proxy_buffering off;
        proxy_cache off;
        proxy_read_timeout 3600s;
        proxy_send_timeout 3600s;
    }
}
```

签发证书并启用：

```bash
sudo ln -s /etc/nginx/sites-available/sb-ops /etc/nginx/sites-enabled/
sudo nginx -t && sudo systemctl reload nginx
sudo certbot --nginx -d panel.example.com        # 自动改写 80 段为校验+跳转
```

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
4. 复制订阅链接到客户端，拉取验证。

### 协议证书（hysteria2 / tuic / anytls）

三选一：

| 方案 | 适用 | 操作 |
|---|---|---|
| **面板 ACME（推荐）** | 域名托管在 Cloudflare | `config.yaml` 配 `acme` 段（DNS-01，不开端口、不与协议端口冲突），面板自动签发续期；协议使用 `tlsMode: certificate` 指向自动管理证书 |
| 外部证书 | 已有证书/其它 CA | `deployment.tls_cert_path` / `tls_key_path` 指向证书文件，进程需可读 |
| 自签 + 跳过校验 | 仅 IP、快速验证 | 默认行为，客户端 `skip-cert-verify: true`（当前本地测试即此模式） |

Reality 协议（vless/trojan）不需要以上任何一种。

## 六、高级功能在 VPS 上的差异

- **WARP 出站 / AI 解锁**：功能同本地。VPS 直连 `api.cloudflareclient.com` 通常无障碍；「注册免费 WARP」或手工粘贴 wgcf 配置皆可。AI 解锁规则在「服务端配置 → 服务器分流」一键生成后即可用。
- **Argo 隧道**：cloudflared 二进制首次自动从 GitHub 下载到 `data/runtime/bin/cloudflared`（VPS 可直连 GitHub）。临时隧道（trycloudflare）重启换址、订阅自动重生成；生产建议固定域名 Token 模式。
- **DNS**：VPS 在境外时无污染问题，`singbox` DNS 可保持为空（用系统解析器）；无需本地测试时的国内 DoH 折中。

## 七、上线验证清单

```bash
systemctl status sb-ops                       # 运行中
curl -s http://127.0.0.1:8030/health/ready    # status":"ready"
sudo journalctl -u sb-ops -n 50               # 无 ERROR
```

- 浏览器 `https://panel.example.com` 打开面板、登录正常；
- 客户端拉取订阅，逐节点延迟测试通过；
- 访问 `gemini.google.com` 等并核对日志中出站是否为 `endpoint/wireguard[...]`（AI 解锁生效的标志）；
- Argo 节点（如有）在客户端可见且可连。

## 八、运维

**备份**（停机或低峰执行均可，SQLite WAL 下建议先停服务）：

```bash
sudo systemctl stop sb-ops
sudo tar czf sb-ops-backup-$(date +%F).tar.gz /var/lib/sb-ops /opt/sb-ops/config.yaml
sudo systemctl start sb-ops
```

**升级**：

```bash
make release VERSION=v1.0.1
sudo install -m 0755 dist/release/sb-ops-linux-amd64 /opt/sb-ops/sb-ops
sudo systemctl restart sb-ops
```

数据库迁移在启动时自动执行（幂等）。

**排障入口**：

| 现象 | 入口 |
|---|---|
| 内核同步失败、面板报「上一份成功配置仍在运行」 | 日志搜 `同步失败`；查 `runtime_state.failure_reason` |
| 面板通知/实时刷新不动 | Nginx SSE 段配置（`proxy_buffering off`） |
| 节点测不通 | 先看协议是否分配给用户、防火墙端口、再查日志该入站的认证错误 |
| WARP 出站不可用 | 构建是否带 `with_gvisor`（`sb-ops -h` 无此命令，看构建日志或重新 `make release`） |

## 九、已验证 VPS 部署实例

以下记录一套已经完成迁移验证的部署，供实际安装或故障排查时对照。敏感凭据（管理员密码、订阅 token、Cloudflare API Token、Argo Token、设备 UUID/password）不写入仓库，应通过密码管理器或安全终端保存。

### 服务器与目录

- 主机：Ubuntu 22.04，amd64，公网地址 `203.0.113.10`。
- 面板域名：`panel.example.com`，Cloudflare DNS 使用 A 记录指向 VPS，当前为 DNS only。
- Argo 节点域名：`sb.example.com`，由 Cloudflare Tunnel 转发到 VMess WebSocket 入站；不要把面板域名和 Argo 域名混用。
- 二进制：`/opt/sb-ops/sb-ops`。
- 运行用户：`sbops`（系统用户，不允许登录）。
- 数据目录：`/var/lib/sb-ops`；数据库为 `/var/lib/sb-ops/sb-ops.db`。
- 配置文件：`/opt/sb-ops/config.yaml`，配置路径固定为工作目录下的 `config.yaml`。
- systemd：`sb-ops.service`，运行在 `127.0.0.1:8030`，不直接暴露面板端口。

### Nginx 与 HTTPS

Nginx 配置文件为 `/etc/nginx/sites-available/sb-ops-panel`，启用链接位于 `/etc/nginx/sites-enabled/sb-ops-panel`。推荐配置结构如下：

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

    ssl_certificate /etc/letsencrypt/live/panel.example.com/fullchain.pem;
    ssl_certificate_key /etc/letsencrypt/live/panel.example.com/privkey.pem;

    client_max_body_size 16m;

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

生产环境可用 Certbot + Cloudflare DNS-01 签发证书：

```bash
sudo apt install -y certbot python3-certbot-dns-cloudflare
sudo install -d -m 0700 /root/.secrets/certbot
sudo chmod 600 /root/.secrets/certbot/cloudflare.ini
sudo certbot certonly --dns-cloudflare \
  --dns-cloudflare-credentials /root/.secrets/certbot/cloudflare.ini \
  --non-interactive --agree-tos -m you@example.com \
  -d panel.example.com
sudo nginx -t && sudo systemctl reload nginx
```

证书续期后必须 reload Nginx。可以放置 `/etc/letsencrypt/renewal-hooks/deploy/reload-nginx.sh`：

```sh
#!/bin/sh
systemctl reload nginx
```

验证续期：

```bash
sudo certbot renew --dry-run
```

### 实例协议端口

本次并行迁移使用新端口，避免与旧节点冲突。最终端口应以面板「协议」页和防火墙实际规则为准：

| 协议 | 端口 | 传输 / 证书 |
|---|---:|---|
| VLESS Reality | `10001/tcp` | TCP + Reality |
| VMess | `10002/tcp` | WebSocket |
| Hysteria2 | `10003/udp` | TLS 证书 |
| TUIC | `10004/udp` | TLS 证书 |
| AnyTLS | `10005/tcp` | TLS 证书 |
| Trojan | `10006/tcp` | TLS 证书 |
| Shadowsocks 2022 | `10007/tcp` | Shadowsocks 2022 |
| VLESS HTTP Reality | `10008/tcp` | HTTP transport + Reality |
| VLESS gRPC Reality | `10009/tcp` | gRPC + Reality |

`sb.example.com` 的 Argo Tunnel 转发到 `localhost:10002`，只用于 VMess WebSocket。Argo 节点不应把面板 `8030` 作为 ingress 目标；面板和订阅通过 `panel.example.com` 的 Nginx 访问。

### Argo 迁移

本地管理的旧 `cloudflared` 配置不能直接当作 sb-ops 的 token 模式配置。迁移顺序如下：

1. 备份旧容器中的 `tunnel.json` 与 `tunnel.yml`。
2. 停止旧容器的 cloudflared supervisor，避免同一个 Tunnel 同时连接两个不同的本地服务。
3. 在面板「Argo」中创建 token 模式隧道，绑定 VMess WebSocket 协议和固定域名。
4. 在 Cloudflare Tunnel 配置中写入 Public Hostname：`sb.example.com` → `http://localhost:10002`。
5. 等待 connector 状态为 running，再使用 VMess/WS 客户端测试，普通浏览器访问节点域名返回 404 属于正常现象。

不要使用 `pkill -f cloudflared` 清理进程：它可能匹配并杀掉 sb-ops 自己管理的 cloudflared。优先通过面板禁用隧道，或使用容器实际的 s6 服务目录停止旧 supervisor。

### ACME 证书与 DNS 传播

sb-ops 的面板 ACME 使用 Cloudflare DNS-01。配置示例：

```yaml
acme:
  email: "you@example.com"
  dns_provider: "cloudflare"
  api_token: "<通过密码管理器注入>"
```

签发证书前，应清理同一 `_acme-challenge.<domain>` 下残留的旧 TXT 记录；多个旧 challenge 值会使 Let's Encrypt 返回 `Incorrect TXT record`。Cloudflare DNS 记录写入后还需要等待传播，当前实现会轮询 TXT 记录，最长等待 60 秒，再提交 ACME challenge。

证书签发后，在「证书」页将证书绑定到 Hysteria2、TUIC、AnyTLS、Trojan 等 TLS 协议，并把 `serverName` 设置为证书域名。使用可信域名证书时，订阅不应设置 `skip-cert-verify: true`；IP 自签证书才保留跳过校验。

### 上线验证

```bash
systemctl status sb-ops nginx
curl -s http://127.0.0.1:8030/health/ready
curl -sI http://panel.example.com/
curl -sI https://panel.example.com/
printf '' | openssl s_client -connect panel.example.com:443 \\
  -servername panel.example.com 2>/dev/null | grep -E 'subject=|issuer=|Verify return code'
```

订阅应使用 HTTPS 面板域名：

```text
https://panel.example.com/subscribe/<subscription-token>/clash.yaml
```

浏览器访问 Argo 节点域名不是协议连通性测试；应使用对应客户端导入订阅，或用 sing-box CLI + 本地 mixed 入站执行出网测试。迁移期间先用新端口并行验证，确认订阅、协议、流量归因、额度扣减和 Argo 节点均正常后，再停止旧部署。
