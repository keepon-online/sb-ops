import {
  ArrowDownOutlined,
  ArrowUpOutlined,
  CopyOutlined,
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
  Grid,
  Input,
  InputNumber,
  Popconfirm,
  Result,
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
import { useNavigate } from 'react-router-dom';
import { Page } from '../../../components/Page';
import { ApiError } from '../../../services/http';
import {
  policiesApi,
  type ResourcePolicy,
  type ResourcePolicyConditions,
  type ResourcePolicyDryRunResult,
  type ResourcePolicyRule,
  type ResourcePolicyValidationResult
} from '../../../services/policies';
import { queryClient } from '../../../services/queryClient';

type PolicyFormValues = { name: string; description: string; enabled: boolean; defaultAction: string };
type RuleFormValues = {
  name: string; enabled: boolean; priority: number; action: string; noResolve: boolean;
  domains: string[]; domainSuffixes: string[]; ipCidrs: string[]; ruleSets: string[]; ports: string[]; networks: Array<'tcp' | 'udp'>; protocols: string[];
};
type DryRunValues = { host: string; port: number; network: string; protocol: string };

const actionOptions = [{ value: 'direct', label: '直连' }, { value: 'block', label: '拦截' }, { value: 'proxy', label: '代理节点组' }];

function actionLabel(action: string) {
  return actionOptions.find((item) => item.value === action)?.label || action;
}

export function PoliciesPage() {
  const navigate = useNavigate();
  const { message, notification } = AntApp.useApp();
  const screens = Grid.useBreakpoint();
  const [selectedID, setSelectedID] = useState<string>();
  const [policyDrawer, setPolicyDrawer] = useState(false);
  const [ruleDrawer, setRuleDrawer] = useState(false);
  const [editingPolicy, setEditingPolicy] = useState<ResourcePolicy | null>(null);
  const [editingRule, setEditingRule] = useState<ResourcePolicyRule | null>(null);
  const [dryRunResult, setDryRunResult] = useState<ResourcePolicyDryRunResult | null>(null);
  const [validation, setValidation] = useState<ResourcePolicyValidationResult | null>(null);
  const [policyForm] = Form.useForm<PolicyFormValues>();
  const [ruleForm] = Form.useForm<RuleFormValues>();
  const [dryRunForm] = Form.useForm<DryRunValues>();
  const policies = useQuery({ queryKey: ['resources', 'policies'], queryFn: policiesApi.list, retry: false });
  const currentPolicy = policies.data?.items.find((item) => item.id === selectedID);
  const rules = useQuery({ queryKey: ['resources', 'policies', selectedID, 'rules'], queryFn: () => policiesApi.listRules(selectedID!), enabled: Boolean(selectedID), retry: false });
  const ruleSetCatalog = useQuery({ queryKey: ['resources', 'policies', 'rule-sets'], queryFn: policiesApi.listRuleSets, retry: false });
  const ruleSetGroupedOptions = useMemo(() => {
    const groups = [
      { behavior: 'domain', label: '域名规则' },
      { behavior: 'ipcidr', label: 'IP 网段' },
      { behavior: 'classical', label: '传统规则（客户端判断）' }
    ];
    return groups.map((group) => ({
      label: group.label,
      options: (ruleSetCatalog.data?.items || []).filter((item) => item.behavior === group.behavior).map((item) => ({ value: item.name, label: item.name }))
    })).filter((group) => group.options.length > 0);
  }, [ruleSetCatalog.data]);
  useEffect(() => {
    if (!selectedID && policies.data?.items.length) setSelectedID(policies.data.items[0].id);
    if (selectedID && policies.data && !policies.data.items.some((item) => item.id === selectedID)) setSelectedID(policies.data.items[0]?.id);
  }, [policies.data, selectedID]);
  const reportError = (error: Error) => notification.error({ message: error.message, description: error instanceof ApiError && error.requestId ? `请求 ID：${error.requestId}` : undefined });
  const refreshPolicies = () => queryClient.invalidateQueries({ queryKey: ['resources', 'policies'] });
  const refreshRules = () => queryClient.invalidateQueries({ queryKey: ['resources', 'policies', selectedID, 'rules'] });
  const closePolicy = () => { setPolicyDrawer(false); setEditingPolicy(null); policyForm.resetFields(); };
  const closeRule = () => { setRuleDrawer(false); setEditingRule(null); ruleForm.resetFields(); };
  const createPolicy = useMutation({ mutationFn: policiesApi.create, onSuccess: (value) => { void refreshPolicies(); setSelectedID(value.id); closePolicy(); message.success('策略已创建'); }, onError: reportError });
  const updatePolicy = useMutation({ mutationFn: (values: PolicyFormValues) => policiesApi.update(editingPolicy!.id, { ...values, revision: editingPolicy!.revision }), onSuccess: () => { void refreshPolicies(); closePolicy(); message.success('策略已更新'); }, onError: reportError });
  const removePolicy = useMutation({ mutationFn: policiesApi.remove, onSuccess: () => { void refreshPolicies(); message.success('策略已删除'); }, onError: reportError });
  const clonePolicy = useMutation({ mutationFn: (value: ResourcePolicy) => policiesApi.clone(value.id, `${value.name}-copy`), onSuccess: (value) => { void refreshPolicies(); setSelectedID(value.id); message.success('策略副本已创建并保持停用'); }, onError: reportError });
  const conditionsFromForm = (values: RuleFormValues): ResourcePolicyConditions => ({
    domains: values.domains || [], domainSuffixes: values.domainSuffixes || [], ipCidrs: values.ipCidrs || [],
    ruleSets: values.ruleSets || [], ports: (values.ports || []).map(Number), networks: values.networks || [], protocols: values.protocols || []
  });
  const rulePayload = (values: RuleFormValues) => ({ name: values.name, enabled: values.enabled, priority: values.priority, conditions: conditionsFromForm(values), action: values.action, noResolve: values.noResolve });
  const createRule = useMutation({ mutationFn: (values: RuleFormValues) => policiesApi.createRule(selectedID!, rulePayload(values)), onSuccess: () => { void refreshRules(); closeRule(); message.success('规则已创建'); }, onError: reportError });
  const updateRule = useMutation({ mutationFn: (values: RuleFormValues) => policiesApi.updateRule(selectedID!, editingRule!.id, { ...rulePayload(values), revision: editingRule!.revision }), onSuccess: () => { void refreshRules(); closeRule(); message.success('规则已更新'); }, onError: reportError });
  const removeRule = useMutation({ mutationFn: (ruleID: string) => policiesApi.removeRule(selectedID!, ruleID), onSuccess: () => { void refreshRules(); message.success('规则已删除'); }, onError: reportError });
  const orderRules = useMutation({ mutationFn: (ids: string[]) => policiesApi.orderRules(selectedID!, ids), onSuccess: () => void refreshRules(), onError: reportError });
  const dryRun = useMutation({ mutationFn: (values: DryRunValues) => policiesApi.dryRun(selectedID!, values), onSuccess: setDryRunResult, onError: reportError });
  const validate = useMutation({ mutationFn: () => policiesApi.validate(selectedID!), onSuccess: setValidation, onError: reportError });
  const openCreatePolicy = () => { setEditingPolicy(null); policyForm.setFieldsValue({ name: '', description: '', enabled: true, defaultAction: 'direct' }); setPolicyDrawer(true); };
  const openEditPolicy = (value: ResourcePolicy) => { setEditingPolicy(value); policyForm.setFieldsValue(value); setPolicyDrawer(true); };
  const openCreateRule = () => { setEditingRule(null); ruleForm.setFieldsValue({ name: '', enabled: true, priority: ((rules.data?.items[0]?.priority || 0) + 10), action: 'proxy', noResolve: false, domains: [], domainSuffixes: [], ipCidrs: [], ruleSets: [], ports: [], networks: [], protocols: [] }); setRuleDrawer(true); };
  const openEditRule = (value: ResourcePolicyRule) => { setEditingRule(value); ruleForm.setFieldsValue({ ...value, ...value.conditions, ruleSets: value.conditions.ruleSets || [], ports: value.conditions.ports?.map(String) || [] }); setRuleDrawer(true); };
  const moveRule = (index: number, direction: -1 | 1) => {
    const items = [...(rules.data?.items || [])]; const next = index + direction; if (next < 0 || next >= items.length) return;
    [items[index], items[next]] = [items[next], items[index]]; orderRules.mutate(items.map((item) => item.id));
  };
  const policyColumns: ColumnsType<ResourcePolicy> = [
    { title: '策略', dataIndex: 'name', width: 180, render: (value: string, record) => <Button type="link" style={{ padding: 0, fontWeight: record.id === selectedID ? 600 : 400 }} onClick={() => { setSelectedID(record.id); setDryRunResult(null); setValidation(null); }}>{value}</Button> },
    { title: '状态', dataIndex: 'enabled', width: 80, render: (value: boolean) => <Tag color={value ? 'success' : 'default'}>{value ? '启用' : '停用'}</Tag> },
    { title: '默认动作', dataIndex: 'defaultAction', width: 110, render: actionLabel },
    { title: '操作', width: 120, render: (_, value) => <Space size={2}><Tooltip title="编辑"><Button type="text" icon={<EditOutlined />} onClick={() => openEditPolicy(value)} /></Tooltip><Tooltip title="复制"><Button type="text" icon={<CopyOutlined />} onClick={() => clonePolicy.mutate(value)} /></Tooltip><Popconfirm title="确认删除该策略？" onConfirm={() => removePolicy.mutate(value.id)}><Tooltip title="删除"><Button type="text" danger icon={<DeleteOutlined />} /></Tooltip></Popconfirm></Space> }
  ];
  const ruleColumns: ColumnsType<ResourcePolicyRule> = [
    { title: '顺序', width: 84, render: (_, __, index) => <Space size={0}><Button type="text" size="small" icon={<ArrowUpOutlined />} disabled={index === 0} onClick={() => moveRule(index, -1)} /><Button type="text" size="small" icon={<ArrowDownOutlined />} disabled={index === (rules.data?.items.length || 0) - 1} onClick={() => moveRule(index, 1)} /></Space> },
    { title: '规则', dataIndex: 'name', width: 170, render: (value: string) => <Typography.Text strong>{value}</Typography.Text> },
    { title: '条件', render: (_, value) => conditionSummary(value.conditions) },
    { title: '动作', dataIndex: 'action', width: 110, render: (value: string) => <Tag color={value === 'block' ? 'error' : value === 'direct' ? 'success' : 'blue'}>{actionLabel(value)}</Tag> },
    { title: '状态', dataIndex: 'enabled', width: 72, render: (value: boolean) => value ? '启用' : '停用' },
    { title: '操作', width: 104, render: (_, value) => <Space size={2}><Tooltip title="编辑"><Button type="text" icon={<EditOutlined />} onClick={() => openEditRule(value)} /></Tooltip><Popconfirm title="确认删除该规则？" onConfirm={() => removeRule.mutate(value.id)}><Tooltip title="删除"><Button type="text" danger icon={<DeleteOutlined />} /></Tooltip></Popconfirm></Space> }
  ];
  if (policies.isError) return <Result status={policies.error instanceof ApiError && policies.error.status === 403 ? '403' : 'error'} title={policies.error.message} extra={<Button type="primary" icon={<ReloadOutlined />} onClick={() => void policies.refetch()}>重试</Button>} />;

  return <Page title="订阅分流">
    <div className="page-toolbar"><Typography.Text type="secondary">订阅分流：写入用户下载的 Clash 订阅，从上到下匹配、命中第一条后停止；只影响单个用户的客户端，不改变服务器转发。</Typography.Text><Space><Button type="primary" onClick={() => navigate('/singbox?tab=routes')}>前往服务器分流</Button><Button type="primary" icon={<PlusOutlined />} onClick={openCreatePolicy}>创建策略</Button></Space></div>
    <Table rowKey="id" size="small" loading={policies.isLoading} dataSource={policies.data?.items || []} columns={policyColumns} pagination={false} scroll={{ x: 590 }} />
    {currentPolicy ? <Tabs style={{ marginTop: 18 }} items={[
      { key: 'rules', label: `规则 (${rules.data?.items.length || 0})`, children: <><div className="page-toolbar"><Typography.Title level={5} style={{ margin: 0 }}>{currentPolicy.name}</Typography.Title><Button type="primary" icon={<PlusOutlined />} onClick={openCreateRule}>添加规则</Button></div><Table rowKey="id" loading={rules.isLoading || orderRules.isPending} dataSource={rules.data?.items || []} columns={ruleColumns} pagination={false} scroll={{ x: screens.md ? 860 : 520 }} /></> },
      { key: 'dry-run', label: '测试规则', children: <div className="policy-dry-run"><Typography.Paragraph type="secondary">面板会下载 Clash 规则集来判断域名/IP；少数 classical 规则集（claude/china/spotify/steam）仍由客户端判断，下载失败时该条也改由客户端判断。</Typography.Paragraph><Form<DryRunValues> form={dryRunForm} layout="inline" initialValues={{ host: 'example.com', port: 443, network: 'tcp', protocol: 'tls' }} onFinish={(values) => dryRun.mutate(values)}><Form.Item name="host" rules={[{ required: true }]}><Input placeholder="域名或 IP" /></Form.Item><Form.Item name="port"><InputNumber min={0} max={65535} placeholder="端口" /></Form.Item><Form.Item name="network"><Select style={{ width: 110 }} options={[{ value: 'tcp', label: 'TCP' }, { value: 'udp', label: 'UDP' }]} /></Form.Item><Form.Item name="protocol"><Input placeholder="应用协议，例如 tls" /></Form.Item><Button type="primary" htmlType="submit" loading={dryRun.isPending}>测试</Button></Form>{dryRunResult ? <><Alert style={{ marginTop: 16 }} type={dryRunResult.determined ? 'info' : 'warning'} message={dryRunResult.determined ? `确定动作：${actionLabel(dryRunResult.action)}` : '无法在此处确定最终动作'} description={dryRunResult.determined ? undefined : '规则集成员关系由 Clash 客户端决定，可能在这一条规则停止匹配。'} /><Table style={{ marginTop: 12 }} rowKey="ruleId" size="small" pagination={false} dataSource={dryRunResult.trace} columns={[{ title: '规则', dataIndex: 'name' }, { title: '状态', dataIndex: 'status', width: 100, render: (value: string) => ({ match: '匹配', miss: '未匹配', skipped: '已停用', client: '客户端判断' }[value] || value) }, { title: '原因', dataIndex: 'reason', render: (value: string) => ({ 'rule disabled': '规则已停用', 'rule set membership is evaluated by the Clash client': '规则集成员关系由 Clash 客户端判断', 'rule set matched': '规则集命中', 'rule set did not match': '规则集未匹配', 'rule set format is not locally matchable': '规则集格式无法本地判断，由客户端判断', 'rule set download failed': '规则集下载失败，改由客户端判断', 'domain did not match': '域名不匹配', 'domain suffix did not match': '域名后缀不匹配', 'host is not an IP address': '输入不是 IP 地址', 'IP CIDR did not match': 'IP 网段不匹配', 'port did not match': '端口不匹配', 'network did not match': '网络类型不匹配', 'protocol did not match': '应用协议不匹配', 'all conditions matched': '所有条件均匹配' }[value] || value) }, { title: '动作', dataIndex: 'action', width: 110, render: actionLabel }]} /></> : <Empty image={Empty.PRESENTED_IMAGE_SIMPLE} description="输入条件后测试规则" />}</div> },
      { key: 'validation', label: '验证', children: <><Button loading={validate.isPending} onClick={() => validate.mutate()}>验证策略</Button>{validation ? <Space direction="vertical" style={{ width: '100%', marginTop: 12 }}>{validation.issues.length === 0 ? <Alert type="success" message="未发现冲突或不可达规则" /> : validation.issues.map((issue, index) => <Alert key={`${issue.ruleId}-${index}`} type={issue.level === 'error' ? 'error' : 'warning'} message={issue.message} />)}</Space> : null}</> }
    ]} /> : <Empty description="创建策略后开始配置规则" />}

    <Drawer title={editingPolicy ? '编辑策略' : '创建策略'} width={440} open={policyDrawer} destroyOnClose onClose={closePolicy} extra={<Button type="primary" loading={createPolicy.isPending || updatePolicy.isPending} onClick={() => policyForm.submit()}>保存</Button>}><Form<PolicyFormValues> form={policyForm} layout="vertical" onFinish={(values) => editingPolicy ? updatePolicy.mutate(values) : createPolicy.mutate(values)}><Form.Item name="name" label="名称" rules={[{ required: true, whitespace: true }]}><Input /></Form.Item><Form.Item name="description" label="描述"><Input.TextArea rows={3} /></Form.Item><Form.Item name="defaultAction" label="默认动作" rules={[{ required: true }]}><Select options={actionOptions} /></Form.Item><Form.Item name="enabled" label="启用策略" valuePropName="checked"><Switch /></Form.Item></Form></Drawer>
    <Drawer title={editingRule ? '编辑规则' : '添加规则'} width={520} open={ruleDrawer} destroyOnClose onClose={closeRule} extra={<Button type="primary" loading={createRule.isPending || updateRule.isPending} onClick={() => ruleForm.submit()}>保存</Button>}><Form<RuleFormValues> form={ruleForm} layout="vertical" onFinish={(values) => editingRule ? updateRule.mutate(values) : createRule.mutate(values)}><Form.Item name="name" label="名称" rules={[{ required: true }]}><Input /></Form.Item><Space align="start"><Form.Item name="priority" label="优先级" rules={[{ required: true }]}><InputNumber min={0} /></Form.Item><Form.Item name="action" label="最终动作" rules={[{ required: true }]}><Select style={{ width: 150 }} options={actionOptions} /></Form.Item></Space><Form.Item name="ruleSets" label="常用服务规则集"><Select mode="multiple" options={ruleSetGroupedOptions} placeholder="选择常用服务，例如 openai、github" /></Form.Item><Form.Item name="domains" label="精确域名"><Select mode="tags" tokenSeparators={[',']} /></Form.Item><Form.Item name="domainSuffixes" label="域名后缀"><Select mode="tags" tokenSeparators={[',']} /></Form.Item><Form.Item name="ipCidrs" label="IP 网段（CIDR）"><Select mode="tags" tokenSeparators={[',']} /></Form.Item><Form.Item name="ports" label="端口"><Select mode="tags" tokenSeparators={[',']} /></Form.Item><Form.Item name="networks" label="网络"><Select mode="multiple" options={[{ value: 'tcp', label: 'TCP' }, { value: 'udp', label: 'UDP' }]} /></Form.Item><Form.Item name="protocols" label="应用协议"><Select mode="tags" tokenSeparators={[',']} /></Form.Item><Space><Form.Item name="enabled" label="启用" valuePropName="checked"><Switch /></Form.Item><Form.Item name="noResolve" label="不解析域名" valuePropName="checked"><Switch /></Form.Item></Space></Form></Drawer>
  </Page>;
}

function conditionSummary(value: ResourcePolicyConditions) {
  const parts = [value.ruleSets?.map((item) => `RULE-SET:${item}`).join(', '), value.domains?.join(', '), value.domainSuffixes?.map((item) => `*.${item}`).join(', '), value.ipCidrs?.join(', '), value.ports?.join(', '), value.networks?.join(', '), value.protocols?.join(', ')].filter(Boolean);
  return parts.length ? <Typography.Text ellipsis={{ tooltip: parts.join(' | ') }} style={{ maxWidth: 360 }}>{parts.join(' | ')}</Typography.Text> : <Tag>全部流量</Tag>;
}
