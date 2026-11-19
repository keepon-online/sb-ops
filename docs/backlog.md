# 待办与路线图

当前没有排期的 backlog 项。

## 已定方向（按优先级）

1. **geoip/geosite 路由条件模型清理**：sing-box 1.12 已移除 geoip/geosite 数据库，路由条件模型、面板 API 与归一化逻辑仍保留这两个字段（前端早已不暴露，纯 API 遗留）；编译期已兜底拒绝带此类条件的规则（防内核启动直接失败），后续可整体删除模型字段与 `normalizeGeoValues` 等配套逻辑。
2. **rule-set `download_detour` → `http_client` 迁移**：上游 1.14 弃用（仍可用、打警告）、1.16 移除；届时编译器与引用校验（四类引用清单）需加 `http_client` 类别。

## 已完成（近期）

- **hysteria2 salamander 混淆（对抗封锁，直连 A 级补短板）**（2026-11-19）：hy2 是两轮 TCP 封锁中直连唯一存活路径（UDP 免疫），但裸 QUIC 指纹可被 DPI 主动识别。协议表单新增 `obfsPassword`（security 组、secret 脱敏、留空=关）：服务端入站与订阅同构启用 salamander（sing-box `obfs` 结构体段 / mihomo 扁平 `obfs`+`obfs-password`），拨测镜像同构；字段对照 v1.14.3 `Hysteria2Obfs` 结构体核验，dump fixture 两侧补齐、`sing-box check` 全过，订阅方言交 CI mihomo 门禁。
- **vmess argoOnly 入站回环收口（对抗封锁补全，v0.8.1 之后）**（2026-11-19）：背景=IP 被 GFW 定向封锁 TCP（2026-10-30 与 11-17 两轮，两轮后已解封），v0.8.1 的 argoOnly 已隐藏订阅直连节点，本次补另一半——argoOnly 入站服务端编译时 listen 强制 `127.0.0.1`（公网端口关闭，明文 ws 指纹对主动探测彻底消失；cloudflared 回源与 probe 拨测本就打回环不受影响）；`Input.Validate` 兜底「argoOnly 无绑定启用隧道」拒绝编译（v0.8.1 注记的「保存时校验」以编译期校验落地——只看配置意图、不锁「先建协议再建隧道」顺序，运行态归 argo manager）。
- **WARP 服务重启按钮 + warp-svc 预防性周重启**（2026-11-16，随下版发布）：cloudflare-warp warp-svc 半死/崩溃循环缺陷跨版本存在（10-21 与 11-16 两次生产实例，上游 apt 源全线无更新版本、无修复说明），且半死形态下 warp-cli IPC 一并超时——换出口注定失败，唯一恢复手段是 `systemctl restart warp-svc`。①面板总览「WARP 解锁链路」卡片新增「重启服务」按钮：`POST /system/warp/restart`（业务码 4012/4013，审计 `system.warp.restart`，root 直执行 systemctl + 宽限窗口滚动复测，复测循环与换出口共享 `probeUntilTerminal`——换出口/重启的错误映射同样共用 `writeWarpActionError`）；②运维侧预防性周重启 systemd timer（周日 05:00 本地时区，避开 04:00 每日备份；单元文件与安装步骤入 docs/deployment.md，生产已装、下次触发 2026-11-22）。设计约束保持：不做全自动按需重启（闪断副作用先例），timer 仅预防性低峰重启。
- **换出口适配新版 warp-cli TOS 门禁**（2026-11-16，随下版发布）：cloudflare-warp 2026.8.2115.0 起未接受 TOS 的 `warp-cli` 调用一律本地拒绝（`Please accept the WARP Terms of Service…`，检查发生在 IPC 之前），换出口路径的 `runWarpCli`（ops_app.go）没带 `--accept-tos` 导致面板换出口必失败——纳管 configure 序列（manager.go）当时已带、唯独此处漏。统一前置 flag 修复；同日生产 warp-svc 崩溃循环（10-21 半死同形态二次复发）由 restart 恢复，64db878 滑动窗口兜底实证正确触发 `warp_masque_degraded`。
- **订阅方言 CI 门禁 + 默认规则悬空引用修复**（2026-10-27，随下版发布）：CI 新增 `subscription-dialect` job——`SB_SUB_DUMP` 导出全 7 协议 clash.yaml 后跑 `mihomo -t`（钉版 v1.19.33 compatible）深度校验，是 `SB_CHECK_DUMP` + `sing-box check` 在订阅侧的对应物；mihomo 升级波次时随 `MIHOMO_VERSION` 同步。门禁首次实弹即捕获 `defaultClashRules` 的 `RULE-SET,cn-domains` 悬空引用（provider 表无此名、上游 .mrs 已 404，未配订阅分流策略的默认订阅会被 mihomo 整份拒载）——已删悬空行。
- **订阅 DNS 段自足化（TUN 兼容加固）**（2026-10-27，随下版发布）：Clash Verge Rev 2.5.4+ 的「DNS 覆写」开关会被检测到订阅自带 dns 段时自动关闭/重置（上游 #7986/#8122/#8207），而此前订阅 DNS 段不自足——缺 `default-nameserver`（域名 DoH 无法引导解析，TUN 下系统 DNS 又被内核接管形成死锁）、缺 `fake-ip-filter`（mDNS/NTP/内网管理页被分配 fake-ip 落进代理）、`nameserver-policy` 用逗号组合 key（新内核不保证逐条解析）。现 `clashDNS()` 补齐明文引导上游（223.5.5.5/119.29.29.29，同时用于 `proxy-server-nameserver`）、标准 fake-ip 排除清单、policy key 拆为独立条目；客户端覆写开关任何状态下 TUN 均可用。
- **WARP 链路告警滑动窗口兜底**（2026-10-21，随下版发布）：`warphealth` state 增加最近 8 轮探测滑动窗口，失败 ≥6 轮即按 degraded 告警——warp-svc 半死的时通时断形态下间歇成功会不断洗掉 stale 计时（当日线上实例 5.5h 零告警），且「尚无成功判定」的启动即死形态此前完全不评估，同受窗口兜底。运维注记：warp-svc 半死时 `warp-cli` IPC 一并超时，面板「换出口」会失败，须 `systemctl restart warp-svc`。
- **部署简化批次**（2026-09-28）：①面板自带 TLS 直听（ACME 自动证书 + 续期热载），Nginx/certbot 退役；②ufw 移除。发布后生产需按 deployment.md 迁移（停 Nginx、config 加 tls_listen/redirect_listen、重启验证）；建议同步把订阅域名橙云降灰云（设备归因恢复真实 IP）。
- **WARP 客户端面板纳管 + root 部署**（2026-09-25）：系统管理页一键安装/重新配置官方 cloudflare-warp 客户端（阶段机 + 固定命令枚举），换出口去 sudo 化，部署文档 root 化并附迁移步骤。发布后生产需按文档迁移 systemd 单元（root + 去 ProtectSystem）并删除旧 sudoers。
- **WARP 注册出口预检**（2026-09-24）：注册免费 WARP 身份后即时实测出口 IP 与 Google 判定并随注册响应展示（判 CN 引导改用 warp-masque）；AI 解锁选中 warp (WireGuard) 出站时给适用性提示。免费 wireguard 出站的定位 =「零依赖通用换出口（DC 声誉 / IPv6）」，非 Google 解锁（该场景由 MASQUE 承担）。
- **WARP 解锁链路一键换出口**（2026-09-24，随下版发布）：`POST /api/v1/system/warp/rotate`（业务码 4006/4007）+ 总览卡片按钮 + sudoers 白名单指引（仅放行 disconnect/connect，`/etc/sudoers.d/sb-ops-warp`，440）+ `system.warp.rotate` 审计；`internal/warphealth.Rotate` 注入 sudo runner（包内不 exec，可测），复测滚动调 Check、终态即时驱动告警与总览；未部署出站 404、sudoers 未放行 409 带配置指引。设计约束：不内嵌 MASQUE 协议实现、面板不做 apt 安装引导、不做全自动换出口（闪断副作用，只做手动按钮）。

- **WARP 解锁链路一键换出口**（2026-09-24，随下版发布）：`POST /api/v1/system/warp/rotate`（业务码 4006/4007）+ 总览卡片按钮 + sudoers 白名单指引 + `system.warp.rotate` 审计；`internal/warphealth.Rotate` 注入 sudo runner（包内不 exec，可测），复测滚动调 Check、终态即时驱动告警与总览。
- **WARP MASQUE 解锁链路健康监控 Phase 1**（2026-09-24，随下版发布）：`internal/warphealth` 周期探测（15min，经 warp-masque socks 出站校验 trace `warp=on`/出口族 + Google GET 地区判定，GET 不用 HEAD）+ `warp_masque_degraded` 告警 + 总览「WARP 解锁链路」卡片；仅当存在 warp-masque 出站才启用，未部署实例静默；歧义失败粘滞上一判定防抖。
- **Google 出口判定监控**（2026-09-23，随下版发布）：`internal/egressgeo` 周期探测 + `egress_geo_misjudged` 告警 + 总览卡片。VPS 上的临时 cron 监控三件套（cron.d / 脚本 / state）已于 2026-09-23 提前删除（用户拍板，接受下版部署前的监控空窗）。
