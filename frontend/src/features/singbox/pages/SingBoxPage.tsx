import {
  ArrowDownOutlined,
  ArrowRightOutlined,
  ArrowUpOutlined,
  CheckCircleOutlined,
  ClearOutlined,
  DeleteOutlined,
  EditOutlined,
  ExperimentOutlined,
  KeyOutlined,
  PlusOutlined,
  ReloadOutlined,
  SafetyCertificateOutlined,
  SearchOutlined,
  SyncOutlined,
  ThunderboltOutlined
} from '@ant-design/icons';
import { useMutation, useQuery } from '@tanstack/react-query';
import {
  Alert,
  App as AntApp,
  Badge,
  Button,
  Card,
  Col,
  Collapse,
  Descriptions,
  Divider,
  Drawer,
  Dropdown,
  Empty,
  Form,
  Input,
  InputNumber,
  Popconfirm,
  Result,
  Row,
  Segmented,
  Select,
  Space,
  Spin,
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

const { Text, Title, Paragraph } = Typography;

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

const publicDNSPresets = [
  { tag: 'cf-doh', address: 'https://1.1.1.1/dns-query', label: 'Cloudflare (1.1.1.1)' },
  { tag: 'google-doh', address: 'https://dns.google/dns-query', label: 'Google (8.8.8.8)' },
  { tag: 'alidns-doh', address: 'https://dns.alidns.com/dns-query', label: '阿里 AliDNS (223.5.5.5)' },
  { tag: 'dnspod-doh', address: 'https://doh.pub/dns-query', label: '腾讯 DNSPod (119.29.29.29)' },
  { tag: 'quad9-doh', address: 'https://dns.quad9.net/dns-query', label: 'Quad9 安全 DNS (9.9.9.9)' }
];

function formatBytes(value: number) {
  const units = ['B', 'KB', 'MB', 'GB', 'TB'];
  let current = value;
  let index = 0;
  while (current >= 1024 && index < units.length - 1) { current /= 1024; index += 1; }
  return `${current.toFixed(index === 0 ? 0 : 2)} ${units[index]}`;
}

function formatUptime(startedAt?: string) {
  if (!startedAt) return '暂无';
  const start = new Date(startedAt).getTime();
  const diffSec = Math.max(0, Math.floor((Date.now() - start) / 1000));
  const days = Math.floor(diffSec / 86400);
  const hours = Math.floor((diffSec % 86400) / 3600);
  const minutes = Math.floor((diffSec % 3600) / 60);
  if (days > 0) return `${days} 天 ${hours} 小时`;
  if (hours > 0) return `${hours} 小时 ${minutes} 分钟`;
  return `${minutes} 分钟`;
}

function generateRandomSecret(length = 24) {
  const chars = 'ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789';
  // 密钥必须用加密安全随机源；Math.random 可预测，不能用于凭据。
  const values = new Uint32Array(length);
  crypto.getRandomValues(values);
  let res = '';
  for (let i = 0; i < length; i++) {
    res += chars.charAt(values[i] % chars.length);
  }
  return res;
}

const tabGroups: Array<{ key: string; label: string; tabs: string[] }> = [
  { key: 'ops', label: '运维与连接', tabs: ['runtime', 'connections'] },
  { key: 'config', label: '配置与路由', tabs: ['base', 'dns', 'outbounds', 'routes', 'rulesets', 'argo'] }
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

  // Form instances
  const [configForm] = Form.useForm<ConfigValues>();
  const [outboundForm] = Form.useForm<OutboundValues>();
  const [argoForm] = Form.useForm<ArgoValues>();
  const [ruleForm] = Form.useForm<RuleValues>();
  const [ruleSetForm] = Form.useForm<RuleSetValues>();
  const [dryRunForm] = Form.useForm<DryRunValues>();

  // Drawers & Modals
  const [outboundDrawer, setOutboundDrawer] = useState(false);
  const [argoDrawer, setArgoDrawer] = useState(false);
  const [ruleDrawer, setRuleDrawer] = useState(false);
  const [ruleSetDrawer, setRuleSetDrawer] = useState(false);
  const [dryRunDrawer, setDryRunDrawer] = useState(false);
  const [validationDrawer, setValidationDrawer] = useState(false);

  // Edit states
  const [editingOutbound, setEditingOutbound] = useState<SingBoxOutbound | null>(null);
  const [editingArgo, setEditingArgo] = useState<ArgoTunnel | null>(null);
  const [editingRule, setEditingRule] = useState<SingBoxRouteRule | null>(null);
  const [editingRuleSet, setEditingRuleSet] = useState<SingBoxRuleSet | null>(null);
  const [aiOutbound, setAiOutbound] = useState<string | undefined>(undefined);
  const [dryRunResult, setDryRunResult] = useState<SingBoxDryRunResult | null>(null);
  const [validation, setValidation] = useState<SingBoxValidationResult | null>(null);

  // Connections filtering & auto-refresh
  const [connKeyword, setConnKeyword] = useState('');
  const [connOutboundFilter, setConnOutboundFilter] = useState<string>('all');
  const [connInboundFilter, setConnInboundFilter] = useState<string>('all');
  const [autoRefreshInterval, setAutoRefreshInterval] = useState<number>(0);

  // Watch switch values in configForm
  const clashApiEnabled = Form.useWatch('clashApiEnabled', configForm);
  const v2rayApiEnabled = Form.useWatch('v2rayApiEnabled', configForm);

  // Queries
  const config = useQuery({ queryKey: ['singbox', 'config'], queryFn: singBoxApi.getConfig, retry: false });
  const runtime = useQuery({ queryKey: ['singbox', 'runtime'], queryFn: singBoxApi.getRuntime, retry: false });
  const connections = useQuery({
    queryKey: ['singbox', 'connections'],
    queryFn: singBoxApi.connections,
    retry: false,
    refetchInterval: tab === 'connections' && autoRefreshInterval > 0 ? autoRefreshInterval : false
  });
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

  // Mutations
  const updateConfig = useMutation({
    mutationFn: (values: ConfigValues) => {
      const { clashApiSecretSet: _, clashApiSecret, ...input } = values as ConfigValues & { clashApiSecretSet?: boolean };
      return singBoxApi.updateConfig({ ...input, ...(clashApiSecret ? { clashApiSecret } : {}), revision: config.data!.revision });
    },
    onSuccess: () => { void refreshConfig(); message.success('服务配置已同步到内核'); },
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
  const removeOutbound = useMutation({
    mutationFn: singBoxApi.removeOutbound,
    onSuccess: () => { void refreshOutbounds(); void refreshConfig(); message.success('出站已删除'); },
    onError: reportError
  });

  const registerWarp = useMutation({
    mutationFn: singBoxApi.registerWarp,
    onSuccess: (value) => {
      outboundForm.setFieldsValue({
        type: 'warp',
        warp: {
          endpoint: value.endpoint,
          publicKey: value.publicKey,
          privateKey: value.privateKey,
          ipv4: value.ipv4,
          ...(value.ipv6 ? { ipv6: value.ipv6 } : {}),
          ...(value.reserved ? { reserved: value.reserved.join(',') } : {})
        }
      });
      message.success('已自动向 Cloudflare 注册免费 WARP 凭据，请核对后保存');
    },
    onError: reportError
  });

  const applyAITemplate = useMutation({
    mutationFn: singBoxApi.applyAITemplate,
    onSuccess: () => { void refreshRules(); message.success('已应用主流 AI 平台（OpenAI / Gemini / Claude）分流规则'); },
    onError: reportError
  });

  const createArgo = useMutation({
    mutationFn: (values: ArgoValues) => singBoxApi.createArgo({ ...values, ...(values.token ? { token: values.token } : {}) }),
    onSuccess: () => { void refreshArgos(); closeArgo(); message.success('Argo 隧道已创建，正在唤起 cloudflared 边缘连接'); },
    onError: reportError
  });
  const updateArgo = useMutation({
    mutationFn: (values: ArgoValues) => singBoxApi.updateArgo(editingArgo!.id, { ...values, ...(values.token ? { token: values.token } : {}), revision: editingArgo!.revision }),
    onSuccess: () => { void refreshArgos(); closeArgo(); message.success('Argo 隧道已更新'); },
    onError: reportError
  });
  const removeArgo = useMutation({ mutationFn: singBoxApi.removeArgo, onSuccess: () => { void refreshArgos(); message.success('Argo 隧道已删除'); }, onError: reportError });
  const restartArgo = useMutation({ mutationFn: singBoxApi.restartArgo, onSuccess: () => { message.success('已调度重启，稍后自动生效'); setTimeout(() => void refreshArgos(), 3000); }, onError: reportError });

  const rulePayload = (values: RuleValues) => ({
    name: values.name, enabled: values.enabled, priority: values.priority, outboundTag: values.outboundTag,
    conditions: {
      domains: values.domains || [], domainSuffixes: values.domainSuffixes || [], ipCidrs: values.ipCidrs || [],
      ports: (values.ports || []).map(Number), networks: values.networks || [], inboundTags: values.inboundTags || [],
      geoip: values.geoip || [], geosite: values.geosite || [], ruleSets: values.ruleSets || []
    }
  });

  const createRule = useMutation({ mutationFn: (values: RuleValues) => singBoxApi.createRouteRule(rulePayload(values)), onSuccess: () => { void refreshRules(); closeRule(); message.success('分流规则已创建'); }, onError: reportError });
  const updateRule = useMutation({ mutationFn: (values: RuleValues) => singBoxApi.updateRouteRule(editingRule!.id, { ...rulePayload(values), revision: editingRule!.revision }), onSuccess: () => { void refreshRules(); closeRule(); message.success('分流规则已更新'); }, onError: reportError });
  const toggleRule = useMutation({
    mutationFn: (rule: SingBoxRouteRule) => singBoxApi.updateRouteRule(rule.id, {
      name: rule.name,
      enabled: !rule.enabled,
      priority: rule.priority,
      outboundTag: rule.outboundTag,
      conditions: rule.conditions,
      revision: rule.revision
    }),
    onSuccess: (_, rule) => { void refreshRules(); message.success(`规则「${rule.name}」已${rule.enabled ? '停用' : '启用'}`); },
    onError: reportError
  });
  const removeRule = useMutation({ mutationFn: singBoxApi.removeRouteRule, onSuccess: () => { void refreshRules(); message.success('分流规则已删除'); }, onError: reportError });
  const orderRules = useMutation({ mutationFn: singBoxApi.orderRouteRules, onSuccess: () => void refreshRules(), onError: reportError });

  const createRuleSet = useMutation({ mutationFn: (values: RuleSetValues) => singBoxApi.createRuleSet(values), onSuccess: () => { void refreshRuleSets(); closeRuleSet(); message.success('规则集已创建'); }, onError: reportError });
  const updateRuleSet = useMutation({ mutationFn: (values: RuleSetValues) => singBoxApi.updateRuleSet(editingRuleSet!.id, { ...values, revision: editingRuleSet!.revision }), onSuccess: () => { void refreshRuleSets(); closeRuleSet(); message.success('规则集已更新'); }, onError: reportError });
  const removeRuleSet = useMutation({ mutationFn: singBoxApi.removeRuleSet, onSuccess: () => { void refreshRuleSets(); message.success('规则集已删除'); }, onError: reportError });

  const dryRun = useMutation({ mutationFn: singBoxApi.dryRun, onSuccess: setDryRunResult, onError: reportError });
  const validate = useMutation({
    mutationFn: singBoxApi.validate,
    onSuccess: (data) => {
      setValidation(data);
      if (data.valid) {
        message.success('配置全链路校验通过');
      } else {
        message.warning(`发现 ${data.issues.length} 处配置风险`);
      }
    },
    onError: reportError
  });

  const checkRuntime = useMutation({ mutationFn: singBoxApi.checkRuntime, onSuccess: (value) => { message.success(value.message); void refreshRuntime(); }, onError: reportError });
  const reconcileRuntime = useMutation({ mutationFn: singBoxApi.reconcileRuntime, onSuccess: () => { message.success('当前托管配置已重新同步并生效'); void refreshRuntime(); }, onError: reportError });
  const restartRuntime = useMutation({ mutationFn: singBoxApi.restartRuntime, onSuccess: () => { message.success('sing-box 已重启并通过健康检查'); void refreshRuntime(); void refreshConnections(); }, onError: reportError });
  const closeConnection = useMutation({ mutationFn: singBoxApi.closeConnection, onSuccess: () => { message.success('连接已断开'); void refreshConnections(); }, onError: reportError });

  // Drawer handlers
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

  // Preset DNS adder
  const handleAddPresetDNS = (preset: typeof publicDNSPresets[0]) => {
    const current = configForm.getFieldValue('dnsServers') || [];
    configForm.setFieldsValue({
      dnsServers: [...current, { tag: preset.tag, address: preset.address, detour: '' }]
    });
    message.success(`已添加 ${preset.label}`);
  };

  // Realtime connections filtering
  const allConnections = connections.data?.items || [];
  const inboundList = useMemo(() => Array.from(new Set(allConnections.map((c) => c.inbound).filter(Boolean))), [allConnections]);
  const outboundList = useMemo(() => Array.from(new Set(allConnections.map((c) => c.outbound).filter(Boolean))), [allConnections]);

  const filteredConnections = useMemo(() => {
    return allConnections.filter((item) => {
      if (connKeyword) {
        const kw = connKeyword.trim().toLowerCase();
        const hostMatch = item.host?.toLowerCase().includes(kw);
        const portMatch = String(item.port || '').includes(kw);
        const ruleMatch = item.rule?.toLowerCase().includes(kw);
        if (!hostMatch && !portMatch && !ruleMatch) return false;
      }
      if (connOutboundFilter !== 'all' && item.outbound !== connOutboundFilter) return false;
      if (connInboundFilter !== 'all' && item.inbound !== connInboundFilter) return false;
      return true;
    });
  }, [allConnections, connKeyword, connOutboundFilter, connInboundFilter]);

  // Table Columns
  const outboundColumns: ColumnsType<SingBoxOutbound> = [
    {
      title: '标签',
      dataIndex: 'tag',
      width: 180,
      render: (value: string, record) => (
        <Space>
          <Typography.Text strong style={{ fontFamily: 'JetBrains Mono, monospace' }}>{value}</Typography.Text>
          {record.id === record.tag ? <Tag>内建</Tag> : null}
        </Space>
      )
    },
    {
      title: '类型',
      dataIndex: 'type',
      width: 110,
      render: (value: string) => value === 'warp' ? <Tag color="processing">WARP</Tag> : <Tag>{value.toUpperCase()}</Tag>
    },
    {
      title: '出口节点地址',
      render: (_, value) => (
        <Typography.Text code style={{ fontSize: 12 }}>
          {value.type === 'warp' ? (value.warp?.endpoint || 'engage.cloudflareclient.com:2408') : value.server ? `${value.server}:${value.serverPort}` : '-'}
        </Typography.Text>
      )
    },
    {
      title: '认证状态',
      width: 140,
      render: (_, value) => value.type === 'warp' ? (
        <Tag color="success">公私钥对齐</Tag>
      ) : value.hasPassword ? (
        <Tag color="default">带密码认证</Tag>
      ) : value.username ? (
        <Tag color="default">仅用户名</Tag>
      ) : (
        <Text type="secondary">无认证</Text>
      )
    },
    {
      title: '操作',
      width: 100,
      fixed: 'right',
      render: (_, value) => value.type === 'socks' || value.type === 'warp' ? (
        <Space size={2}>
          <Tooltip title="编辑参数">
            <Button type="text" size="small" icon={<EditOutlined />} onClick={() => openEditOutbound(value)} />
          </Tooltip>
          <Popconfirm title="确认删除该出站？" description="请确保无正在引用的分流规则或 DNS detour" onConfirm={() => removeOutbound.mutate(value.id)}>
            <Tooltip title="删除出站">
              <Button type="text" size="small" danger icon={<DeleteOutlined />} />
            </Tooltip>
          </Popconfirm>
        </Space>
      ) : null
    }
  ];

  const argoColumns: ColumnsType<ArgoTunnel> = [
    {
      title: '隧道名称',
      dataIndex: 'name',
      width: 150,
      render: (value: string) => <Typography.Text strong>{value}</Typography.Text>
    },
    {
      title: '模式',
      dataIndex: 'mode',
      width: 100,
      render: (value: string) => value === 'token' ? <Tag color="processing">固定域名</Tag> : <Tag>临时隧道</Tag>
    },
    {
      title: '边缘分发域名',
      render: (_, value) => {
        const domain = value.assignedDomain || value.fixedDomain;
        return domain ? <Typography.Text copyable style={{ fontFamily: 'JetBrains Mono, monospace', fontSize: 12 }}>{domain}</Typography.Text> : <Text type="secondary">-</Text>;
      }
    },
    {
      title: '优选 CDN 地址',
      dataIndex: 'preferredAddress',
      width: 150,
      render: (value?: string) => value ? <Typography.Text code style={{ fontSize: 12 }}>{value}</Typography.Text> : <Text type="secondary">同隧道域名</Text>
    },
    {
      title: '承载协议',
      width: 120,
      render: (_, value) => {
        const p = (protocols.data?.items || []).find((item) => item.id === value.protocolId);
        return <Tag color="default">{p?.name || value.protocolId}</Tag>;
      }
    },
    {
      title: '状态',
      dataIndex: 'status',
      width: 100,
      render: (value: string, record) => (
        <Tooltip title={record.lastError || undefined}>
          {value === 'running' ? <Tag color="success">运行中</Tag> : value === 'starting' ? <Tag color="processing">启动中</Tag> : value === 'error' ? <Tag color="error">错误</Tag> : <Tag>已停止</Tag>}
        </Tooltip>
      )
    },
    {
      title: '操作',
      width: 130,
      fixed: 'right',
      render: (_, value) => (
        <Space size={2}>
          <Tooltip title="编辑">
            <Button type="text" size="small" icon={<EditOutlined />} onClick={() => openEditArgo(value)} />
          </Tooltip>
          <Tooltip title="重启隧道进程">
            <Button type="text" size="small" icon={<SyncOutlined />} onClick={() => restartArgo.mutate(value.id)}>重启</Button>
          </Tooltip>
          <Popconfirm title="确认删除该 Argo 隧道？" onConfirm={() => removeArgo.mutate(value.id)}>
            <Tooltip title="删除">
              <Button type="text" size="small" danger icon={<DeleteOutlined />} />
            </Tooltip>
          </Popconfirm>
        </Space>
      )
    }
  ];

  const ruleColumns: ColumnsType<SingBoxRouteRule> = [
    {
      title: '顺序',
      width: 76,
      render: (_, __, index) => (
        <Space size={0}>
          <Button type="text" size="small" icon={<ArrowUpOutlined />} disabled={index === 0} onClick={() => moveRule(index, -1)} />
          <Button type="text" size="small" icon={<ArrowDownOutlined />} disabled={index === (rules.data?.items.length || 0) - 1} onClick={() => moveRule(index, 1)} />
        </Space>
      )
    },
    {
      title: '规则名称',
      dataIndex: 'name',
      width: 160,
      render: (value: string, record) => (
        <Space direction="vertical" size={2}>
          <Typography.Text strong>{value}</Typography.Text>
          <Text type="secondary" style={{ fontSize: 11 }}>优先级 {record.priority}</Text>
        </Space>
      )
    },
    {
      title: '匹配条件',
      render: (_, value) => conditionSummary(value.conditions)
    },
    {
      title: '路由出口',
      dataIndex: 'outboundTag',
      width: 140,
      render: (value: string) => (
        <Tag color={value === 'block' ? 'error' : value === 'direct' ? 'success' : 'processing'} style={{ fontFamily: 'JetBrains Mono, monospace' }}>
          {value}
        </Tag>
      )
    },
    {
      title: '状态',
      dataIndex: 'enabled',
      width: 80,
      render: (value: boolean, record) => (
        <Switch
          size="small"
          checked={value}
          loading={toggleRule.isPending}
          onChange={() => toggleRule.mutate(record)}
        />
      )
    },
    {
      title: '操作',
      width: 90,
      fixed: 'right',
      render: (_, value) => (
        <Space size={2}>
          <Tooltip title="编辑规则">
            <Button type="text" size="small" icon={<EditOutlined />} onClick={() => openEditRule(value)} />
          </Tooltip>
          <Popconfirm title="确认删除该分流规则？" onConfirm={() => removeRule.mutate(value.id)}>
            <Tooltip title="删除规则">
              <Button type="text" size="small" danger icon={<DeleteOutlined />} />
            </Tooltip>
          </Popconfirm>
        </Space>
      )
    }
  ];

  const ruleSetColumns: ColumnsType<SingBoxRuleSet> = [
    {
      title: '标签',
      dataIndex: 'tag',
      width: 180,
      render: (value: string) => <Typography.Text strong style={{ fontFamily: 'JetBrains Mono, monospace' }}>{value}</Typography.Text>
    },
    {
      title: '文件格式',
      dataIndex: 'format',
      width: 130,
      render: (value: string) => value === 'binary' ? <Tag color="processing">二进制 (.srs)</Tag> : <Tag>源文件 (.json)</Tag>
    },
    {
      title: '远程地址',
      dataIndex: 'url',
      ellipsis: true,
      render: (value: string) => <Typography.Text copyable={{ text: value }} style={{ fontSize: 12 }}>{value}</Typography.Text>
    },
    {
      title: '下载出口',
      dataIndex: 'downloadDetour',
      width: 130,
      render: (value?: string) => value ? <Tag style={{ fontFamily: 'JetBrains Mono, monospace' }}>{value}</Tag> : <Tag color="success">直连 (direct)</Tag>
    },
    {
      title: '操作',
      width: 90,
      fixed: 'right',
      render: (_, value) => (
        <Space size={2}>
          <Tooltip title="编辑">
            <Button type="text" size="small" icon={<EditOutlined />} onClick={() => openEditRuleSet(value)} />
          </Tooltip>
          <Popconfirm title="确认删除该规则集？" description="请确保无分流规则引用此规则集" onConfirm={() => removeRuleSet.mutate(value.id)}>
            <Tooltip title="删除">
              <Button type="text" size="small" danger icon={<DeleteOutlined />} />
            </Tooltip>
          </Popconfirm>
        </Space>
      )
    }
  ];

  const connectionColumns: ColumnsType<SingBoxConnection> = [
    {
      title: '目标地址 (Host:Port)',
      render: (_, value) => (
        <Typography.Text code style={{ fontSize: 12 }}>
          {value.host ? `${value.host}${value.port ? `:${value.port}` : ''}` : '-'}
        </Typography.Text>
      )
    },
    {
      title: '链路流向',
      render: (_, value) => (
        <Space size={4}>
          <Tag style={{ margin: 0, fontSize: 11 }}>入: {value.inbound || '未知'}</Tag>
          <ArrowRightOutlined style={{ fontSize: 10, color: 'var(--text-secondary)' }} />
          <Tag color="processing" style={{ margin: 0, fontSize: 11 }}>出: {value.outbound || '未知'}</Tag>
        </Space>
      )
    },
    {
      title: '命中规则',
      dataIndex: 'rule',
      render: (value?: string) => value ? <Tag color="default">{value}</Tag> : <Text type="secondary">默认出口</Text>
    },
    {
      title: '流量 (上行 / 下行)',
      render: (_, value) => (
        <Text style={{ fontFamily: 'JetBrains Mono, monospace', fontSize: 12 }}>
          ↑ {formatBytes(value.uploadBytes || 0)} · ↓ {formatBytes(value.downloadBytes || 0)}
        </Text>
      )
    },
    {
      title: '建立时间',
      dataIndex: 'startedAt',
      width: 170,
      render: (value: string) => value ? new Date(value).toLocaleTimeString() : '-'
    },
    {
      title: '断开',
      width: 70,
      fixed: 'right',
      render: (_, value) => (
        <Popconfirm title="确认强制断开这条连接？" onConfirm={() => closeConnection.mutate(value.id)}>
          <Tooltip title="断开连接">
            <Button type="text" size="small" danger icon={<DeleteOutlined />} loading={closeConnection.isPending} />
          </Tooltip>
        </Popconfirm>
      )
    }
  ];

  if (config.isError) {
    return (
      <Result
        status={config.error instanceof ApiError && config.error.status === 403 ? '403' : 'error'}
        title={config.error.message}
        extra={<Button type="primary" icon={<ReloadOutlined />} onClick={() => void config.refetch()}>重试</Button>}
      />
    );
  }

  const isRunning = runtime.data?.kernelStatus === 'running';
  const isUnhealthy = runtime.data?.kernelStatus === 'unhealthy';

  return (
    <Page title="服务配置">
      {/* 顶层主分组 Segmented */}
      <Segmented
        options={tabGroups.map((group) => ({ label: group.label, value: group.key }))}
        value={activeGroup.key}
        onChange={(value) => selectGroup(String(value))}
        style={{ marginBottom: 16 }}
      />

      {/* 二级功能 Tabs */}
      <Tabs
        activeKey={tab}
        onChange={selectTab}
        items={[
          // Tab 1: 运行维护
          {
            key: 'runtime',
            label: (
              <Space size={6}>
                <span className={`pulse-dot ${isRunning ? 'running' : isUnhealthy ? 'error' : 'warning'}`} style={{ width: 8, height: 8, borderRadius: '50%', background: 'currentColor', display: 'inline-block' }} />
                <span>运行维护</span>
              </Space>
            ),
            children: (
              <>
                {/* 运行看板 Hero Card */}
                <div className="singbox-hero-card">
                  <div className="singbox-hero-header">
                    <div className="singbox-hero-title">
                      <Title level={4} style={{ margin: 0 }}>本地 sing-box 内核</Title>
                      <Tag color={isRunning ? 'success' : isUnhealthy ? 'error' : 'default'} style={{ fontSize: 13, padding: '2px 10px', borderRadius: 12 }}>
                        {kernelStatusLabel(runtime.data?.kernelStatus || '加载中')}
                      </Tag>
                      <Tag bordered={false} style={{ fontFamily: 'JetBrains Mono, monospace', fontSize: 12 }}>
                        {runtime.data?.version || '未探测版本'}
                      </Tag>
                      <Tag bordered={false}>{runtime.data?.runtimeMode === 'embedded' ? '嵌入式内核' : runtime.data?.runtimeMode || '单进程'}</Tag>
                    </div>
                    <Space wrap>
                      <Button icon={<CheckCircleOutlined />} loading={checkRuntime.isPending} onClick={() => checkRuntime.mutate()}>
                        检查配置
                      </Button>
                      <Button icon={<SyncOutlined />} loading={reconcileRuntime.isPending} onClick={() => reconcileRuntime.mutate()}>
                        重新同步
                      </Button>
                      {runtime.data?.canRestart && (
                        <Popconfirm
                          title="确认重建 sing-box 内核？"
                          description="重建会使内核重新载入全部出站与路由，现有活动连接会短暂中断。"
                          onConfirm={() => restartRuntime.mutate()}
                          okText="确认重建"
                          cancelText="取消"
                          okButtonProps={{ danger: true }}
                        >
                          <Button danger type="primary" icon={<ReloadOutlined />} loading={restartRuntime.isPending}>
                            重建内核
                          </Button>
                        </Popconfirm>
                      )}
                    </Space>
                  </div>
                  <Divider style={{ margin: '14px 0' }} />
                  <Row gutter={[24, 12]}>
                    <Col xs={24} md={12}>
                      <Text type="secondary">持续运行：</Text>
                      <Text strong style={{ marginLeft: 6 }}>{formatUptime(runtime.data?.startedAt)}</Text>
                      <Text type="secondary" style={{ fontSize: 12, marginLeft: 8 }}>
                        （{runtime.data?.startedAt ? new Date(runtime.data.startedAt).toLocaleString() : '未启动'}）
                      </Text>
                    </Col>
                    <Col xs={24} md={12}>
                      <Text type="secondary">配置状态：</Text>
                      <Tag color={runtime.data?.configStatus === 'applied' ? 'success' : 'warning'} style={{ marginLeft: 6 }}>
                        {configStatusLabel(runtime.data?.configStatus || '加载中')}
                      </Tag>
                      <Text type="secondary" style={{ fontSize: 12, marginLeft: 8 }}>
                        同步于 {runtime.data?.lastAppliedAt ? new Date(runtime.data.lastAppliedAt).toLocaleString() : '暂无'}
                      </Text>
                    </Col>
                  </Row>
                </div>

                {/* 异常警告区域 */}
                {runtime.data?.failureReason && (
                  <Alert
                    style={{ marginBottom: 16 }}
                    type="error"
                    showIcon
                    message="内核最近一次同步失败"
                    description={`${runtime.data.failureReason}。当前保护机制已自动生效，sing-box 仍持续使用上一份成功的运行时配置。`}
                  />
                )}
                {runtime.data?.reason && (
                  <Alert
                    style={{ marginBottom: 16 }}
                    type="warning"
                    showIcon
                    message="运行环境有待处理项"
                    description={runtime.data.reason}
                  />
                )}

                {/* 摘要信息卡片 */}
                <Card size="small" title="当前运行时指纹与摘要" style={{ marginBottom: 16 }}>
                  <Descriptions size="small" column={1}>
                    <Descriptions.Item label="配置摘要">
                      {runtime.data?.configDigest ? (
                        <Typography.Text code copyable={{ text: runtime.data.configDigest }} style={{ fontSize: 12, wordBreak: 'break-all' }}>
                          {runtime.data.configDigest}
                        </Typography.Text>
                      ) : (
                        <Text type="secondary">暂无</Text>
                      )}
                    </Descriptions.Item>
                    <Descriptions.Item label="日志集成">
                      <Text type="secondary">内核日志已由 PlatformLogWriter 自动剥色并转发至系统管理日志</Text>
                    </Descriptions.Item>
                  </Descriptions>
                </Card>
              </>
            )
          },

          // Tab 2: 实时连接
          {
            key: 'connections',
            label: (
              <Space size={6}>
                <span>实时连接</span>
                <Badge count={allConnections.length} overflowCount={999} size="small" style={{ backgroundColor: 'var(--primary-color)' }} />
              </Space>
            ),
            children: (
              <>
                {/* 连接排障工具栏 */}
                <div className="conn-filter-toolbar">
                  <Space wrap size={10}>
                    <Input
                      allowClear
                      prefix={<SearchOutlined style={{ color: 'var(--text-secondary)' }} />}
                      placeholder="搜索域名、IP 或端口..."
                      value={connKeyword}
                      onChange={(e) => setConnKeyword(e.target.value)}
                      style={{ width: 220 }}
                    />
                    <Select
                      value={connOutboundFilter}
                      onChange={setConnOutboundFilter}
                      style={{ width: 140 }}
                      options={[
                        { value: 'all', label: '全部出口' },
                        ...outboundList.map((tag) => ({ value: tag, label: `出口: ${tag}` }))
                      ]}
                    />
                    <Select
                      value={connInboundFilter}
                      onChange={setConnInboundFilter}
                      style={{ width: 140 }}
                      options={[
                        { value: 'all', label: '全部入站' },
                        ...inboundList.map((tag) => ({ value: tag, label: `入站: ${tag}` }))
                      ]}
                    />
                    {(connKeyword || connOutboundFilter !== 'all' || connInboundFilter !== 'all') && (
                      <Button
                        icon={<ClearOutlined />}
                        onClick={() => {
                          setConnKeyword('');
                          setConnOutboundFilter('all');
                          setConnInboundFilter('all');
                        }}
                      >
                        重置
                      </Button>
                    )}
                  </Space>

                  <Space wrap size={12}>
                    <Text type="secondary" style={{ fontSize: 13 }}>
                      匹配 <Text strong>{filteredConnections.length}</Text> / {allConnections.length} 条连接
                    </Text>
                    <Select
                      value={autoRefreshInterval}
                      onChange={setAutoRefreshInterval}
                      style={{ width: 120 }}
                      options={[
                        { value: 0, label: '手动刷新' },
                        { value: 3000, label: '每 3 秒刷新' },
                        { value: 5000, label: '每 5 秒刷新' },
                        { value: 10000, label: '每 10 秒刷新' }
                      ]}
                    />
                    <Button icon={<ReloadOutlined />} onClick={() => void connections.refetch()} loading={connections.isFetching}>
                      刷新
                    </Button>
                  </Space>
                </div>

                {connections.data?.available ? (
                  <Table
                    rowKey="id"
                    size="small"
                    loading={connections.isLoading}
                    dataSource={filteredConnections}
                    columns={connectionColumns}
                    pagination={{ pageSize: 20, showSizeChanger: true, pageSizeOptions: ['10', '20', '50', '100'] }}
                    scroll={{ x: 860 }}
                  />
                ) : (
                  <Alert
                    type="info"
                    showIcon
                    message="实时连接不可用"
                    description={connections.data?.reason || '尚未配置本机或内部 Clash API，可在「基础配置」中开启以启用实时连接观测。'}
                  />
                )}
              </>
            )
          },

          // Tab 3: 基础配置
          {
            key: 'base',
            label: '基础配置',
            children: (
              <Form<ConfigValues> form={configForm} layout="vertical" onFinish={() => updateConfig.mutate(configForm.getFieldsValue(true) as ConfigValues)}>
                <div className="page-toolbar" style={{ marginBottom: 16 }}>
                  <Typography.Text type="secondary">更改会自动编译校验并热同步到本地 sing-box 内核。</Typography.Text>
                  <Button type="primary" htmlType="submit" loading={updateConfig.isPending} icon={<CheckCircleOutlined />}>
                    保存并同步
                  </Button>
                </div>

                {/* 分组卡片 1: 内核与日志 */}
                <div className="singbox-config-card">
                  <div className="singbox-config-card-title">
                    <Title level={5} style={{ margin: 0 }}>内核全局参数</Title>
                    <Tag>核心设置</Tag>
                  </div>
                  <Row gutter={16}>
                    <Col xs={24} md={8}>
                      <Form.Item name="logLevel" label="内核日志等级" rules={[{ required: true }]}>
                        <Select options={logLevelOptions} />
                      </Form.Item>
                    </Col>
                    <Col xs={24} md={8}>
                      <Form.Item name="defaultOutboundTag" label="默认兜底出口" rules={[{ required: true }]} tooltip="当所有服务器分流规则均未命中时的出站出口">
                        <Select options={outboundOptions} />
                      </Form.Item>
                    </Col>
                    <Col xs={24} md={8}>
                      <Form.Item name="logTimestamp" label="日志时间戳" valuePropName="checked">
                        <Switch />
                      </Form.Item>
                    </Col>
                  </Row>
                </div>

                {/* 分组卡片 2: Clash API */}
                <div className="singbox-config-card">
                  <div className="singbox-config-card-title">
                    <Space>
                      <Title level={5} style={{ margin: 0 }}>Clash API（连接观测）</Title>
                      <Tag color="processing">仅用于连接监控</Tag>
                    </Space>
                    <Form.Item name="clashApiEnabled" valuePropName="checked" style={{ margin: 0 }}>
                      <Switch checkedChildren="已开启" unCheckedChildren="已关闭" />
                    </Form.Item>
                  </div>
                  <Text type="secondary" style={{ display: 'block', marginBottom: 16, fontSize: 13 }}>
                    提供实时连接快照（/connections），仅监听回环地址，严禁暴露公网。
                  </Text>
                  <Row gutter={16}>
                    <Col xs={24} md={12}>
                      <Form.Item name="clashApiListen" label="监听地址" extra="默认仅监听本机 127.0.0.1:9090">
                        <Input disabled={!clashApiEnabled} placeholder="127.0.0.1:9090" />
                      </Form.Item>
                    </Col>
                    <Col xs={24} md={12}>
                      <Form.Item
                        name="clashApiSecret"
                        label={
                          <Space>
                            <span>API 访问密钥</span>
                            {clashApiEnabled && (
                              <Button
                                type="link"
                                size="small"
                                icon={<KeyOutlined />}
                                onClick={() => {
                                  const secret = generateRandomSecret(24);
                                  configForm.setFieldsValue({ clashApiSecret: secret });
                                  message.success('已生成随机密钥并填入');
                                }}
                                style={{ padding: 0 }}
                              >
                                随机生成
                              </Button>
                            )}
                          </Space>
                        }
                        extra={config.data?.clashApiSecretSet ? '已设置密钥，留空保持原值' : '建议配置以防未授权访问'}
                      >
                        <Input.Password disabled={!clashApiEnabled} placeholder={config.data?.clashApiSecretSet ? '••••••••' : '请输入 API 密钥'} />
                      </Form.Item>
                    </Col>
                  </Row>
                </div>

                {/* 分组卡片 3: V2Ray API */}
                <div className="singbox-config-card">
                  <div className="singbox-config-card-title">
                    <Space>
                      <Title level={5} style={{ margin: 0 }}>V2Ray API（流量采集）</Title>
                      <Tag color="success">核心计费与采集</Tag>
                    </Space>
                    <Form.Item name="v2rayApiEnabled" valuePropName="checked" style={{ margin: 0 }}>
                      <Switch checkedChildren="已开启" unCheckedChildren="已关闭" />
                    </Form.Item>
                  </div>
                  <Text type="secondary" style={{ display: 'block', marginBottom: 16, fontSize: 13 }}>
                    提供 gRPC stats 接口供面板归因采集用户设备流量与扣减额度。
                  </Text>
                  <Row gutter={16}>
                    <Col xs={24} md={12}>
                      <Form.Item name="v2rayApiListen" label="gRPC 监听地址" extra="内部回环地址，如 127.0.0.1:10000 或 0.0.0.0:10000">
                        <Input disabled={!v2rayApiEnabled} placeholder="0.0.0.0:10000" />
                      </Form.Item>
                    </Col>
                  </Row>
                </div>

              </Form>
            )
          },

          // Tab 4: DNS 配置
          {
            key: 'dns',
            label: 'DNS 解析',
            children: (
              <Form<ConfigValues> form={configForm} layout="vertical" onFinish={() => updateConfig.mutate(configForm.getFieldsValue(true) as ConfigValues)}>
                <div className="page-toolbar" style={{ marginBottom: 16 }}>
                  <Typography.Text type="secondary">配置 sing-box 内核内置 DNS 服务器。可通过出口（Detour）分流指定 DNS 请求。</Typography.Text>
                  <Button type="primary" htmlType="submit" loading={updateConfig.isPending} icon={<CheckCircleOutlined />}>
                    保存并同步
                  </Button>
                </div>

                <div className="singbox-config-card">
                  <div className="singbox-config-card-title">
                    <Title level={5} style={{ margin: 0 }}>DNS 服务器列表</Title>
                    <Space wrap>
                      <Dropdown
                        menu={{
                          items: publicDNSPresets.map((preset) => ({
                            key: preset.tag,
                            label: preset.label,
                            onClick: () => handleAddPresetDNS(preset)
                          }))
                        }}
                      >
                        <Button icon={<PlusOutlined />}>
                          添加常用公共 DNS
                        </Button>
                      </Dropdown>
                      <Form.List name="dnsServers">
                        {(_, { add }) => (
                          <Button type="primary" icon={<PlusOutlined />} onClick={() => add({ tag: '', address: '', detour: '' })}>
                            添加自定义 DNS
                          </Button>
                        )}
                      </Form.List>
                    </Space>
                  </div>

                  <Form.List name="dnsServers">
                    {(fields, { remove }) => (
                      <Table
                        size="small"
                        pagination={false}
                        rowKey="key"
                        dataSource={fields}
                        columns={[
                          {
                            title: '标签 (Tag)',
                            width: 170,
                            render: (_, field) => (
                              <Form.Item name={[field.name, 'tag']} rules={[{ required: true, message: '必填' }]} style={{ margin: 0 }}>
                                <Input placeholder="如 cf-dns" style={{ fontFamily: 'JetBrains Mono, monospace' }} />
                              </Form.Item>
                            )
                          },
                          {
                            title: 'DNS 地址 (支持 DoH / DoT / UDP / TCP)',
                            render: (_, field) => (
                              <Form.Item name={[field.name, 'address']} rules={[{ required: true, message: '必填' }]} style={{ margin: 0 }}>
                                <Input placeholder="https://1.1.1.1/dns-query" style={{ fontFamily: 'JetBrains Mono, monospace' }} />
                              </Form.Item>
                            )
                          },
                          {
                            title: '请求出口 (Detour)',
                            width: 170,
                            render: (_, field) => (
                              <Form.Item name={[field.name, 'detour']} style={{ margin: 0 }}>
                                <Select allowClear placeholder="直连 (direct)" options={outboundOptions} />
                              </Form.Item>
                            )
                          },
                          {
                            title: '操作',
                            width: 60,
                            render: (_, field) => (
                              <Button type="text" danger icon={<DeleteOutlined />} aria-label="删除 DNS" onClick={() => remove(field.name)} />
                            )
                          }
                        ]}
                      />
                    )}
                  </Form.List>

                  <Divider style={{ margin: '20px 0 16px' }} />
                  <Form.Item name="dnsFinal" label="最终兜底 DNS" style={{ maxWidth: 360, margin: 0 }} tooltip="当所有 DNS 规则均未命中时所采用的兜底服务器">
                    <Select
                      allowClear
                      placeholder="选择最终 DNS 服务器"
                      options={(configForm.getFieldValue('dnsServers') || [])
                        .filter((item: { tag?: string }) => item.tag)
                        .map((item: { tag: string }) => ({ value: item.tag, label: item.tag }))}
                    />
                  </Form.Item>
                </div>
              </Form>
            )
          },

          // Tab 5: 出站管理
          {
            key: 'outbounds',
            label: (
              <Space size={6}>
                <span>出站出口</span>
                <Badge count={outbounds.data?.items?.length || 0} size="small" style={{ backgroundColor: 'var(--primary-color)' }} />
              </Space>
            ),
            children: (
              <>
                <div className="page-toolbar" style={{ marginBottom: 16 }}>
                  <Typography.Text type="secondary">
                    出站是流量转发的目标出口。内建 direct（直连）与 block（拦截）不可删除；支持添加 SOCKS 落地机与 WARP (WireGuard) 双栈出口。
                  </Typography.Text>
                  <Space wrap>
                    <Button icon={<ReloadOutlined />} onClick={() => void outbounds.refetch()} loading={outbounds.isFetching}>
                      刷新
                    </Button>
                    <Button type="primary" icon={<PlusOutlined />} onClick={openCreateOutbound}>
                      添加出站
                    </Button>
                  </Space>
                </div>

                <Table
                  rowKey="id"
                  loading={outbounds.isLoading}
                  dataSource={outbounds.data?.items || []}
                  columns={outboundColumns}
                  pagination={false}
                  scroll={{ x: 740 }}
                />
              </>
            )
          },

          // Tab 6: 服务器分流
          {
            key: 'routes',
            label: (
              <Space size={6}>
                <span>服务器分流</span>
                <Badge count={rules.data?.items?.length || 0} size="small" style={{ backgroundColor: 'var(--primary-color)' }} />
              </Space>
            ),
            children: (
              <>
                <div className="page-toolbar" style={{ marginBottom: 16 }}>
                  <Typography.Text type="secondary">
                    服务器分流：作用于内核转发，全局生效；不改变客户端本地行为。自上而下匹配，首条命中即终止。
                  </Typography.Text>
                  <Space wrap>
                    <Button onClick={() => navigate('/policies')}>前往订阅分流</Button>
                    <Button icon={<ExperimentOutlined />} onClick={() => setDryRunDrawer(true)}>
                      路由仿真测试
                    </Button>
                    <Button icon={<SafetyCertificateOutlined />} onClick={() => { setValidationDrawer(true); validate.mutate(); }}>
                      验证配置
                    </Button>
                    <Button type="primary" icon={<PlusOutlined />} onClick={openCreateRule}>
                      添加分流规则
                    </Button>
                    <Space.Compact>
                      <Select style={{ width: 140 }} placeholder="AI 解锁出站" value={aiOutbound} onChange={setAiOutbound} options={outboundOptions} />
                      <Button
                        type="primary"
                        loading={applyAITemplate.isPending}
                        disabled={!aiOutbound}
                        onClick={() => aiOutbound && applyAITemplate.mutate(aiOutbound)}
                      >
                        一键 AI 解锁
                      </Button>
                    </Space.Compact>
                  </Space>
                </div>

                <Table
                  rowKey="id"
                  loading={rules.isLoading || orderRules.isPending}
                  dataSource={rules.data?.items || []}
                  columns={ruleColumns}
                  pagination={false}
                  scroll={{ x: 800 }}
                />
              </>
            )
          },

          // Tab 7: 规则集
          {
            key: 'rulesets',
            label: (
              <Space size={6}>
                <span>远程规则集</span>
                <Badge count={ruleSets.data?.items?.length || 0} size="small" style={{ backgroundColor: 'var(--primary-color)' }} />
              </Space>
            ),
            children: (
              <>
                <div className="page-toolbar" style={{ marginBottom: 16 }}>
                  <Typography.Text type="secondary">
                    远程规则集由 sing-box 内核定期下载并本地缓存，供服务器分流规则引用。
                  </Typography.Text>
                  <Space wrap>
                    <Button icon={<ReloadOutlined />} onClick={() => void ruleSets.refetch()} loading={ruleSets.isFetching}>
                      刷新
                    </Button>
                    <Button type="primary" icon={<PlusOutlined />} onClick={openCreateRuleSet}>
                      添加规则集
                    </Button>
                  </Space>
                </div>

                <Table
                  rowKey="id"
                  size="small"
                  loading={ruleSets.isLoading}
                  dataSource={ruleSets.data?.items || []}
                  columns={ruleSetColumns}
                  pagination={false}
                  scroll={{ x: 700 }}
                />
              </>
            )
          },

          // Tab 8: Argo 隧道
          {
            key: 'argo',
            label: (
              <Space size={6}>
                <span>Argo 隧道</span>
                <Badge count={argos.data?.items?.length || 0} size="small" style={{ backgroundColor: 'var(--primary-color)' }} />
              </Space>
            ),
            children: (
              <>
                <div className="page-toolbar" style={{ marginBottom: 16 }}>
                  <Typography.Text type="secondary">
                    通过 cloudflared 将 vmess(WS) 入站安全穿透至 Cloudflare 边缘网络，免暴露 VPS 原生端口。
                  </Typography.Text>
                  <Space wrap>
                    <Button icon={<ReloadOutlined />} onClick={() => void argos.refetch()} loading={argos.isFetching}>
                      刷新
                    </Button>
                    <Button type="primary" icon={<PlusOutlined />} onClick={openCreateArgo} disabled={vmessProtocols.length === 0}>
                      添加隧道
                    </Button>
                  </Space>
                </div>

                {vmessProtocols.length === 0 && (
                  <Alert type="info" showIcon message="需要先创建已启用的 vmess+WS 协议以作为 Argo 隧道的本地转发目标" style={{ marginBottom: 16 }} />
                )}

                <Table
                  rowKey="id"
                  size="small"
                  loading={argos.isLoading}
                  dataSource={argos.data?.items || []}
                  columns={argoColumns}
                  pagination={false}
                  scroll={{ x: 860 }}
                />
              </>
            )
          }
        ].filter((item) => activeGroup.tabs.includes(item.key))}
      />

      {/* 抽屉 1: 出站配置 */}
      <Drawer
        title={editingOutbound ? '编辑出站' : '添加出站'}
        width={460}
        open={outboundDrawer}
        destroyOnClose
        onClose={closeOutbound}
        extra={
          <Button type="primary" loading={createOutbound.isPending || updateOutbound.isPending} onClick={() => outboundForm.submit()}>
            保存
          </Button>
        }
      >
        <Form<OutboundValues>
          form={outboundForm}
          layout="vertical"
          onFinish={(values) => (editingOutbound ? updateOutbound.mutate(values) : createOutbound.mutate(values))}
        >
          <Form.Item name="type" label="出站类型" rules={[{ required: true }]}>
            <Select
              disabled={Boolean(editingOutbound)}
              options={[
                { value: 'socks', label: 'SOCKS5 代理出站' },
                { value: 'warp', label: 'Cloudflare WARP (WireGuard)' }
              ]}
              onChange={() => outboundForm.setFieldsValue({ server: '', serverPort: undefined, username: '', password: '' })}
            />
          </Form.Item>
          <Form.Item name="tag" label="出站标签 (Tag)" rules={[{ required: true, whitespace: true, message: '请输入唯一标签' }]} tooltip="在分流规则中引用的唯一标识">
            <Input disabled={Boolean(editingOutbound)} placeholder="如 warp-out / socks-node-1" style={{ fontFamily: 'JetBrains Mono, monospace' }} />
          </Form.Item>

          <Form.Item noStyle shouldUpdate={(prev, next) => prev.type !== next.type}>
            {({ getFieldValue }) =>
              getFieldValue('type') === 'warp' ? (
                <>
                  <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', marginBottom: 16, padding: '10px 12px', background: 'rgba(var(--primary-color-rgb), 0.06)', borderRadius: 8 }}>
                    <Text type="secondary" style={{ fontSize: 12 }}>无需账号，一键向 Cloudflare API 申请免费双栈 WireGuard 凭据</Text>
                    <Button size="small" type="primary" loading={registerWarp.isPending} onClick={() => registerWarp.mutate()}>
                      注册免费 WARP
                    </Button>
                  </div>
                  <Form.Item name={['warp', 'endpoint']} label="对端接入点 (Endpoint)" tooltip="默认 engage.cloudflareclient.com:2408；如果运营商阻断可填优选 IP:端口">
                    <Input placeholder="engage.cloudflareclient.com:2408" style={{ fontFamily: 'JetBrains Mono, monospace' }} />
                  </Form.Item>
                  <Form.Item name={['warp', 'publicKey']} label="对端公钥 (Public Key)" rules={[{ required: true, whitespace: true }]}>
                    <Input style={{ fontFamily: 'JetBrains Mono, monospace' }} />
                  </Form.Item>
                  <Form.Item
                    name={['warp', 'privateKey']}
                    label={editingOutbound?.warp?.privateKeySet ? '本地私钥 (Private Key，留空保持原值)' : '本地私钥 (Private Key)'}
                    rules={[() => ({ validator: (_, value) => (!value && editingOutbound?.warp?.privateKeySet) || value ? Promise.resolve() : Promise.reject(new Error('必填')) })]}
                  >
                    <Input.Password style={{ fontFamily: 'JetBrains Mono, monospace' }} />
                  </Form.Item>
                  <Row gutter={12}>
                    <Col span={12}>
                      <Form.Item name={['warp', 'ipv4']} label="IPv4 CIDR" rules={[{ required: true, whitespace: true }]}>
                        <Input placeholder="172.16.0.2/32" style={{ fontFamily: 'JetBrains Mono, monospace' }} />
                      </Form.Item>
                    </Col>
                    <Col span={12}>
                      <Form.Item name={['warp', 'ipv6']} label="IPv6 CIDR (可选)">
                        <Input placeholder="2606:4700:.../128" style={{ fontFamily: 'JetBrains Mono, monospace' }} />
                      </Form.Item>
                    </Col>
                  </Row>
                  <Form.Item name={['warp', 'reserved']} label="Reserved 校验码 (可选)" tooltip="逗号分隔的三字节，如 26,105,116；部分地区连接需精准对齐">
                    <Input placeholder="26,105,116" style={{ fontFamily: 'JetBrains Mono, monospace' }} />
                  </Form.Item>
                  <Form.Item name={['warp', 'mtu']} label="MTU (默认 1420)">
                    <InputNumber min={512} max={65535} style={{ width: '100%' }} placeholder="1420" />
                  </Form.Item>
                </>
              ) : (
                <>
                  <Row gutter={12}>
                    <Col span={16}>
                      <Form.Item name="server" label="服务器主机或 IP" rules={[{ required: true, whitespace: true }]}>
                        <Input placeholder="1.2.3.4 或 node.example.com" style={{ fontFamily: 'JetBrains Mono, monospace' }} />
                      </Form.Item>
                    </Col>
                    <Col span={8}>
                      <Form.Item name="serverPort" label="端口" rules={[{ required: true }]}>
                        <InputNumber min={1} max={65535} style={{ width: '100%' }} placeholder="1080" />
                      </Form.Item>
                    </Col>
                  </Row>
                  <Form.Item name="username" label="用户名 (可选)">
                    <Input placeholder="留空为无用户名认证" />
                  </Form.Item>
                  <Form.Item name="password" label={editingOutbound?.hasPassword ? '密码 (留空保持原值)' : '密码 (可选)'}>
                    <Input.Password placeholder="留空为无密码认证" />
                  </Form.Item>
                </>
              )
            }
          </Form.Item>
        </Form>
      </Drawer>

      {/* 抽屉 2: 分流规则配置 (结构化折叠面板) */}
      <Drawer
        title={editingRule ? '编辑分流规则' : '添加分流规则'}
        width={540}
        open={ruleDrawer}
        destroyOnClose
        onClose={closeRule}
        extra={
          <Button type="primary" loading={createRule.isPending || updateRule.isPending} onClick={() => ruleForm.submit()}>
            保存
          </Button>
        }
      >
        <Form<RuleValues>
          form={ruleForm}
          layout="vertical"
          onFinish={(values) => (editingRule ? updateRule.mutate(values) : createRule.mutate(values))}
        >
          {/* 基础属性卡片 */}
          <Card size="small" style={{ marginBottom: 16 }}>
            <Form.Item name="name" label="规则名称" rules={[{ required: true, whitespace: true, message: '请输入规则名称' }]}>
              <Input placeholder="如：主流 AI 服务 / 广告拦截 / 国内直连" />
            </Form.Item>
            <Row gutter={12}>
              <Col span={14}>
                <Form.Item name="outboundTag" label="出口 (Outbound)" rules={[{ required: true, message: '请选择出口' }]}>
                  <Select options={outboundOptions} />
                </Form.Item>
              </Col>
              <Col span={10}>
                <Form.Item name="priority" label="优先级" rules={[{ required: true }]} tooltip="数值越小越优先">
                  <InputNumber min={0} style={{ width: '100%' }} />
                </Form.Item>
              </Col>
            </Row>
            <Form.Item name="enabled" label="立即启用该规则" valuePropName="checked" style={{ margin: 0 }}>
              <Switch />
            </Form.Item>
          </Card>

          {/* 条件折叠分组 */}
          <Collapse
            defaultActiveKey={['domain-network']}
            items={[
              {
                key: 'domain-network',
                label: <Text strong>域名与网络条件</Text>,
                children: (
                  <>
                    <Form.Item name="domainSuffixes" label="域名后缀" tooltip="如 youtube.com，匹配 *.youtube.com">
                      <Select mode="tags" tokenSeparators={[',', ' ']} placeholder="输入后缀回车，如 openai.com" />
                    </Form.Item>
                    <Form.Item name="domains" label="精确完整域名">
                      <Select mode="tags" tokenSeparators={[',', ' ']} placeholder="输入完整域名回车" />
                    </Form.Item>
                    <Row gutter={12}>
                      <Col span={14}>
                        <Form.Item name="ports" label="目标端口">
                          <Select mode="tags" tokenSeparators={[',', ' ']} placeholder="如 443, 80" />
                        </Form.Item>
                      </Col>
                      <Col span={10}>
                        <Form.Item name="networks" label="网络协议">
                          <Select mode="multiple" options={[{ value: 'tcp', label: 'TCP' }, { value: 'udp', label: 'UDP' }]} />
                        </Form.Item>
                      </Col>
                    </Row>
                    <Form.Item name="ipCidrs" label="目标 IP 网段 (CIDR)">
                      <Select mode="tags" tokenSeparators={[',', ' ']} placeholder="如 192.168.0.0/16, 1.1.1.1/32" />
                    </Form.Item>
                  </>
                )
              },
              {
                key: 'geo-rulesets',
                label: <Text strong>地理位置与规则集（高级）</Text>,
                children: (
                  <>
                    <Form.Item name="geosite" label="GeoSite 代码" extra="可从下拉选择常见服务分类，亦可手动输入">
                      <Select mode="tags" tokenSeparators={[',', ' ']} options={geoSiteOptions} placeholder="选择或输入 GeoSite" />
                    </Form.Item>
                    <Form.Item name="geoip" label="GeoIP 国家代码" extra="可从下拉选择，亦可手动输入 ISO 国家代码（如 cn, us）">
                      <Select mode="tags" tokenSeparators={[',', ' ']} options={geoIPOptions} placeholder="选择或输入 GeoIP" />
                    </Form.Item>
                    <Form.Item name="ruleSets" label="引用已同步的规则集" extra="在「远程规则集」中管理的规则集合">
                      <Select mode="multiple" options={ruleSetOptions} placeholder="选择引用的规则集" />
                    </Form.Item>
                    <Form.Item name="inboundTags" label="限定入站标签 (Inbound Tags)" extra="仅匹配从特定入站协议进来的流量">
                      <Select mode="tags" tokenSeparators={[',', ' ']} placeholder="如 mixed-in" />
                    </Form.Item>
                  </>
                )
              }
            ]}
          />
        </Form>
      </Drawer>

      {/* 抽屉 3: 路由仿真测试 (Dry-run Drawer) */}
      <Drawer
        title="路由仿真测试 (Dry-run)"
        width={560}
        open={dryRunDrawer}
        destroyOnClose
        onClose={() => setDryRunDrawer(false)}
      >
        <Paragraph type="secondary" style={{ fontSize: 13 }}>
          模拟指定目标网络包在服务器内核的实际匹配轨迹。无需真实发包，即时评估将命中的首条分流规则及最终出口。
        </Paragraph>

        <Form<DryRunValues>
          form={dryRunForm}
          layout="vertical"
          initialValues={{ host: 'example.com', port: 443, network: 'tcp' }}
          onFinish={(values) => dryRun.mutate(values)}
          style={{ marginBottom: 20 }}
        >
          <Row gutter={12}>
            <Col span={16}>
              <Form.Item name="host" label="目标域名或 IP 地址" rules={[{ required: true, message: '必填' }]}>
                <Input placeholder="如 google.com 或 1.1.1.1" style={{ fontFamily: 'JetBrains Mono, monospace' }} />
              </Form.Item>
            </Col>
            <Col span={8}>
              <Form.Item name="port" label="端口">
                <InputNumber min={1} max={65535} style={{ width: '100%' }} placeholder="443" />
              </Form.Item>
            </Col>
          </Row>
          <Row gutter={12}>
            <Col span={12}>
              <Form.Item name="network" label="传输协议">
                <Select options={[{ value: 'tcp', label: 'TCP' }, { value: 'udp', label: 'UDP' }]} />
              </Form.Item>
            </Col>
            <Col span={12}>
              <Form.Item name="inboundTag" label="模拟入站标签 (可选)">
                <Input placeholder="留空模拟默认入站" />
              </Form.Item>
            </Col>
          </Row>
          <Button type="primary" htmlType="submit" loading={dryRun.isPending} icon={<ThunderboltOutlined />} block>
            执行仿真评估
          </Button>
        </Form>

        {dryRunResult ? (
          <div>
            <Alert
              type="info"
              showIcon
              message={
                <Space>
                  <span>最终判定出口：</span>
                  <Tag color="processing" style={{ fontSize: 13, fontFamily: 'JetBrains Mono, monospace' }}>
                    {dryRunResult.outboundTag}
                  </Tag>
                </Space>
              }
              style={{ marginBottom: 14 }}
            />
            <Title level={5} style={{ fontSize: 13, marginBottom: 8 }}>规则判定轨迹 (自顶向下执行)：</Title>
            <Table
              rowKey="ruleId"
              size="small"
              pagination={false}
              dataSource={dryRunResult.trace}
              columns={[
                { title: '规则名称', dataIndex: 'name', render: (v: string) => <Text strong>{v}</Text> },
                {
                  title: '判定结果',
                  dataIndex: 'status',
                  width: 90,
                  render: (value: string) =>
                    value === 'match' ? <Tag color="success">命中</Tag> : value === 'miss' ? <Tag>未匹配</Tag> : value === 'skipped' ? <Tag color="warning">已停用</Tag> : <Tag>未知</Tag>
                },
                {
                  title: '原因说明',
                  dataIndex: 'reason',
                  render: (value: string) =>
                    ({
                      'rule disabled': '规则已停用',
                      'geoip/geosite condition requires the live kernel to evaluate': 'geoip/geosite 需内核在线评估',
                      'all conditions matched': '所有条件均命中',
                      'domain did not match': '域名不匹配',
                      'domain suffix did not match': '域名后缀不匹配',
                      'host is not an IP address': '输入不是 IP 地址',
                      'IP CIDR did not match': 'IP 网段不匹配',
                      'port did not match': '端口不匹配',
                      'network did not match': '网络类型不匹配',
                      'inbound tag did not match': '入站标签不匹配',
                      'rule set did not match': '规则集未命中'
                    }[value] || value)
                },
                {
                  title: '指定出口',
                  dataIndex: 'outboundTag',
                  width: 100,
                  render: (v: string) => <Tag style={{ fontFamily: 'JetBrains Mono, monospace' }}>{v}</Tag>
                }
              ]}
            />
          </div>
        ) : (
          <Empty image={Empty.PRESENTED_IMAGE_SIMPLE} description="输入目标参数后点击执行仿真评估" />
        )}
      </Drawer>

      {/* 抽屉 4: 配置全链路验证 (Validation Drawer) */}
      <Drawer
        title="配置全链路验证"
        width={480}
        open={validationDrawer}
        destroyOnClose
        onClose={() => setValidationDrawer(false)}
        extra={
          <Button icon={<SyncOutlined />} loading={validate.isPending} onClick={() => validate.mutate()}>
            重新验证
          </Button>
        }
      >
        <Paragraph type="secondary" style={{ fontSize: 13 }}>
          对当前全部出站引用、默认出口、DNS detour 以及规则集 download_detour 进行静态图谱闭包校验，杜绝悬空引用导致的断网风险。
        </Paragraph>

        {validate.isPending ? (
          <div style={{ textAlign: 'center', padding: '60px 0' }}>
            <Spin size="large" />
            <Text type="secondary" style={{ display: 'block', marginTop: 12 }}>正在深度校验内核配置图谱...</Text>
          </div>
        ) : validation ? (
          validation.issues.length === 0 ? (
            <Alert
              type="success"
              showIcon
              message="配置全链路校验通过"
              description="所有分流规则出口、DNS Detour、规则集下载出口引用均完整有效，内核可安全热重启。"
            />
          ) : (
            <div>
              <Alert
                type="warning"
                showIcon
                message={`发现 ${validation.issues.length} 处潜在配置问题`}
                description="存在未解析或悬空的出口引用，命中该规则的连接将无法正常拨号："
                style={{ marginBottom: 14 }}
              />
              <Space direction="vertical" style={{ width: '100%' }}>
                {validation.issues.map((issue, index) => (
                  <Alert
                    key={`${issue.ruleId}-${index}`}
                    type={issue.level === 'error' ? 'error' : 'warning'}
                    message={issue.message}
                    showIcon
                  />
                ))}
              </Space>
            </div>
          )
        ) : null}
      </Drawer>

      {/* 抽屉 5: Argo 隧道配置 */}
      <Drawer
        title={editingArgo ? '编辑 Argo 隧道' : '添加 Argo 隧道'}
        width={460}
        open={argoDrawer}
        destroyOnClose
        onClose={closeArgo}
        extra={
          <Button type="primary" loading={createArgo.isPending || updateArgo.isPending} onClick={() => argoForm.submit()}>
            保存
          </Button>
        }
      >
        <Form<ArgoValues>
          form={argoForm}
          layout="vertical"
          onFinish={(values) => (editingArgo ? updateArgo.mutate(values) : createArgo.mutate(values))}
        >
          <Form.Item name="name" label="隧道名称" rules={[{ required: true, whitespace: true }]}>
            <Input placeholder="如 cf-argo-vmess" />
          </Form.Item>
          <Form.Item name="mode" label="隧道接入模式" rules={[{ required: true }]}>
            <Select
              options={[
                { value: 'temporary', label: '临时隧道（trycloudflare，免账号，重启自动换址）' },
                { value: 'token', label: '固定域名（Cloudflare Tunnel Token）' }
              ]}
            />
          </Form.Item>
          <Form.Item name="protocolId" label="承载协议（vmess+ws）" rules={[{ required: true }]}>
            <Select options={vmessProtocols.map((item) => ({ value: item.id, label: item.name }))} />
          </Form.Item>
          <Form.Item noStyle shouldUpdate={(prev, next) => prev.mode !== next.mode}>
            {({ getFieldValue }) =>
              getFieldValue('mode') === 'token' ? (
                <>
                  <Form.Item name="fixedDomain" label="固定边缘域名" rules={[{ required: true, whitespace: true }]}>
                    <Input placeholder="tunnel.example.com" style={{ fontFamily: 'JetBrains Mono, monospace' }} />
                  </Form.Item>
                  <Form.Item
                    name="token"
                    label={editingArgo?.tokenSet ? 'Tunnel Token (留空保持原值)' : 'Cloudflare Tunnel Token'}
                    rules={[() => ({ validator: (_, value) => (!value && editingArgo?.tokenSet) || value ? Promise.resolve() : Promise.reject(new Error('必填')) })]}
                  >
                    <Input.TextArea rows={3} placeholder="eyJhIjoi..." style={{ fontFamily: 'JetBrains Mono, monospace' }} />
                  </Form.Item>
                </>
              ) : null
            }
          </Form.Item>
          <Form.Item name="preferredAddress" label="优选 CDN 地址 (可选)" extra="订阅分发时客户端拨号连接的目标地址；留空直接使用边缘域名">
            <Input placeholder="如 icook.tw 或自选优质 Cloudflare IP" style={{ fontFamily: 'JetBrains Mono, monospace' }} />
          </Form.Item>
          <Form.Item name="enabled" label="立即启用" valuePropName="checked">
            <Switch />
          </Form.Item>
        </Form>
      </Drawer>

      {/* 抽屉 6: 规则集配置 */}
      <Drawer
        title={editingRuleSet ? '编辑规则集' : '添加规则集'}
        width={460}
        open={ruleSetDrawer}
        destroyOnClose
        onClose={closeRuleSet}
        extra={
          <Button type="primary" loading={createRuleSet.isPending || updateRuleSet.isPending} onClick={() => ruleSetForm.submit()}>
            保存
          </Button>
        }
      >
        <Form<RuleSetValues>
          form={ruleSetForm}
          layout="vertical"
          onFinish={(values) => (editingRuleSet ? updateRuleSet.mutate(values) : createRuleSet.mutate(values))}
        >
          <Form.Item name="tag" label="规则集标签 (Tag)" rules={[{ required: true, whitespace: true }]} tooltip="在分流规则中引用的标识名">
            <Input disabled={Boolean(editingRuleSet)} placeholder="如 geosite-google" style={{ fontFamily: 'JetBrains Mono, monospace' }} />
          </Form.Item>
          <Form.Item name="format" label="格式类型" rules={[{ required: true }]}>
            <Select
              options={[
                { value: 'binary', label: '二进制编译格式 (.srs，推荐性能最高)' },
                { value: 'source', label: '源文件文本格式 (.json)' }
              ]}
            />
          </Form.Item>
          <Form.Item name="url" label="下载地址 (URL)" rules={[{ required: true, whitespace: true }]}>
            <Input placeholder="https://raw.githubusercontent.com/.../xxx.srs" style={{ fontFamily: 'JetBrains Mono, monospace' }} />
          </Form.Item>
          <Form.Item name="downloadDetour" label="下载出口 (Detour)" extra="留空则通过 direct 直连下载">
            <Select allowClear placeholder="直连 (direct)" options={outboundOptions} />
          </Form.Item>
        </Form>
      </Drawer>
    </Page>
  );
}

// 格式化呈现分流规则条件的紧凑标签组
function conditionSummary(value: SingBoxRouteConditions) {
  const tags: React.ReactNode[] = [];

  if (value.domainSuffixes?.length) {
    tags.push(
      <Tag key="ds" color="processing">
        {value.domainSuffixes.length === 1 ? `*.${value.domainSuffixes[0]}` : `${value.domainSuffixes.length} 个后缀`}
      </Tag>
    );
  }
  if (value.domains?.length) {
    tags.push(
      <Tag key="d" color="processing">
        {value.domains.length === 1 ? value.domains[0] : `${value.domains.length} 个精确域名`}
      </Tag>
    );
  }
  if (value.geosite?.length) {
    tags.push(
      <Tag key="gs" color="purple">
        {value.geosite.length <= 2 ? value.geosite.map((g) => `geosite:${g}`).join(' ') : `${value.geosite.length} 个 GeoSite`}
      </Tag>
    );
  }
  if (value.geoip?.length) {
    tags.push(
      <Tag key="gi" color="cyan">
        {value.geoip.length <= 2 ? value.geoip.map((g) => `geoip:${g}`).join(' ') : `${value.geoip.length} 个 GeoIP`}
      </Tag>
    );
  }
  if (value.ruleSets?.length) {
    tags.push(
      <Tag key="rs" color="default">
        {value.ruleSets.length <= 2 ? value.ruleSets.map((r) => `rs:${r}`).join(' ') : `${value.ruleSets.length} 个规则集`}
      </Tag>
    );
  }
  if (value.ipCidrs?.length) {
    tags.push(
      <Tag key="ip">
        {value.ipCidrs.length === 1 ? value.ipCidrs[0] : `${value.ipCidrs.length} 个 IP 段`}
      </Tag>
    );
  }
  if (value.ports?.length) {
    tags.push(
      <Tag key="port">
        {value.ports.length === 1 ? `:${value.ports[0]}` : `${value.ports.length} 个端口`}
      </Tag>
    );
  }
  if (value.networks?.length) {
    tags.push(<Tag key="net">{value.networks.join('/')}</Tag>);
  }
  if (value.inboundTags?.length) {
    tags.push(<Tag key="in">in:{value.inboundTags.join(',')}</Tag>);
  }

  if (!tags.length) {
    return <Tag>全部流量 (兜底)</Tag>;
  }

  const fullTooltip = [
    value.domains?.join(', '),
    value.domainSuffixes?.map((i) => `*.${i}`).join(', '),
    value.geosite?.map((i) => `geosite:${i}`).join(', '),
    value.geoip?.map((i) => `geoip:${i}`).join(', '),
    value.ruleSets?.map((i) => `rs:${i}`).join(', '),
    value.ipCidrs?.join(', '),
    value.ports?.join(', '),
    value.networks?.join(', '),
    value.inboundTags?.map((i) => `in:${i}`).join(', ')
  ].filter(Boolean).join(' | ');

  return (
    <Tooltip title={fullTooltip}>
      <Space size={[4, 4]} wrap style={{ maxWidth: 360 }}>
        {tags}
      </Space>
    </Tooltip>
  );
}
