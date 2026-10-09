# 域名购买与 DNS 托管

一句话结论：**注册商推荐 [Spaceship](https://www.spaceship.com/zh/)，解析托管交给 Cloudflare（免费）**。注册商只决定域名归属与续费成本，解析托管与注册商无关、随时可迁——而面板 ACME 的 DNS-01 签发只支持 Cloudflare DNS API，所以无论在哪买域名，解析最终都应落在 Cloudflare（衔接部署指南「面板自带 TLS」一章）。

## 为什么需要域名

- **面板 TLS**：`server.tls_listen` 的证书按 `deployment.panel_domain` 经 ACME DNS-01 自动签发与续期；
- **订阅链接**：客户端导入的是 `https://<域名>/subscribe/...`，可信域名证书让订阅免于「跳过证书校验」；
- **TLS 协议证书**：hysteria2 / tuic / anytls / trojan(TLS) 使用可信证书时，需要一个归你所有的域名；
- **换 VPS 不换订阅**：迁移服务器只改一条 A 记录，所有客户端的订阅链接保持不变。

## 推荐注册商：Spaceship

[Spaceship](https://www.spaceship.com/zh/) 是 Namecheap 团队 2023 年推出的注册商品牌，ICANN 认证。对本项目的部署者，关键点：

| 关注点 | 情况 |
| --- | --- |
| WHOIS 隐私 | **免费**提供，注册人信息不进公开 WHOIS |
| 界面 / 客服 | 中文界面，注册续费流程清爽、少 upsell |
| 价格 | 首年常有促销；**下单前核对续费价**（任何注册商同理，首年价不代表长期成本） |
| 支付方式 | 以结账页当前显示为准，不在本文列举 |

对一个暴露在公网、与代理服务关联的域名来说，免费 WHOIS 隐私是默认就该有的保护——这是把 Spaceship 列为首选的主要原因，价格与体验只是不输同行。

注册商完全可替换：Namecheap / Porkbun / Namesilo 等同类均可，选择原则不变——**隐私保护免费、续费价合理**；解析反正要迁去 Cloudflare，不必为注册商自带的解析托管付费。两点补充：

- 国内注册商（阿里云/腾讯云）要求域名实名认证、注册人信息入库；对隐私敏感的部署通常选境外注册商。
- Cloudflare Registrar 按注册局成本价续费、无溢价，但**只支持转入不支持新注册**——域名已托管在 Cloudflare 的可考虑转入省一点续费。

## 买完之后：解析托管到 Cloudflare

1. **Cloudflare 添加站点**：<https://dash.cloudflare.com> → Add a site → 输入域名 → 选 Free 套餐，Cloudflare 会分配两条 nameserver（形如 `xxx.ns.cloudflare.com`）。
2. **回注册商改 NS**：Spaceship 域名管理 → Nameservers → 替换为 Cloudflare 分配的两条。NS 生效从几分钟到数小时不等，Cloudflare 站点页显示 Active 即完成。
3. **建 A 记录（灰云）**：`panel.example.com` → VPS 公网 IP，代理状态选 **DNS only（灰云）**——橙云代理与面板直听不配套，原因见部署指南「Cloudflare 橙云」要点。
4. **建 API Token**：Cloudflare → My Profile → API Tokens → Create Token，权限只给 `Zone → DNS → Edit`（作用域可收窄到该域名），填入 `config.yaml` 的 `acme.api_token`。

完成后回到部署指南「面板自带 TLS」继续配置。

## 常见问题

- **要备案吗？** 不需要。面板与协议都部署在境外 VPS，域名不解析到境内服务器，无备案义务。
- **能不买域名、直接用 IP 吗？** 能跑：订阅与协议直接用 IP，证书走自签（`deployment.tls_cert_path` 指向自签证书，客户端 `skip-cert-verify: true`）。代价是每个客户端都要关闭证书校验，且换 VPS 必须重发全部订阅。域名年成本通常几十元人民币量级，建议直接买。
- **续费与转移**：NS 已指向 Cloudflare 的情况下，在注册商续费、甚至转移到其它注册商，都不影响解析。域名到期后短期内通常仍可原价续费，但别依赖宽限期——开启自动续费或设个日历提醒。
