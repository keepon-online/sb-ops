# 变更记录

本文件记录 sb-ops 的版本变更。格式参考 [Keep a Changelog](https://keepachangelog.com/zh-CN/)。

## [Unreleased]

### 修复

- **Clash 连接入站标签解析**：sing-box v1.13 的 `/connections` 无独立 `inbound` 字段（入站信息在 `metadata.type`，格式 `类型/tag`），改为从 `type` 提取——修复协议页连接归因恒为 0 与「实时连接」入站列为空。

## [0.1.0] - 2026-08-19

初始版本：单一 Go 进程内嵌 sing-box 内核与 Web 管理控制台，面向个人使用的单 VPS 部署。

### 新增

- **单管理员面板**：首次运行 `/setup` 创建管理员；登录 / 会话 / 修改密码；全部管理操作留痕审计日志；无多账号与角色权限体系。
- **用户与设备**：建号即得一条用户级订阅链接（token 可重置），产物为 `clash.yaml` 一种格式；凭据按「设备」维度管理——客户端导入时按订阅 UA 指纹自动发现设备（独立凭据、平台与客户端识别、最近活跃），上限 10 台，支持手动添加 / 停用 / 轮换 / 删除与导入二维码。
- **协议与证书**：vless（含 Reality）/ vmess / trojan / hysteria2 / tuic / anytls / shadowsocks 2022 七种协议；自签证书（IP / DNS）与 ACME 证书管理；资源保存即编译 `sb.json` 并热重建内嵌内核，失败自动回滚上一份成功配置。
- **流量与计费**：V2Ray stats gRPC 按设备 user-tag 归因流量并汇总到用户，驱动 GB 额度扣减与到期 / 超量阻断；Clash API 用于实时连接观测；历史按小时 / 天聚合，保留期可配并自动清理。
- **两套分流**：「订阅分流」写入单用户的 Clash 订阅（只影响该用户客户端），「服务器分流」作用于内核转发（影响所有用户）；规则库内置预设（广告 / AI / 流媒体 / 社交 / 开发者 / 国内直连等），订阅分流支持 dry-run 命中测试（内置远程规则集源与本地缓存匹配）。
- **观测与告警**：服务器监控（CPU / 内存 / 磁盘 / 网络指标与趋势，gopsutil）；流量采集自检（`traffic_stalled` 告警与 readiness 联动）；告警外发 Webhook / Telegram（`config.yaml` 配置，未配置静默跳过）。
- **工程基线**：管理 API 统一响应信封 `{code, message, data, requestId}` 与字段级中文校验消息（`internal/httpx`）；OpenAPI 契约驱动前端类型生成（`make api`）；golangci-lint v2 + tsc / vitest / redocly 的 CI 门禁。
