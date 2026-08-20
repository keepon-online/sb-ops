# 产品优化 Backlog

> 场景：单人管理员（自己）+ 分享订阅链接给用户/朋友/家人。
> 只保留待办；已完成的功能与修复见 [CHANGELOG.md](../CHANGELOG.md)。

## 待办

### 可选优化

- [ ] **secret 主密钥外部化（A13）**：主密钥明文与密文同库（`internal/service/secret/store.go`），对整库被拿走零防护、无版本化。支持可选环境变量 / 文件注入外部 master key。
- [ ] **审计写所有权收敛（A5 遗留）**：`audit_logs` 表 identity 写 / audit 读两套模型靠复制维持，写所有权收敛待做。

### 测试补强

- [ ] **handler 层测试盲区（A15）**：users / traffic / overview 的 handler 无专项测试；后台任务（留存 / 续期 / 优雅停机）与配置 reload 分类无测试。

### 明确不做

- **iptables 端口跳跃事务性**：`reconcilePortHop` 保持 best-effort（失败仅记日志、每次 sync 全量重建规则可自愈半配置状态）；真正原子化需 `iptables-restore` 整表替换，风险高于收益，且无 CI 测试环境。
