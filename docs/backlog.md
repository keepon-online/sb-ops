# 待办与路线图

当前没有排期的 backlog 项。

## 已定方向（按优先级）

1. **geoip/geosite 路由条件模型清理**：sing-box 1.12 已移除 geoip/geosite 数据库，路由条件模型、面板 API 与归一化逻辑仍保留这两个字段（前端早已不暴露，纯 API 遗留）；编译期已兜底拒绝带此类条件的规则（防内核启动直接失败），后续可整体删除模型字段与 `normalizeGeoValues` 等配套逻辑。
2. **rule-set `download_detour` → `http_client` 迁移**：上游 1.14 弃用（仍可用、打警告）、1.16 移除；届时编译器与引用校验（四类引用清单）需加 `http_client` 类别。

## 已完成（近期）

- **Google 出口判定监控**（2026-09-23，随下版发布）：`internal/egressgeo` 周期探测 + `egress_geo_misjudged` 告警 + 总览卡片。生产部署本功能后需**删除 VPS 上的临时 cron 监控**（`/etc/cron.d/sb-google-verdict-watch`、`/usr/local/bin/sb-google-verdict-watch`、`/var/lib/sb-ops/google-verdict.state`），避免与面板告警重复推送。
