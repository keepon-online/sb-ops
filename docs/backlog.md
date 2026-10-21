# 待办与路线图

当前没有排期的 backlog 项。

## 已定方向（按优先级）

1. **geoip/geosite 路由条件模型清理**：sing-box 1.12 已移除 geoip/geosite 数据库，路由条件模型、面板 API 与归一化逻辑仍保留这两个字段（前端早已不暴露，纯 API 遗留）；编译期已兜底拒绝带此类条件的规则（防内核启动直接失败），后续可整体删除模型字段与 `normalizeGeoValues` 等配套逻辑。
2. **rule-set `download_detour` → `http_client` 迁移**：上游 1.14 弃用（仍可用、打警告）、1.16 移除；届时编译器与引用校验（四类引用清单）需加 `http_client` 类别。

## 已完成（近期）

- **WARP 链路告警滑动窗口兜底**（2026-10-21，随下版发布）：`warphealth` state 增加最近 8 轮探测滑动窗口，失败 ≥6 轮即按 degraded 告警——warp-svc 半死的时通时断形态下间歇成功会不断洗掉 stale 计时（当日线上实例 5.5h 零告警），且「尚无成功判定」的启动即死形态此前完全不评估，同受窗口兜底。运维注记：warp-svc 半死时 `warp-cli` IPC 一并超时，面板「换出口」会失败，须 `systemctl restart warp-svc`。
- **部署简化批次**（2026-09-28）：①面板自带 TLS 直听（ACME 自动证书 + 续期热载），Nginx/certbot 退役；②ufw 移除。发布后生产需按 deployment.md 迁移（停 Nginx、config 加 tls_listen/redirect_listen、重启验证）；建议同步把订阅域名橙云降灰云（设备归因恢复真实 IP）。
- **WARP 客户端面板纳管 + root 部署**（2026-09-25）：系统管理页一键安装/重新配置官方 cloudflare-warp 客户端（阶段机 + 固定命令枚举），换出口去 sudo 化，部署文档 root 化并附迁移步骤。发布后生产需按文档迁移 systemd 单元（root + 去 ProtectSystem）并删除旧 sudoers。
- **WARP 注册出口预检**（2026-09-24）：注册免费 WARP 身份后即时实测出口 IP 与 Google 判定并随注册响应展示（判 CN 引导改用 warp-masque）；AI 解锁选中 warp (WireGuard) 出站时给适用性提示。免费 wireguard 出站的定位 =「零依赖通用换出口（DC 声誉 / IPv6）」，非 Google 解锁（该场景由 MASQUE 承担）。
- **WARP 解锁链路一键换出口**（2026-09-24，随下版发布）：`POST /api/v1/system/warp/rotate`（业务码 4006/4007）+ 总览卡片按钮 + sudoers 白名单指引（仅放行 disconnect/connect，`/etc/sudoers.d/sb-ops-warp`，440）+ `system.warp.rotate` 审计；`internal/warphealth.Rotate` 注入 sudo runner（包内不 exec，可测），复测滚动调 Check、终态即时驱动告警与总览；未部署出站 404、sudoers 未放行 409 带配置指引。设计约束：不内嵌 MASQUE 协议实现、面板不做 apt 安装引导、不做全自动换出口（闪断副作用，只做手动按钮）。

- **WARP 解锁链路一键换出口**（2026-09-24，随下版发布）：`POST /api/v1/system/warp/rotate`（业务码 4006/4007）+ 总览卡片按钮 + sudoers 白名单指引 + `system.warp.rotate` 审计；`internal/warphealth.Rotate` 注入 sudo runner（包内不 exec，可测），复测滚动调 Check、终态即时驱动告警与总览。
- **WARP MASQUE 解锁链路健康监控 Phase 1**（2026-09-24，随下版发布）：`internal/warphealth` 周期探测（15min，经 warp-masque socks 出站校验 trace `warp=on`/出口族 + Google GET 地区判定，GET 不用 HEAD）+ `warp_masque_degraded` 告警 + 总览「WARP 解锁链路」卡片；仅当存在 warp-masque 出站才启用，未部署实例静默；歧义失败粘滞上一判定防抖。
- **Google 出口判定监控**（2026-09-23，随下版发布）：`internal/egressgeo` 周期探测 + `egress_geo_misjudged` 告警 + 总览卡片。VPS 上的临时 cron 监控三件套（cron.d / 脚本 / state）已于 2026-09-23 提前删除（用户拍板，接受下版部署前的监控空窗）。
