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
