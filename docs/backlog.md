# 待办与路线图

当前没有排期的 backlog 项。

## 已定方向（按优先级）

1. **geoip/geosite 路由条件模型清理**：sing-box 1.12 已移除 geoip/geosite 数据库，路由条件模型、面板 API 与归一化逻辑仍保留这两个字段（前端早已不暴露，纯 API 遗留）；编译期已兜底拒绝带此类条件的规则（防内核启动直接失败），后续可整体删除模型字段与 `normalizeGeoValues` 等配套逻辑。
2. **rule-set `download_detour` → `http_client` 迁移**：上游 1.14 弃用（仍可用、打警告）、1.16 移除；届时编译器与引用校验（四类引用清单）需加 `http_client` 类别。
3. **WARP MASQUE 解锁链路收编 Phase 2——一键换出口**（Phase 1 健康监控已于 2026-09-24 落地，见已完成）：warp-cli 在 Linux 必须 root 而面板跑 sbops 用户，部署文档加 sudoers 白名单一行（`disconnect`/`connect` 两命令 NOPASSWD）；API `POST /system/warp/rotate` 执行轮换并立即复测返回新判定，未配 sudoers 时 409/总览卡片按钮置灰；审计 `system.warp.rotate` 照 backup 先例 `h.audit` 直记（不接 mutate，不涉内核重编译）。设计约束（2026-09-24 定）：不内嵌 MASQUE 协议实现、不把官方客户端收进面板二进制（官方 warp-svc 是唯一正源）；面板不做 apt 安装引导；不做全自动换出口（有闪断副作用，只做手动按钮）。

## 已完成（近期）

- **WARP MASQUE 解锁链路健康监控 Phase 1**（2026-09-24，随下版发布）：`internal/warphealth` 周期探测（15min，经 warp-masque socks 出站校验 trace `warp=on`/出口族 + Google GET 地区判定，GET 不用 HEAD）+ `warp_masque_degraded` 告警 + 总览「WARP 解锁链路」卡片；仅当存在 warp-masque 出站才启用，未部署实例静默；歧义失败粘滞上一判定防抖。
- **Google 出口判定监控**（2026-09-23，随下版发布）：`internal/egressgeo` 周期探测 + `egress_geo_misjudged` 告警 + 总览卡片。VPS 上的临时 cron 监控三件套（cron.d / 脚本 / state）已于 2026-09-23 提前删除（用户拍板，接受下版部署前的监控空窗）。
