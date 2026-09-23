// 审计动作、资源类型、告警类型的中文标签映射。
// 专有名词（sing-box / SOCKS / UUID 等）保留原文。

export const resourceTypeLabels: Record<string, string> = {
  user: '用户',
  protocol: '协议',
  certificate: '证书',
  policy: '策略',
  rule: '规则',
  singbox_config: '服务端配置',
  singbox_outbound: 'SOCKS 出站',
  singbox_route_rule: '服务器分流',
  singbox_runtime: '内核',
  singbox_connection: '实时连接',
  singbox: '服务端',
  traffic: '流量',
  alert: '告警',
  account: '账号',
  session: '会话',
  permission: '权限',
  system: '系统',
  system_upgrade: '面板升级',
  system_backup: '数据备份',
  database: '数据库',
  control_plane: '控制面',
  singbox_rule_set: '服务器规则集',
  argo_tunnel: 'Argo 隧道'
};

const auditSegmentLabels: Record<string, string> = {
  auth: '认证',
  user: '用户',
  protocol: '协议',
  certificate: '证书',
  policy: '策略',
  rule: '规则',
  setup: '初始化',
  runtime: '同步',
  singbox: '服务端',
  traffic: '流量',
  alert: '告警',
  create: '创建',
  update: '更新',
  delete: '删除',
  clone: '复制',
  order: '排序',
  login: '登录',
  logout: '登出',
  change: '修改',
  rotate: '轮换',
  reset: '重置',
  adjust: '调整',
  acknowledge: '确认',
  initialize: '初始化',
  sync: '同步',
  close: '断开',
  check: '检查',
  reconcile: '重新同步',
  restart: '重建',
  purge: '清理',
  password: '密码',
  credential: '凭据',
  subscription: '订阅',
  config: '配置',
  connection: '连接',
  outbound: '出站',
  secret: '密钥',
  argo: 'Argo',
  tunnel: '隧道',
  'route-rule': '分流规则',
  'rule-set': '规则集',
  'batch-status': '批量状态',
  acme: 'ACME',
  self_signed: '自签'
};

const auditActionOverrides: Record<string, string> = {
  'setup.admin.initialize': '初始化管理员',
  'runtime.sync': '同步配置',
  'singbox.config.update': '更新服务端配置',
  'singbox.connection.close': '断开实时连接',
  'singbox.runtime.check': '检查内核',
  'singbox.runtime.reconcile': '重新同步内核',
  'singbox.runtime.restart': '重建内核',
  'singbox.outbound.create': '创建 SOCKS 出站',
  'singbox.outbound.update': '更新 SOCKS 出站',
  'singbox.outbound.delete': '删除 SOCKS 出站',
  'auth.password.change': '修改密码',
  'user.credential.rotate': '轮换订阅凭据',
  'user.subscription.view': '查看订阅链接',
  'user.quota.adjust': '调整额度',
  'user.traffic.reset': '重置流量',
  'user.traffic.auto-reset': '周期重置流量（系统）',
  'alert.acknowledge': '确认告警',
  'protocol.secret.rotate': '轮换协议密钥',
  'policy.clone': '复制策略',
  'protocol.clone': '复制协议',
  'rule.order': '排序规则',
  'auth.login': '登录',
  'auth.logout': '登出',
  'system.upgrade.check': '检查面板更新',
  'system.upgrade.start': '开始面板升级',
  'system.backup.create': '创建数据备份',
  'system.backup.download': '下载数据备份',
  'system.backup.delete': '删除数据备份'
};

export function auditActionLabel(action: string): string {
  if (auditActionOverrides[action]) return auditActionOverrides[action];
  const segments = action.split('.');
  if (segments.length === 1) return auditSegmentLabels[segments[0]] || action;
  const resource = auditSegmentLabels[segments[0]];
  const verb = segments.slice(1).map((segment) => auditSegmentLabels[segment] || segment).join('·');
  return resource ? `${resource}·${verb}` : verb;
}

export function auditResourceLabel(resourceType: string): string {
  return resourceTypeLabels[resourceType] || resourceType;
}

export const alertTypeLabels: Record<string, string> = {
  runtime_sync_failed: '配置同步失败',
  database_capacity: '数据库容量预警',
  aggregation_failed: '流量聚合失败',
  traffic_stalled: '流量采集停滞',
  protocol_probe_failed: '协议链路拨测失败',
  certificate_expiring: '证书即将到期',
  vps_bandwidth_high: 'VPS 月流量用量过高',
  vps_bandwidth_stalled: 'VPS 月流量计量停滞',
  upgrade_available: '面板有新版本',
  backup_failed: '定时备份失败',
  egress_geo_misjudged: 'Google 出口判定异常',
  warp_masque_degraded: 'WARP 解锁链路异常'
};

export function alertTypeLabel(type: string): string {
  return alertTypeLabels[type] || type;
}

export const resultLabels: Record<string, string> = {
  success: '成功',
  failure: '失败',
  denied: '拒绝'
};

export const runtimeStatusLabels: Record<string, string> = {
  running: '运行中',
  unhealthy: '需要处理',
  not_configured: '待配置'
};

export const configStatusLabels: Record<string, string> = {
  not_configured: '尚未配置',
  pending: '等待首次同步',
  applied: '已应用',
  not_checked: '未检查'
};

export const kernelStatusLabels: Record<string, string> = {
  running: '健康',
  unhealthy: '健康检查失败',
  not_checked: '尚未检查'
};

export function configStatusLabel(value: string): string {
  return configStatusLabels[value] || value;
}

export function kernelStatusLabel(value: string): string {
  return kernelStatusLabels[value] || value;
}

// 审计过滤下拉的值清单，对齐 api/audit_actions.go 注册表（契约：只增不改，
// 新增动作时同步这里）。user.device.list 是已废弃的读操作审计，不再产生
// 新记录，不进下拉。
export const auditResourceTypeValues = [
  'account', 'session', 'user', 'protocol', 'certificate', 'policy', 'rule',
  'singbox_config', 'singbox_outbound', 'singbox_route_rule', 'singbox_rule_set',
  'singbox_runtime', 'singbox_connection', 'argo_tunnel', 'system_upgrade', 'system_backup', 'alert'
];

export const auditActionValues = [
  'setup.admin.initialize',
  'auth.login', 'auth.logout', 'auth.password.change',
  'user.create', 'user.update', 'user.delete', 'user.batch-status',
  'user.traffic.reset', 'user.quota.adjust', 'user.traffic.auto-reset',
  'user.subscription.rotate',
  'user.device.update', 'user.device.delete', 'user.device.rotate',
  'protocol.create', 'protocol.update', 'protocol.delete', 'protocol.clone', 'protocol.secret.rotate',
  'certificate.create', 'certificate.update', 'certificate.delete',
  'certificate.self_signed.create', 'certificate.self_signed.regenerate',
  'certificate.acme.create', 'certificate.acme.renew',
  'policy.create', 'policy.update', 'policy.delete', 'policy.clone',
  'rule.create', 'rule.update', 'rule.delete', 'rule.order',
  'singbox.config.update',
  'singbox.outbound.create', 'singbox.outbound.update', 'singbox.outbound.delete',
  'singbox.route-rule.create', 'singbox.route-rule.update', 'singbox.route-rule.delete', 'singbox.route-rule.order',
  'singbox.rule-set.create', 'singbox.rule-set.update', 'singbox.rule-set.delete',
  'singbox.runtime.check', 'singbox.runtime.reconcile', 'singbox.runtime.restart',
  'singbox.connection.close',
  'argo.tunnel.create', 'argo.tunnel.update', 'argo.tunnel.delete', 'argo.tunnel.restart',
  'runtime.sync',
  'system.upgrade.check', 'system.upgrade.start',
  'system.backup.create', 'system.backup.download', 'system.backup.delete',
  'alert.acknowledge'
];
