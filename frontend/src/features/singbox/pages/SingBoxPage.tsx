import {
  ArrowDownOutlined,
  ArrowUpOutlined,
  DeleteOutlined,
  EditOutlined,
  PlusOutlined,
  ReloadOutlined
} from '@ant-design/icons';
import { useMutation, useQuery } from '@tanstack/react-query';
import {
  Alert,
  App as AntApp,
  Button,
  Drawer,
  Empty,
  Form,
  Input,
  InputNumber,
  Popconfirm,
  Descriptions,
  Result,
  Segmented,
  Select,
  Space,
  Switch,
  Table,
  Tabs,
  Tag,
  Tooltip,
  Typography
} from 'antd';
import type { ColumnsType } from 'antd/es/table';
import { useEffect, useMemo, useState } from 'react';
import { useLocation, useNavigate } from 'react-router-dom';
import { Page } from '../../../components/Page';
import { ApiError } from '../../../services/http';
import { configStatusLabel, kernelStatusLabel } from '../../../util/labels';
import { queryClient } from '../../../services/queryClient';
import { protocolsApi } from '../../../services/protocols';
import {
  singBoxApi,
  type ArgoTunnel,
  type SingBoxConnection,
  type SingBoxConfig,
  type SingBoxDryRunResult,
  type SingBoxOutbound,
  type SingBoxRouteConditions,
  type SingBoxRouteRule,
  type SingBoxRuleSet,
  type SingBoxValidationResult
} from '../../../services/singbox';

type ConfigValues = Omit<SingBoxConfig, 'revision' | 'updatedAt' | 'clashApiSecretSet'> & { clashApiSecret?: string };
type WarpValues = { endpoint?: string; publicKey: string; privateKey?: string; ipv4: string; ipv6?: string; reserved?: string; mtu?: number };
type OutboundValues = { type?: 'socks' | 'warp'; tag: string; server?: string; serverPort?: number; username?: string; password?: string; warp?: WarpValues };
type ArgoValues = { name: string; enabled: boolean; mode: 'temporary' | 'token'; fixedDomain?: string; preferredAddress?: string; protocolId: string; token?: string };
type RuleValues = {
  name: string; enabled: boolean; priority: number; outboundTag: string;
  domains: string[]; domainSuffixes: string[]; ipCidrs: string[]; ports: string[]; networks: Array<'tcp' | 'udp'>; inboundTags: string[];
  geoip: string[]; geosite: string[]; ruleSets: string[];
};
type RuleSetValues = { tag: string; format: 'source' | 'binary'; url: string; downloadDetour?: string };
type DryRunValues = { host: string; port?: number; network?: 'tcp' | 'udp'; inboundTag?: string };

const logLevelOptions = ['trace', 'debug', 'info', 'warn', 'error', 'fatal', 'panic'].map((value) => ({ value }));

// 常用 geoip/geosite 代码，供服务器分流规则的 GeoIP/GeoSite 字段下拉选择。
// sing-box 内建 geo 数据库支持任意国家码/类别，故仍保留自由输入。
const geoIPOptions = [
  ['private', '私网/局域网'], ['cn', '中国'], ['hk', '香港'], ['tw', '台湾'], ['mo', '澳门'],
  ['sg', '新加坡'], ['jp', '日本'], ['kr', '韩国'], ['us', '美国'], ['gb', '英国'],
  ['de', '德国'], ['fr', '法国'], ['ru', '俄罗斯'], ['ca', '加拿大'], ['au', '澳大利亚']
].map(([value, label]) => ({ value, label: `${label} (${value})` }));
const geoSiteOptions = [
  ['category-ads-all', '广告拦截'], ['google', '谷歌'], ['github', 'GitHub'], ['openai', 'OpenAI'],
  ['telegram', '电报'], ['geolocation-cn', '中国站点'], ['geolocation-!cn', '非中国站点'],
  ['category-ai-!cn', 'AI 服务'], ['netflix', 'Netflix'], ['youtube', '油管'], ['microsoft', '微软'],
  ['apple', '苹果'], ['facebook', 'Facebook'], ['twitter', 'X/Twitter'], ['tiktok', 'TikTok'],
  ['spotify', 'Spotify'], ['bilibili', '哔哩哔哩'], ['steam', 'Steam']
].map(([value, label]) => ({ value, label: `${label} (${value})` }));

function formatBytes(value: number) {
  const units = ['B', 'KB', 'MB', 'GB', 'TB'];
  let current = value;
  let index = 0;
  while (current >= 1024 && index < units.length - 1) { current /= 1024; index += 1; }
  return `${current.toFixed(index === 0 ? 0 : 2)} ${units[index]}`;
}

const tabGroups: Array<{ key: string; label: string; tabs: string[] }> = [
  { key: 'ops', label: '运维与观测', tabs: ['runtime', 'connections'] },
  { key: 'config', label: '内核配置', tabs: ['base', 'dns', 'outbounds', 'argo', 'routes', 'rulesets'] }
];

export function SingBoxPage() {
  const { message, notification } = AntApp.useApp();
  const location = useLocation();
  const navigate = useNavigate();
  const [tab, setTab] = useState(() => new URLSearchParams(location.search).get('tab') || 'runtime');
  const activeGroup = tabGroups.find((group) => group.tabs.includes(tab)) ?? tabGroups[0];
  const selectTab = (key: string) => {
    setTab(key);
    navigate(`/singbox${key === 'runtime' ? '' : `?tab=${key}`}`, { replace: true });
  };
  const selectGroup = (key: string) => selectTab((tabGroups.find((group) => group.key === key) ?? tabGroups[0]).tabs[0]);
  const [configForm] = Form.useForm<ConfigValues>();
  const [outboundForm] = Form.useForm<OutboundValues>();
  const [argoForm] = Form.useForm<ArgoValues>();
  const [ruleForm] = Form.useForm<RuleValues>();
  const [ruleSetForm] = Form.useForm<RuleSetValues>();
  const [dryRunForm] = Form.useForm<DryRunValues>();
  const [outboundDrawer, setOutboundDrawer] = useState(false);
  const [argoDrawer, setArgoDrawer] = useState(false);
  const [ruleDrawer, setRuleDrawer] = useState(false);
  const [ruleSetDrawer, setRuleSetDrawer] = useState(false);
  const [editingOutbound, setEditingOutbound] = useState<SingBoxOutbound | null>(null);
  const [editingArgo, setEditingArgo] = useState<ArgoTunnel | null>(null);
  const [aiOutbound, setAiOutbound] = useState<string | undefined>(undefined);
  const [editingRule, setEditingRule] = useState<SingBoxRouteRule | null>(null);
  const [editingRuleSet, setEditingRuleSet] = useState<SingBoxRuleSet | null>(null);
  const [dryRunResult, setDryRunResult] = useState<SingBoxDryRunResult | null>(null);
  const [validation, setValidation] = useState<SingBoxValidationResult | null>(null);

  const config = useQuery({ queryKey: ['singbox', 'config'], queryFn: singBoxApi.getConfig, retry: false });
  const runtime = useQuery({ queryKey: ['singbox', 'runtime'], queryFn: singBoxApi.getRuntime, retry: false });
  const connections = useQuery({ queryKey: ['singbox', 'connections'], queryFn: singBoxApi.connections, retry: false });
  const outbounds = useQuery({ queryKey: ['singbox', 'outbounds'], queryFn: singBoxApi.listOutbounds, retry: false });
  const rules = useQuery({ queryKey: ['singbox', 'route-rules'], queryFn: singBoxApi.listRouteRules, retry: false });
  const ruleSets = useQuery({ queryKey: ['singbox', 'rule-sets'], queryFn: singBoxApi.listRuleSets, retry: false });
  const argos = useQuery({ queryKey: ['singbox', 'argos'], queryFn: singBoxApi.listArgos, retry: false, refetchInterval: 30000 });
  const protocols = useQuery({ queryKey: ['resources', 'protocols'], queryFn: protocolsApi.list, retry: false });
  useEffect(() => {
    if (config.data) configForm.setFieldsValue({ ...config.data, clashApiSecret: '' });
  }, [config.data, configForm]);

  const reportError = (error: Error) => notification.error({
    message: error.message,
    description: error instanceof ApiError && error.requestId ? `请求 ID：${error.requestId}` : undefined
  });
  const refreshConfig = () => queryClient.invalidateQueries({ queryKey: ['singbox', 'config'] });
  const refreshRuntime = () => queryClient.invalidateQueries({ queryKey: ['singbox', 'runtime'] });
  const refreshConnections = () => queryClient.invalidateQueries({ queryKey: ['singbox', 'connections'] });
  const refreshOutbounds = () => queryClient.invalidateQueries({ queryKey: ['singbox', 'outbounds'] });
  const refreshRules = () => queryClient.invalidateQueries({ queryKey: ['singbox', 'route-rules'] });
  const refreshRuleSets = () => queryClient.invalidateQueries({ queryKey: ['singbox', 'rule-sets'] });
  const refreshArgos = () => queryClient.invalidateQueries({ queryKey: ['singbox', 'argos'] });
  const outboundOptions = useMemo(() => (outbounds.data?.items || []).map((item) => ({ value: item.tag, label: item.tag })), [outbounds.data]);
  const ruleSetOptions = useMemo(() => (ruleSets.data?.items || []).map((item) => ({ value: item.tag, label: item.tag })), [ruleSets.data]);
  const vmessProtocols = useMemo(() => (protocols.data?.items || []).filter((item) => item.type === 'vmess' && item.enabled), [protocols.data]);

  const updateConfig = useMutation({
    mutationFn: (values: ConfigValues) => {
      const { clashApiSecretSet: _, clashApiSecret, ...input } = values as ConfigValues & { clashApiSecretSet?: boolean };
      return singBoxApi.updateConfig({ ...input, ...(clashApiSecret ? { clashApiSecret } : {}), revision: config.data!.revision });
    },
    onSuccess: () => { void refreshConfig(); message.success('服务端配置已同步'); },
    onError: reportError
  });
  const outboundPayload = (values: OutboundValues) => values.type === 'warp'
    ? { type: 'warp' as const, tag: values.tag, warp: { endpoint: values.warp?.endpoint, publicKey: values.warp?.publicKey || '', ...(values.warp?.privateKey ? { privateKey: values.warp.privateKey } : {}), ipv4: values.warp?.ipv4 || '', ...(values.warp?.ipv6 ? { ipv6: values.warp.ipv6 } : {}), ...(values.warp?.reserved ? { reserved: values.warp.reserved.split(',').map((part) => Number(part.trim())) } : {}), ...(values.warp?.mtu ? { mtu: values.warp.mtu } : {}) } }
    : { type: 'socks' as const, tag: values.tag, server: values.server || '', serverPort: values.serverPort || 0, ...(values.username ? { username: values.username } : {}), ...(values.password ? { password: values.password } : {}) };
  const createOutbound = useMutation({
    mutationFn: (values: OutboundValues) => singBoxApi.createOutbound(outboundPayload(values)),
    onSuccess: () => { void refreshOutbounds(); void refreshConfig(); closeOutbound(); message.success('出站已创建'); },
    onError: reportError
  });
  const updateOutbound = useMutation({
    mutationFn: (values: OutboundValues) => singBoxApi.updateOutbound(editingOutbound!.id, { ...outboundPayload(values), revision: editingOutbound!.revision }),
    onSuccess: () => { void refreshOutbounds(); void refreshConfig(); closeOutbound(); message.success('出站已更新'); },
    onError: reportError
  });
  const registerWarp = useMutation({
    mutationFn: singBoxApi.registerWarp,
    onSuccess: (value) => {
      outboundForm.setFieldsValue({ type: 'warp', warp: { endpoint: value.endpoint, publicKey: value.publicKey, privateKey: value.privateKey, ipv4: value.ipv4, ...(value.ipv6 ? { ipv6: value.ipv6 } : {}), ...(value.reserved ? { reserved: value.reserved.join(',') } : {}) } });
      message.success('已注册免费 WARP 账户，检查参数后保存');
    },
    onError: reportError
  });
  const applyAITemplate = useMutation({
    mutationFn: singBoxApi.applyAITemplate,
    onSuccess: () => { void refreshRules(); message.success('已应用 AI 解锁规则（openai/gemini/anthropic）'); },
    onError: reportError
  });
  const createArgo = useMutation({
    mutationFn: (values: ArgoValues) => singBoxApi.createArgo({ ...values, ...(values.token ? { token: values.token } : {}) }),
    onSuccess: () => { void refreshArgos(); closeArgo(); message.success('Argo 隧道已创建，等待 cloudflared 启动'); },
    onError: reportError
  });
  const updateArgo = useMutation({
    mutationFn: (values: ArgoValues) => singBoxApi.updateArgo(editingArgo!.id, { ...values, ...(values.token ? { token: values.token } : {}), revision: editingArgo!.revision }),
    onSuccess: () => { void refreshArgos(); closeArgo(); message.success('Argo 隧道已更新'); },
    onError: reportError
  });
  const removeArgo = useMutation({ mutationFn: singBoxApi.removeArgo, onSuccess: () => { void refreshArgos(); message.success('Argo 隧道已删除'); }, onError: reportError });
  const restartArgo = useMutation({ mutationFn: singBoxApi.restartArgo, onSuccess: () => { message.success('重启已调度，稍候生效'); setTimeout(() => void refreshArgos(), 3000); }, onError: reportError });
  const removeOutbound = useMutation({ mutationFn: singBoxApi.removeOutbound, onSuccess: () => { void refreshOutbounds(); void refreshConfig(); message.success('SOCKS 出站已删除'); }, onError: reportError });
  const rulePayload = (values: RuleValues) => ({
    name: values.name, enabled: values.enabled, priority: values.priority, outboundTag: values.outboundTag,
    conditions: { domains: values.domains || [], domainSuffixes: values.domainSuffixes || [], ipCidrs: values.ipCidrs || [], ports: (values.ports || []).map(Number), networks: values.networks || [], inboundTags: values.inboundTags || [], geoip: values.geoip || [], geosite: values.geosite || [], ruleSets: values.ruleSets || [] }
  });
  const createRule = useMutation({ mutationFn: (values: RuleValues) => singBoxApi.createRouteRule(rulePayload(values)), onSuccess: () => { void refreshRules(); closeRule(); message.success('分流规则已创建'); }, onError: reportError });
  const updateRule = useMutation({ mutationFn: (values: RuleValues) => singBoxApi.updateRouteRule(editingRule!.id, { ...rulePayload(values), revision: editingRule!.revision }), onSuccess: () => { void refreshRules(); closeRule(); message.success('分流规则已更新'); }, onError: reportError });
  const removeRule = useMutation({ mutationFn: singBoxApi.removeRouteRule, onSuccess: () => { void refreshRules(); message.success('分流规则已删除'); }, onError: reportError });
  const orderRules = useMutation({ mutationFn: singBoxApi.orderRouteRules, onSuccess: () => void refreshRules(), onError: reportError });
  const createRuleSet = useMutation({ mutationFn: (values: RuleSetValues) => singBoxApi.createRuleSet(values), onSuccess: () => { void refreshRuleSets(); closeRuleSet(); message.success('规则集已创建'); }, onError: reportError });
  const updateRuleSet = useMutation({ mutationFn: (values: RuleSetValues) => singBoxApi.updateRuleSet(editingRuleSet!.id, { ...values, revision: editingRuleSet!.revision }), onSuccess: () => { void refreshRuleSets(); closeRuleSet(); message.success('规则集已更新'); }, onError: reportError });
  const removeRuleSet = useMutation({ mutationFn: singBoxApi.removeRuleSet, onSuccess: () => { void refreshRuleSets(); message.success('规则集已删除'); }, onError: reportError });
  const dryRun = useMutation({ mutationFn: singBoxApi.dryRun, onSuccess: setDryRunResult, onError: reportError });
  const validate = useMutation({ mutationFn: singBoxApi.validate, onSuccess: setValidation, onError: reportError });
  const checkRuntime = useMutation({ mutationFn: singBoxApi.checkRuntime, onSuccess: (value) => { message.success(value.message); void refreshRuntime(); }, onError: reportError });
  const reconcileRuntime = useMutation({ mutationFn: singBoxApi.reconcileRuntime, onSuccess: () => { message.success('当前托管配置已重新同步'); void refreshRuntime(); }, onError: reportError });
  const restartRuntime = useMutation({ mutationFn: singBoxApi.restartRuntime, onSuccess: () => { message.success('sing-box 已重启并通过健康检查'); void refreshRuntime(); void refreshConnections(); }, onError: reportError });
  const closeConnection = useMutation({ mutationFn: singBoxApi.closeConnection, onSuccess: () => { message.success('连接已断开'); void refreshConnections(); }, onError: reportError });

  const closeOutbound = () => { setOutboundDrawer(false); setEditingOutbound(null); outboundForm.resetFields(); };
  const closeArgo = () => { setArgoDrawer(false); setEditingArgo(null); argoForm.resetFields(); };
  const closeRule = () => { setRuleDrawer(false); setEditingRule(null); ruleForm.resetFields(); };
  const closeRuleSet = () => { setRuleSetDrawer(false); setEditingRuleSet(null); ruleSetForm.resetFields(); };
  const openCreateOutbound = () => { setEditingOutbound(null); outboundForm.setFieldsValue({ type: 'socks', tag: '', server: '', serverPort: 1080, username: '', password: '' }); setOutboundDrawer(true); };
  const openCreateArgo = () => { setEditingArgo(null); argoForm.setFieldsValue({ name: '', enabled: true, mode: 'temporary', fixedDomain: '', preferredAddress: '', protocolId: vmessProtocols[0]?.id || '', token: '' }); setArgoDrawer(true); };
  const openEditArgo = (value: ArgoTunnel) => { setEditingArgo(value); argoForm.setFieldsValue({ name: value.name, enabled: value.enabled, mode: value.mode, fixedDomain: value.fixedDomain || '', preferredAddress: value.preferredAddress || '', protocolId: value.protocolId, token: '' }); setArgoDrawer(true); };
  const openEditOutbound = (value: SingBoxOutbound) => { setEditingOutbound(value); outboundForm.setFieldsValue({ type: value.type === 'warp' ? 'warp' : 'socks', tag: value.tag, server: value.server || '', serverPort: value.serverPort || 0, username: value.username || '', password: '', ...(value.warp ? { warp: { endpoint: value.warp.endpoint, publicKey: value.warp.publicKey, privateKey: '', ipv4: value.warp.ipv4, ...(value.warp.ipv6 ? { ipv6: value.warp.ipv6 } : {}), ...(value.warp.reserved ? { reserved: value.warp.reserved.join(',') } : {}), ...(value.warp.mtu ? { mtu: value.warp.mtu } : {}) } } : {}) }); setOutboundDrawer(true); };
  const openCreateRule = () => { const items = rules.data?.items || []; setEditingRule(null); ruleForm.setFieldsValue({ name: '', enabled: true, priority: ((items[items.length - 1]?.priority || 0) + 10), outboundTag: config.data?.defaultOutboundTag || 'direct', domains: [], domainSuffixes: [], ipCidrs: [], ports: [], networks: [], inboundTags: [], geoip: [], geosite: [], ruleSets: [] }); setRuleDrawer(true); };
  const openEditRule = (value: SingBoxRouteRule) => { setEditingRule(value); ruleForm.setFieldsValue({ ...value, ...value.conditions, ports: value.conditions.ports?.map(String) || [] }); setRuleDrawer(true); };
  const openCreateRuleSet = () => { setEditingRuleSet(null); ruleSetForm.setFieldsValue({ tag: '', format: 'binary', url: '', downloadDetour: '' }); setRuleSetDrawer(true); };
  const openEditRuleSet = (value: SingBoxRuleSet) => { setEditingRuleSet(value); ruleSetForm.setFieldsValue({ tag: value.tag, format: value.format, url: value.url, downloadDetour: value.downloadDetour }); setRuleSetDrawer(true); };
  const moveRule = (index: number, direction: -1 | 1) => {
    const items = [...(rules.data?.items || [])]; const next = index + direction;
    if (next < 0 || next >= items.length) return;
    [items[index], items[next]] = [items[next], items[index]];
    orderRules.mutate(items.map((item) => item.id));
  };

  const outboundColumns: ColumnsType<SingBoxOutbound> = [
    { title: '标签', dataIndex: 'tag', width: 170, render: (value: string, record) => <Space><Typography.Text strong>{value}</Typography.Text>{record.id === record.tag ? <Tag>内建</Tag> : null}</Space> },
    { title: '类型', dataIndex: 'type', width: 100, render: (value: string) => value === 'warp' ? <Tag color="geekblue">WARP</Tag> : value },
    { title: '地址', render: (_, value) => value.type === 'warp' ? (value.warp?.endpoint || '-') : value.server ? `${value.server}:${value.serverPort}` : '-' },
    { title: '认证', width: 120, render: (_, value) => value.type === 'socks' ? value.hasPassword ? '密码已设置' : value.username ? '仅用户名' : '无' : '-' },
    { title: '操作', width: 100, render: (_, value) => value.type === 'socks' || value.type === 'warp' ? <Space size={2}><Tooltip title="编辑"><Button type="text" icon={<EditOutlined />} onClick={() => openEditOutbound(value)} /></Tooltip><Popconfirm title="确认删除该出站？" onConfirm={() => removeOutbound.mutate(value.id)}><Tooltip title="删除"><Button type="text" danger icon={<DeleteOutlined />} /></Tooltip></Popconfirm></Space> : null }
  ];
  const argoColumns: ColumnsType<ArgoTunnel> = [
    { title: '名称', dataIndex: 'name', width: 140, render: (value: string) => <Typography.Text strong>{value}</Typography.Text> },
    { title: '模式', dataIndex: 'mode', width: 90, render: (value: string) => value === 'token' ? '固定域名' : '临时' },
    { title: '域名', render: (_, value) => value.assignedDomain || value.fixedDomain || '-' },
    { title: '优选地址', dataIndex: 'preferredAddress', width: 140, render: (value?: string) => value || '-' },
    { title: '承载协议', width: 120, render: (_, value) => (protocols.data?.items || []).find((item) => item.id === value.protocolId)?.name || value.protocolId },
    { title: '状态', dataIndex: 'status', width: 90, render: (value: string, record) => <Tooltip title={record.lastError || undefined}>{value === 'running' ? <Tag color="success">运行中</Tag> : value === 'starting' ? <Tag color="processing">启动中</Tag> : value === 'error' ? <Tag color="error">错误</Tag> : <Tag>已停止</Tag>}</Tooltip> },
    { title: '操作', width: 140, render: (_, value) => <Space size={2}><Tooltip title="编辑"><Button type="text" icon={<EditOutlined />} onClick={() => openEditArgo(value)} /></Tooltip><Tooltip title="重启隧道"><Button type="text" onClick={() => restartArgo.mutate(value.id)}>重启</Button></Tooltip><Popconfirm title="确认删除该 Argo 隧道？" onConfirm={() => removeArgo.mutate(value.id)}><Tooltip title="删除"><Button type="text" danger icon={<DeleteOutlined />} /></Tooltip></Popconfirm></Space> }
  ];
  const ruleColumns: ColumnsType<SingBoxRouteRule> = [
    { title: '顺序', width: 84, render: (_, __, index) => <Space size={0}><Button type="text" size="small" icon={<ArrowUpOutlined />} disabled={index === 0} onClick={() => moveRule(index, -1)} /><Button type="text" size="small" icon={<ArrowDownOutlined />} disabled={index === (rules.data?.items.length || 0) - 1} onClick={() => moveRule(index, 1)} /></Space> },
    { title: '规则', dataIndex: 'name', width: 180, render: (value: string) => <Typography.Text strong>{value}</Typography.Text> },
    { title: '条件', render: (_, value) => conditionSummary(value.conditions) },
    { title: '出口', dataIndex: 'outboundTag', width: 130, render: (value: string) => <Tag color={value === 'block' ? 'error' : value === 'direct' ? 'success' : 'processing'}>{value}</Tag> },
    { title: '状态', dataIndex: 'enabled', width: 76, render: (value: boolean) => value ? '启用' : '停用' },
    { title: '操作', width: 100, render: (_, value) => <Space size={2}><Tooltip title="编辑"><Button type="text" icon={<EditOutlined />} onClick={() => openEditRule(value)} /></Tooltip><Popconfirm title="确认删除该分流规则？" onConfirm={() => removeRule.mutate(value.id)}><Tooltip title="删除"><Button type="text" danger icon={<DeleteOutlined />} /></Tooltip></Popconfirm></Space> }
  ];
  const ruleSetColumns: ColumnsType<SingBoxRuleSet> = [
    { title: '标签', dataIndex: 'tag', width: 170, render: (value: string) => <Typography.Text strong>{value}</Typography.Text> },
    { title: '格式', dataIndex: 'format', width: 120, render: (value: string) => value === 'binary' ? '二进制 (.srs)' : '源文件 (.json)' },
    { title: '下载地址', dataIndex: 'url', ellipsis: true },
    { title: '下载出口', dataIndex: 'downloadDetour', width: 120, render: (value?: string) => value ? <Tag>{value}</Tag> : '直连' },
    { title: '操作', width: 100, render: (_, value) => <Space size={2}><Tooltip title="编辑"><Button type="text" icon={<EditOutlined />} onClick={() => openEditRuleSet(value)} /></Tooltip><Popconfirm title="确认删除该规则集？" onConfirm={() => removeRuleSet.mutate(value.id)}><Tooltip title="删除"><Button type="text" danger icon={<DeleteOutlined />} /></Tooltip></Popconfirm></Space> }
  ];

  const connectionColumns: ColumnsType<SingBoxConnection> = [
    { title: '目标', render: (_, value) => value.host ? `${value.host}${value.port ? `:${value.port}` : ''}` : '-' },
    { title: '入站 / 出口', render: (_, value) => [value.inbound, value.outbound].filter(Boolean).join(' -> ') || '-' },
    { title: '规则', dataIndex: 'rule', render: (value?: string) => value || '-' },
    { title: '上行', dataIndex: 'uploadBytes', render: formatBytes },
    { title: '下行', dataIndex: 'downloadBytes', render: formatBytes },
    { title: '开始时间', dataIndex: 'startedAt', render: (value: string) => value ? new Date(value).toLocaleString() : '-' },
    { title: '操作', width: 76, render: (_, value) => <Popconfirm title="确认断开这条连接？" onConfirm={() => closeConnection.mutate(value.id)}><Tooltip title="断开连接"><Button type="text" danger icon={<DeleteOutlined />} loading={closeConnection.isPending} /></Tooltip></Popconfirm> }
  ];

  if (config.isError) return <Result status={config.error instanceof ApiError && config.error.status === 403 ? '403' : 'error'} title={config.error.message} extra={<Button type="primary" icon={<ReloadOutlined />} onClick={() => void config.refetch()}>重试</Button>} />;

  return <Page title="服务端配置">
    <Segmented options={tabGroups.map((group) => ({ label: group.label, value: group.key }))} value={activeGroup.key} onChange={(value) => selectGroup(String(value))} style={{ marginBottom: 16 }} />
    <Tabs activeKey={tab} onChange={selectTab} items={[
      { key: 'runtime', label: '运行维护', children: <><div className="page-toolbar"><Typography.Text type="secondary">配置保存后会自动重建嵌入式 sing-box 内核，现有代理连接会短暂中断。</Typography.Text><Space><Button type="primary" loading={checkRuntime.isPending} onClick={() => checkRuntime.mutate()}>检查配置</Button>{runtime.data?.canRestart ? <Popconfirm title="确认重建 sing-box 内核？" description="重建会中断所有活动连接。" onConfirm={() => restartRuntime.mutate()}><Button type="primary" danger loading={restartRuntime.isPending}>重建内核</Button></Popconfirm> : null}<Popconfirm title="确认重新同步当前托管配置？" description="内核启动失败时会恢复上一份成功配置。" onConfirm={() => reconcileRuntime.mutate()}><Button type="primary" loading={reconcileRuntime.isPending}>重新同步</Button></Popconfirm></Space></div><Descriptions bordered size="small" column={1} items={[{ key: 'mode', label: '运行模式', children: runtime.data?.runtimeMode === 'embedded' ? '嵌入式内核' : '加载中' }, { key: 'config', label: '配置状态', children: configStatusLabel(runtime.data?.configStatus || '加载中') }, { key: 'kernel', label: '内核状态', children: kernelStatusLabel(runtime.data?.kernelStatus || '加载中') }, { key: 'version', label: 'sing-box 版本', children: runtime.data?.version || '未探测' }, { key: 'started', label: '内核启动时间', children: runtime.data?.startedAt ? new Date(runtime.data.startedAt).toLocaleString() : '暂无' }, { key: 'digest', label: '当前配置摘要', children: runtime.data?.configDigest || '暂无' }, { key: 'applied', label: '最后成功同步', children: runtime.data?.lastAppliedAt ? new Date(runtime.data.lastAppliedAt).toLocaleString() : '暂无' }]} />{runtime.data?.reason ? <Alert style={{ marginTop: 16 }} type="warning" message="运行环境需要处理" description={runtime.data.reason} /> : null}{runtime.data?.failureReason ? <Alert style={{ marginTop: 16 }} type="error" message="最近同步失败" description={runtime.data.failureReason} /> : null}</> },
      { key: 'connections', label: `实时连接 (${connections.data?.items.length || 0})`, children: <><div className="page-toolbar"><Typography.Text type="secondary">连接数据仅来自本机或内部 Clash API；它不会改变历史流量统计。</Typography.Text><Button icon={<ReloadOutlined />} onClick={() => void connections.refetch()} loading={connections.isFetching}>刷新</Button></div>{connections.data?.available ? <Table rowKey="id" size="small" loading={connections.isLoading} dataSource={connections.data.items} columns={connectionColumns} pagination={{ pageSize: 20 }} scroll={{ x: 900 }} /> : <Alert type="info" message="实时连接不可用" description={connections.data?.reason || '尚未配置本机或内部 Clash API'} />}</> },
      { key: 'base', label: '基础配置', children: <Form<ConfigValues> form={configForm} layout="vertical" onFinish={() => updateConfig.mutate(configForm.getFieldsValue(true) as ConfigValues)}><div className="page-toolbar"><Typography.Text type="secondary">更改会自动编译并同步到本机 sing-box。</Typography.Text><Button type="primary" htmlType="submit" loading={updateConfig.isPending}>保存并同步</Button></div><Form.Item name="logLevel" label="日志等级" rules={[{ required: true }]}><Select options={logLevelOptions} style={{ maxWidth: 260 }} /></Form.Item><Form.Item name="logTimestamp" label="日志时间戳" valuePropName="checked"><Switch /></Form.Item><Form.Item name="defaultOutboundTag" label="默认出口" rules={[{ required: true }]}><Select options={outboundOptions} style={{ maxWidth: 320 }} /></Form.Item><Space align="start" wrap><Form.Item name="clashApiEnabled" label="Clash API" valuePropName="checked"><Switch /></Form.Item><Form.Item name="clashApiListen" label="Clash API 监听地址" extra="默认仅监听本机；不要将此管理 API 暴露到公网。"><Input placeholder="127.0.0.1:9090" style={{ width: 230 }} /></Form.Item><Form.Item name="clashApiSecret" label={config.data?.clashApiSecretSet ? 'Clash API 密钥（留空保持原值）' : 'Clash API 密钥'}><Input.Password placeholder="建议设置，用于实时连接观测" style={{ width: 230 }} /></Form.Item></Space><Space align="start" wrap><Form.Item name="v2rayApiEnabled" label="V2Ray API" valuePropName="checked"><Switch /></Form.Item><Form.Item name="v2rayApiListen" label="V2Ray API 监听地址"><Input placeholder="0.0.0.0:10000" style={{ width: 230 }} /></Form.Item></Space></Form> },
      { key: 'dns', label: 'DNS', children: <Form<ConfigValues> form={configForm} layout="vertical" onFinish={() => updateConfig.mutate(configForm.getFieldsValue(true) as ConfigValues)}><div className="page-toolbar"><Typography.Text type="secondary">DNS detour 使用已管理的出口。</Typography.Text><Button type="primary" htmlType="submit" loading={updateConfig.isPending}>保存并同步</Button></div><Form.List name="dnsServers">{(fields, { add, remove }) => <><Table size="small" pagination={false} rowKey="key" dataSource={fields} columns={[{ title: '标签', render: (_, field) => <Form.Item name={[field.name, 'tag']} rules={[{ required: true }]} style={{ margin: 0 }}><Input /></Form.Item> }, { title: '地址', render: (_, field) => <Form.Item name={[field.name, 'address']} rules={[{ required: true }]} style={{ margin: 0 }}><Input placeholder="https://dns.example/dns-query" /></Form.Item> }, { title: '出口', render: (_, field) => <Form.Item name={[field.name, 'detour']} style={{ margin: 0 }}><Select allowClear options={outboundOptions} style={{ minWidth: 130 }} /></Form.Item> }, { title: '操作', width: 64, render: (_, field) => <Button type="text" danger icon={<DeleteOutlined />} aria-label="删除 DNS" onClick={() => remove(field.name)} /> }]} /><Button type="primary" style={{ marginTop: 12 }} icon={<PlusOutlined />} onClick={() => add({ tag: '', address: '', detour: '' })}>添加 DNS</Button></>}</Form.List><Form.Item name="dnsFinal" label="最终 DNS" style={{ marginTop: 18 }}><Select allowClear options={(configForm.getFieldValue('dnsServers') || []).filter((item: { tag?: string }) => item.tag).map((item: { tag: string }) => ({ value: item.tag }))} style={{ maxWidth: 320 }} /></Form.Item></Form> },
      { key: 'outbounds', label: `出站 (${outbounds.data?.items.length || 0})`, children: <><div className="page-toolbar"><Typography.Text type="secondary">内建 direct 和 block 只读；WARP 出站需 with_wireguard 构建标签。</Typography.Text><Button type="primary" icon={<PlusOutlined />} onClick={openCreateOutbound}>添加出站</Button></div><Table rowKey="id" loading={outbounds.isLoading} dataSource={outbounds.data?.items || []} columns={outboundColumns} pagination={false} scroll={{ x: 700 }} /></> },
      { key: 'argo', label: `Argo 隧道 (${argos.data?.items.length || 0})`, children: <><div className="page-toolbar"><Typography.Text type="secondary">Argo 隧道通过 cloudflared 把 vmess(WS) 入站穿透到 Cloudflare 边缘；临时隧道地址在重启后会变化并自动更新订阅。二进制自动下载到 data/runtime/bin，受限网络可手动放置。</Typography.Text><Space><Button icon={<ReloadOutlined />} onClick={() => void argos.refetch()} loading={argos.isFetching}>刷新</Button><Button type="primary" icon={<PlusOutlined />} onClick={openCreateArgo} disabled={vmessProtocols.length === 0}>添加隧道</Button></Space></div>{vmessProtocols.length === 0 ? <Alert type="info" message="需要先创建启用的 vmess 协议（Argo 隧道走 vmess+WS）" style={{ marginBottom: 16 }} /> : null}<Table rowKey="id" size="small" loading={argos.isLoading} dataSource={argos.data?.items || []} columns={argoColumns} pagination={false} scroll={{ x: 860 }} /></> },
      { key: 'routes', label: `服务器分流 (${rules.data?.items.length || 0})`, children: <><div className="page-toolbar"><Typography.Text type="secondary">服务器分流：作用于服务器转发，影响所有用户；不会写入订阅，不改变单个用户客户端的分流。</Typography.Text><Space wrap><Button type="primary" onClick={() => navigate('/policies')}>前往订阅分流</Button><Button type="primary" icon={<PlusOutlined />} onClick={openCreateRule}>添加分流规则</Button><Space.Compact><Select style={{ width: 150 }} placeholder="AI 解锁出站" value={aiOutbound} onChange={setAiOutbound} options={outboundOptions} /><Button type="primary" loading={applyAITemplate.isPending} disabled={!aiOutbound} onClick={() => aiOutbound && applyAITemplate.mutate(aiOutbound)}>一键 AI 解锁</Button></Space.Compact></Space></div><Table rowKey="id" loading={rules.isLoading || orderRules.isPending} dataSource={rules.data?.items || []} columns={ruleColumns} pagination={false} scroll={{ x: 760 }} /><Tabs style={{ marginTop: 20 }} items={[{ key: 'dry-run', label: '测试路由', children: <div className="policy-dry-run"><Form<DryRunValues> form={dryRunForm} layout="inline" initialValues={{ host: 'example.com', port: 443, network: 'tcp' }} onFinish={(values) => dryRun.mutate(values)}><Form.Item name="host" rules={[{ required: true }]}><Input placeholder="域名或 IP" /></Form.Item><Form.Item name="port"><InputNumber min={0} max={65535} placeholder="端口" /></Form.Item><Form.Item name="network"><Select style={{ width: 110 }} options={[{ value: 'tcp' }, { value: 'udp' }]} /></Form.Item><Form.Item name="inboundTag"><Input placeholder="入站标签" /></Form.Item><Button type="primary" htmlType="submit" loading={dryRun.isPending}>执行</Button></Form>{dryRunResult ? <><Alert style={{ marginTop: 16 }} type="info" message={`最终出口：${dryRunResult.outboundTag}`} /><Table style={{ marginTop: 12 }} rowKey="ruleId" size="small" pagination={false} dataSource={dryRunResult.trace} columns={[{ title: '规则', dataIndex: 'name' }, { title: '状态', dataIndex: 'status', width: 90, render: (value: string) => ({ match: '匹配', miss: '未匹配', skipped: '已停用', unknown: '无法本地判断' }[value] || value) }, { title: '原因', dataIndex: 'reason', render: (value: string) => ({ 'rule disabled': '规则已停用', 'geoip/geosite condition requires the live kernel to evaluate': 'geoip/geosite 条件需内核实时判断', 'all conditions matched': '所有条件均匹配', 'domain did not match': '域名不匹配', 'domain suffix did not match': '域名后缀不匹配', 'host is not an IP address': '输入不是 IP 地址', 'IP CIDR did not match': 'IP 网段不匹配', 'port did not match': '端口不匹配', 'network did not match': '网络类型不匹配', 'inbound tag did not match': '入站标签不匹配', 'rule set did not match': '规则集未命中' }[value] || value) }, { title: '出口', dataIndex: 'outboundTag', width: 100 }]} /></> : <Empty image={Empty.PRESENTED_IMAGE_SIMPLE} description="暂无评估结果" />}</div> }, { key: 'validation', label: '验证', children: <><Button loading={validate.isPending} onClick={() => validate.mutate()}>验证服务端配置</Button>{validation ? <Space direction="vertical" style={{ width: '100%', marginTop: 12 }}>{validation.issues.length === 0 ? <Alert type="success" message="未发现配置问题" /> : validation.issues.map((issue, index) => <Alert key={`${issue.ruleId}-${index}`} type={issue.level === 'error' ? 'error' : 'warning'} message={issue.message} />)}</Space> : null}</> }]} /></> },
      { key: 'rulesets', label: `规则集 (${ruleSets.data?.items.length || 0})`, children: <><div className="page-toolbar"><Typography.Text type="secondary">远程规则集由内核下载并缓存，供服务器分流规则引用。</Typography.Text><Space><Button icon={<ReloadOutlined />} onClick={() => void ruleSets.refetch()} loading={ruleSets.isFetching}>刷新</Button><Button type="primary" icon={<PlusOutlined />} onClick={openCreateRuleSet}>添加规则集</Button></Space></div><Table rowKey="id" size="small" loading={ruleSets.isLoading} dataSource={ruleSets.data?.items || []} columns={ruleSetColumns} pagination={false} scroll={{ x: 700 }} /></> }
    ].filter((item) => activeGroup.tabs.includes(item.key))} />

    <Drawer title={editingOutbound ? '编辑出站' : '添加出站'} width={440} open={outboundDrawer} destroyOnClose onClose={closeOutbound} extra={<Button type="primary" loading={createOutbound.isPending || updateOutbound.isPending} onClick={() => outboundForm.submit()}>保存</Button>}><Form<OutboundValues> form={outboundForm} layout="vertical" onFinish={(values) => editingOutbound ? updateOutbound.mutate(values) : createOutbound.mutate(values)}><Form.Item name="type" label="类型" rules={[{ required: true }]}><Select disabled={Boolean(editingOutbound)} options={[{ value: 'socks', label: 'SOCKS' }, { value: 'warp', label: 'WARP (WireGuard)' }]} onChange={() => outboundForm.setFieldsValue({ server: '', serverPort: undefined, username: '', password: '' })} /></Form.Item><Form.Item name="tag" label="标签" rules={[{ required: true, whitespace: true }]}><Input disabled={Boolean(editingOutbound)} /></Form.Item><Form.Item noStyle shouldUpdate={(prev, next) => prev.type !== next.type}>{({ getFieldValue }) => getFieldValue('type') === 'warp' ? <><Form.Item label={<Space>WARP 参数<Button size="small" loading={registerWarp.isPending} onClick={() => registerWarp.mutate()}>注册免费 WARP</Button></Space>} /><Form.Item name={['warp', 'endpoint']} label="Endpoint" tooltip="留空默认 engage.cloudflareclient.com:2408"><Input placeholder="engage.cloudflareclient.com:2408" /></Form.Item><Form.Item name={['warp', 'publicKey']} label="对端公钥" rules={[{ required: true, whitespace: true }]}><Input /></Form.Item><Form.Item name={['warp', 'privateKey']} label={editingOutbound?.warp?.privateKeySet ? '私钥（留空保持原值）' : '私钥'} rules={[() => ({ validator: (_, value) => !value && editingOutbound?.warp?.privateKeySet ? Promise.resolve() : value ? Promise.resolve() : Promise.reject(new Error('必填')) })]}><Input.Password /></Form.Item><Form.Item name={['warp', 'ipv4']} label="IPv4（CIDR）" rules={[{ required: true, whitespace: true }]}><Input placeholder="172.16.0.2/32" /></Form.Item><Form.Item name={['warp', 'ipv6']} label="IPv6（CIDR，可选）"><Input /></Form.Item><Form.Item name={['warp', 'reserved']} label="Reserved（可选，逗号分隔三字节）"><Input placeholder="26,105,116" /></Form.Item><Form.Item name={['warp', 'mtu']} label="MTU（可选）"><InputNumber min={512} max={65535} style={{ width: '100%' }} /></Form.Item></> : <><Form.Item name="server" label="服务器" rules={[{ required: true, whitespace: true }]}><Input /></Form.Item><Form.Item name="serverPort" label="端口" rules={[{ required: true }]}><InputNumber min={1} max={65535} style={{ width: '100%' }} /></Form.Item><Form.Item name="username" label="用户名"><Input /></Form.Item><Form.Item name="password" label={editingOutbound?.hasPassword ? '密码（留空保持原值）' : '密码'}><Input.Password /></Form.Item></>}</Form.Item></Form></Drawer>
    <Drawer title={editingRule ? '编辑分流规则' : '添加分流规则'} width={520} open={ruleDrawer} destroyOnClose onClose={closeRule} extra={<Button type="primary" loading={createRule.isPending || updateRule.isPending} onClick={() => ruleForm.submit()}>保存</Button>}><Form<RuleValues> form={ruleForm} layout="vertical" onFinish={(values) => editingRule ? updateRule.mutate(values) : createRule.mutate(values)}><Form.Item name="name" label="名称" rules={[{ required: true, whitespace: true }]}><Input /></Form.Item><Space align="start"><Form.Item name="priority" label="优先级" rules={[{ required: true }]}><InputNumber min={0} /></Form.Item><Form.Item name="outboundTag" label="出口" rules={[{ required: true }]}><Select style={{ width: 200 }} options={outboundOptions} /></Form.Item></Space><Form.Item name="domains" label="精确域名"><Select mode="tags" tokenSeparators={[',']} /></Form.Item><Form.Item name="domainSuffixes" label="域名后缀"><Select mode="tags" tokenSeparators={[',']} /></Form.Item><Form.Item name="ipCidrs" label="IP CIDR"><Select mode="tags" tokenSeparators={[',']} /></Form.Item><Form.Item name="ports" label="端口"><Select mode="tags" tokenSeparators={[',']} /></Form.Item><Form.Item name="networks" label="网络"><Select mode="multiple" options={[{ value: 'tcp' }, { value: 'udp' }]} /></Form.Item><Form.Item name="inboundTags" label="入站标签"><Select mode="tags" tokenSeparators={[',']} /></Form.Item><Form.Item name="geoip" label="GeoIP" extra="可从常用列表选择，也可带 geoip: 前缀手动输入"><Select mode="tags" tokenSeparators={[',']} options={geoIPOptions} /></Form.Item><Form.Item name="geosite" label="GeoSite" extra="可从常用列表选择，也可带 geosite: 前缀手动输入"><Select mode="tags" tokenSeparators={[',']} options={geoSiteOptions} /></Form.Item><Form.Item name="ruleSets" label="规则集"><Select mode="multiple" options={ruleSetOptions} /></Form.Item><Form.Item name="enabled" label="启用" valuePropName="checked"><Switch /></Form.Item></Form></Drawer>
    <Drawer title={editingArgo ? '编辑 Argo 隧道' : '添加 Argo 隧道'} width={440} open={argoDrawer} destroyOnClose onClose={closeArgo} extra={<Button type="primary" loading={createArgo.isPending || updateArgo.isPending} onClick={() => argoForm.submit()}>保存</Button>}><Form<ArgoValues> form={argoForm} layout="vertical" onFinish={(values) => editingArgo ? updateArgo.mutate(values) : createArgo.mutate(values)}><Form.Item name="name" label="名称" rules={[{ required: true, whitespace: true }]}><Input /></Form.Item><Form.Item name="mode" label="模式" rules={[{ required: true }]}><Select options={[{ value: 'temporary', label: '临时隧道（trycloudflare，无需账号，重启换址）' }, { value: 'token', label: '固定域名（Cloudflare Tunnel Token）' }]} /></Form.Item><Form.Item name="protocolId" label="承载协议（vmess）" rules={[{ required: true }]}><Select options={vmessProtocols.map((item) => ({ value: item.id, label: item.name }))} /></Form.Item><Form.Item noStyle shouldUpdate={(prev, next) => prev.mode !== next.mode}>{({ getFieldValue }) => getFieldValue('mode') === 'token' ? <><Form.Item name="fixedDomain" label="固定域名" rules={[{ required: true, whitespace: true }]}><Input placeholder="tunnel.example.com" /></Form.Item><Form.Item name="token" label={editingArgo?.tokenSet ? 'Tunnel Token（留空保持原值）' : 'Tunnel Token'} rules={[() => ({ validator: (_, value) => (!value && editingArgo?.tokenSet) || value ? Promise.resolve() : Promise.reject(new Error('必填')) })]}><Input.TextArea rows={3} placeholder="eyJhIjoi…" /></Form.Item></> : null}</Form.Item><Form.Item name="preferredAddress" label="优选 CDN 地址（可选）" extra="订阅里客户端拨打的地址；留空用隧道域名"><Input placeholder="icook.tw" /></Form.Item><Form.Item name="enabled" label="启用" valuePropName="checked"><Switch /></Form.Item></Form></Drawer>
    <Drawer title={editingRuleSet ? '编辑规则集' : '添加规则集'} width={440} open={ruleSetDrawer} destroyOnClose onClose={closeRuleSet} extra={<Button type="primary" loading={createRuleSet.isPending || updateRuleSet.isPending} onClick={() => ruleSetForm.submit()}>保存</Button>}><Form<RuleSetValues> form={ruleSetForm} layout="vertical" onFinish={(values) => editingRuleSet ? updateRuleSet.mutate(values) : createRuleSet.mutate(values)}><Form.Item name="tag" label="标签" rules={[{ required: true, whitespace: true }]}><Input disabled={Boolean(editingRuleSet)} placeholder="geosite-google" /></Form.Item><Form.Item name="format" label="格式" rules={[{ required: true }]}><Select options={[{ value: 'binary', label: '二进制 (.srs)' }, { value: 'source', label: '源文件 (.json)' }]} /></Form.Item><Form.Item name="url" label="下载地址" rules={[{ required: true, whitespace: true }]}><Input placeholder="https://…/xxx.srs" /></Form.Item><Form.Item name="downloadDetour" label="下载出口" extra="留空则直连下载"><Select allowClear options={outboundOptions} /></Form.Item></Form></Drawer>
  </Page>;
}

function conditionSummary(value: SingBoxRouteConditions) {
  const parts = [value.domains?.join(', '), value.domainSuffixes?.map((item) => `*.${item}`).join(', '), value.ipCidrs?.join(', '), value.ports?.join(', '), value.networks?.join(', '), value.inboundTags?.map((item) => `in:${item}`).join(', '), value.geoip?.map((item) => `geoip:${item}`).join(', '), value.geosite?.map((item) => `geosite:${item}`).join(', '), value.ruleSets?.map((item) => `rs:${item}`).join(', ')].filter(Boolean);
  return parts.length ? <Typography.Text ellipsis={{ tooltip: parts.join(' | ') }} style={{ maxWidth: 340 }}>{parts.join(' | ')}</Typography.Text> : <Tag>全部流量</Tag>;
}
