import {
  CopyOutlined,
  DeleteOutlined,
  EditOutlined,
  KeyOutlined,
  PlusOutlined,
  RedoOutlined,
  ReloadOutlined,
  SafetyCertificateOutlined
} from '@ant-design/icons';
import { useMutation, useQuery } from '@tanstack/react-query';
import {
  App as AntApp,
  Button,
  Drawer,
  Form,
  Grid,
  Input,
  InputNumber,
  Modal,
  Popconfirm,
  Result,
  Select,
  Space,
  Switch,
  Table,
  Tabs,
  Tag,
  Tooltip,
  Typography,
  type FormInstance
} from 'antd';
import type { ColumnsType } from 'antd/es/table';
import { useEffect, useMemo, useState } from 'react';
import { Page } from '../../../components/Page';
import { ApiCode, ApiError } from '../../../services/http';
import {
  certificatesApi,
  protocolsApi,
  type ResourceCertificate,
  type ResourceProtocol,
  type ResourceProtocolType,
  type ResourceProtocolWrite
} from '../../../services/protocols';import { queryClient } from '../../../services/queryClient';

type SettingValue = string | number | boolean | undefined;
type ProtocolFormValues = Omit<ResourceProtocolWrite, 'settings'> & { settings: Record<string, SettingValue> };
type CertificateFormValues = { name: string; serverName: string };
type AcmeFormValues = { name: string; domain: string };
type CloneFormValues = { name: string; listenPort: number };

const certificateSourceLabels: Record<string, string> = {
  self_signed: '自签名', manual: '手动导入', acme: 'ACME'
};

const protocolLabels: Record<ResourceProtocolType, string> = {
  vless: 'VLESS', vmess: 'VMess', hysteria2: 'Hysteria2',
  tuic: 'TUIC', anytls: 'AnyTLS', shadowsocks: 'Shadowsocks 2022',
  trojan: 'Trojan'
};

const protocolOptions = [
  { value: 'vless', label: '推荐：VLESS Reality - 通用、安全的默认选择' },
  { value: 'vmess', label: '兼容：VMess - 兼容旧客户端' },
  { value: 'hysteria2', label: '高速 UDP：Hysteria2 - 网络波动时优先' },
  { value: 'tuic', label: '高速 UDP：TUIC - 需要 TLS 证书' },
  { value: 'anytls', label: '高级：AnyTLS - 需要 TLS 证书' },
  { value: 'shadowsocks', label: '高级：Shadowsocks 2022' },
  { value: 'trojan', label: '经典：Trojan - 兼容多种客户端' }
];

const runtimeStatusLabels: Record<string, string> = {
  listening: '监听中', not_listening: '未监听', kernel_down: '内核未运行', disabled: '已停用', unknown: '未知'
};

const runtimeStatusColors: Record<string, string> = {
  listening: 'success', not_listening: 'error', kernel_down: 'warning', disabled: 'default', unknown: 'default'
};

const portHopRuleLabels: Record<string, string> = {
  missing: '端口跳跃 iptables 规则缺失，跳跃端口外部不可达；重新保存协议或手动触发同步可重建。',
  mismatched: '端口跳跃规则与当前配置不一致（改过跳跃范围后未同步）；重新保存协议可重建。',
  unknown: '无法检查端口跳跃规则（iptables 不可用）。'
};

export function ProtocolsPage() {
  const { message, notification } = AntApp.useApp();
  const screens = Grid.useBreakpoint();
  const [tab, setTab] = useState('protocols');
  const [protocolDrawer, setProtocolDrawer] = useState(false);
  const [certificateDrawer, setCertificateDrawer] = useState(false);
  const [acmeDrawer, setAcmeDrawer] = useState(false);
  const [editingProtocol, setEditingProtocol] = useState<ResourceProtocol | null>(null);
  const [editingCertificate, setEditingCertificate] = useState<ResourceCertificate | null>(null);
  const [cloneSource, setCloneSource] = useState<ResourceProtocol | null>(null);
  const [protocolType, setProtocolType] = useState<ResourceProtocolType>('vless');
  const [createDefaultsApplied, setCreateDefaultsApplied] = useState<ResourceProtocolType | null>(null);
  const [suggestedPort, setSuggestedPort] = useState<number | null>(null);
  const [protocolForm] = Form.useForm<ProtocolFormValues>();
  const [certificateForm] = Form.useForm<CertificateFormValues>();
  const [acmeForm] = Form.useForm<AcmeFormValues>();
  const [cloneForm] = Form.useForm<CloneFormValues>();
  const protocols = useQuery({ queryKey: ['resources', 'protocols'], queryFn: protocolsApi.list, retry: false });
  const runtime = useQuery({ queryKey: ['resources', 'protocols-runtime'], queryFn: protocolsApi.runtimeStatus, enabled: tab === 'protocols', retry: false, refetchInterval: 15000 });
  const certificates = useQuery({ queryKey: ['resources', 'certificates'], queryFn: certificatesApi.list, retry: false });
  const schema = useQuery({ queryKey: ['resources', 'protocol-schema', protocolType], queryFn: () => protocolsApi.schema(protocolType), enabled: protocolDrawer, retry: false });
  const suggestedPortQuery = useQuery({ queryKey: ['resources', 'protocol-port', protocolType], queryFn: () => protocolsApi.suggestPort(protocolType), enabled: protocolDrawer && !editingProtocol, retry: false });
  const settings = Form.useWatch('settings', protocolForm);
  const certificateRequired = Boolean(schema.data?.certificateRequired || Object.entries(schema.data?.certificateRequiredWhen || {}).some(([field, values]) => values.includes(String(settings?.[field] ?? ''))));
  const refreshProtocols = async () => {
    await Promise.all([
      queryClient.invalidateQueries({ queryKey: ['resources', 'protocols'] }),
      queryClient.invalidateQueries({ queryKey: ['resources', 'protocol-port'] }),
      queryClient.invalidateQueries({ queryKey: ['resources', 'protocols-runtime'] })
    ]);
  };
  const refreshCertificates = () => queryClient.invalidateQueries({ queryKey: ['resources', 'certificates'] });
  const certificateOptions = useMemo(() => [
    { source: 'acme', label: 'ACME 证书（受信任，自动续期）' },
    { source: 'self_signed', label: '自签名证书（客户端需跳过校验）' },
    { source: 'manual', label: '手动导入' }
  ].map((group) => ({
    label: group.label,
    options: (certificates.data?.items || []).filter((item) => item.source === group.source).map((item) => ({ value: item.id, label: item.name }))
  })).filter((group) => group.options.length > 0), [certificates.data]);
  useEffect(() => {
    if (!protocolDrawer || editingProtocol || !schema.data || createDefaultsApplied === protocolType) return;
    protocolForm.setFieldsValue({ settings: schema.data.defaults as Record<string, SettingValue> });
    setCreateDefaultsApplied(protocolType);
  }, [createDefaultsApplied, editingProtocol, protocolDrawer, protocolForm, protocolType, schema.data]);
  useEffect(() => {
    if (!protocolDrawer || editingProtocol || !suggestedPortQuery.data || protocolForm.getFieldValue('listenPort')) return;
    protocolForm.setFieldValue('listenPort', suggestedPortQuery.data.listenPort);
    setSuggestedPort(suggestedPortQuery.data.listenPort);
  }, [editingProtocol, protocolDrawer, protocolForm, suggestedPortQuery.data]);
  const certificateIdValue = Form.useWatch('certificateId', protocolForm);
  useEffect(() => {
    // 唯一证书时自动选中；用 useWatch 订阅值本身，字段条件挂载后仍能可靠触发。
    if (!protocolDrawer || editingProtocol || !certificateRequired || certificates.data?.items.length !== 1 || certificateIdValue) return;
    protocolForm.setFieldValue('certificateId', certificates.data.items[0].id);
  }, [certificateIdValue, certificateRequired, certificates.data?.items, editingProtocol, protocolDrawer, protocolForm]);
  const reportError = <T extends object,>(error: Error, form?: FormInstance<T>) => {
    const syncFailed = error instanceof ApiError && error.code === ApiCode.SyncFailed;
    if (syncFailed) {
      void Promise.all([
        refreshProtocols(),
        refreshCertificates(),
        queryClient.invalidateQueries({ queryKey: ['overview'] })
      ]);
      notification.error({
        message: '已保存，但尚未应用到 sing-box',
        description: `上一份成功配置仍在运行。请在总览查看原因后修正并再次保存。${error.requestId ? ` 请求 ID：${error.requestId}` : ''}`
      });
    } else {
      notification.error({ message: error.message, description: error instanceof ApiError && error.requestId ? `请求 ID：${error.requestId}` : undefined });
    }
    if (form && error instanceof ApiError) {
      const fields = Object.entries(error.fieldErrors).map(([name, value]) => ({ name: name.split('.'), errors: [value] }));
      form.setFields(fields as Parameters<FormInstance<T>['setFields']>[0]);
    }
    return syncFailed;
  };
  const closeProtocol = () => { setProtocolDrawer(false); setEditingProtocol(null); protocolForm.resetFields(); };
  const closeCertificate = () => { setCertificateDrawer(false); setEditingCertificate(null); certificateForm.resetFields(); };
  const protocolPayload = (values: ProtocolFormValues) => ({
    name: values.name, type: values.type, enabled: values.enabled, listen: values.listen || '::',
    listenPort: values.listenPort, settings: values.settings || {}, certificateId: values.certificateId || null
  });
  const createProtocol = useMutation({
    mutationFn: (values: ProtocolFormValues) => protocolsApi.create(protocolPayload(values)),
    onSuccess: () => { void refreshProtocols(); closeProtocol(); message.success('协议已创建'); },
    onError: (error: Error) => { if (reportError(error, protocolForm)) closeProtocol(); }
  });
  const updateProtocol = useMutation({
    mutationFn: (values: ProtocolFormValues) => protocolsApi.update(editingProtocol!.id, { ...protocolPayload(values), revision: editingProtocol!.revision }),
    onSuccess: () => { void refreshProtocols(); closeProtocol(); message.success('协议已更新'); },
    onError: (error: Error) => { void refreshProtocols(); if (reportError(error, protocolForm)) closeProtocol(); }
  });
  const removeProtocol = useMutation({ mutationFn: protocolsApi.remove, onSuccess: () => { void refreshProtocols(); message.success('协议已删除'); }, onError: (error: Error) => reportError(error) });
  const rotateSecret = useMutation({
    mutationFn: protocolsApi.rotateSecret,
    onSuccess: (result) => {
      void refreshProtocols();
      Modal.info({ title: '一次性密钥', content: <Typography.Text code copyable>{result.value}</Typography.Text>, okText: '我已保存' });
    },
    onError: (error: Error) => reportError(error)
  });
  const cloneProtocol = useMutation({
    mutationFn: (values: CloneFormValues) => protocolsApi.clone(cloneSource!.id, values),
    onSuccess: () => { void refreshProtocols(); setCloneSource(null); cloneForm.resetFields(); message.success('协议副本已创建并保持停用'); },
    onError: (error: Error) => reportError(error)
  });
  const createCertificate = useMutation({
    mutationFn: (values: CertificateFormValues) => certificatesApi.createSelfSigned(values),
    onSuccess: () => { void refreshCertificates(); closeCertificate(); message.success('自签名证书已生成'); },
    onError: (error: Error) => { if (reportError(error, certificateForm)) closeCertificate(); }
  });
  const createSelfSignedCertificate = useMutation({
    mutationFn: (input: { name: string; serverName: string }) => certificatesApi.createSelfSigned(input),
    onSuccess: (value) => {
      void refreshCertificates();
      protocolForm.setFieldValue('certificateId', value.id);
      message.success('自签名证书已生成并选择');
    },
    onError: (error: Error) => reportError(error, protocolForm)
  });
  const regenerateCertificate = useMutation({
    mutationFn: (values: CertificateFormValues) => certificatesApi.regenerateSelfSigned(editingCertificate!.id, { revision: editingCertificate!.revision, serverName: values.serverName }),
    onSuccess: () => { void refreshCertificates(); closeCertificate(); message.success('证书已重新生成并同步到 sing-box'); },
    onError: (error: Error) => { if (reportError(error, certificateForm)) closeCertificate(); }
  });
  const removeCertificate = useMutation({ mutationFn: certificatesApi.remove, onSuccess: () => { void refreshCertificates(); message.success('证书已删除'); }, onError: (error: Error) => reportError(error) });
  const issueAcmeCertificate = useMutation({
    mutationFn: (values: AcmeFormValues) => certificatesApi.issueAcme(values),
    onSuccess: () => { void refreshCertificates(); setAcmeDrawer(false); acmeForm.resetFields(); message.success('ACME 证书已签发'); },
    onError: (error: Error) => { if (reportError(error, acmeForm)) setAcmeDrawer(false); }
  });
  const renewAcmeCertificate = useMutation({
    mutationFn: certificatesApi.renewAcme,
    onSuccess: () => { void refreshCertificates(); message.success('ACME 证书已续期'); },
    onError: (error: Error) => reportError(error)
  });

  const applySchema = (next: ResourceProtocolType) => {
    setProtocolType(next);
    if (!editingProtocol) {
      setCreateDefaultsApplied(null);
      setSuggestedPort(null);
      protocolForm.setFieldsValue({ settings: {}, certificateId: null, listenPort: undefined });
    }
  };
  const openCreateProtocol = () => {
    setEditingProtocol(null);
    setProtocolType('vless');
    setCreateDefaultsApplied(null);
    setSuggestedPort(null);
    protocolForm.setFieldsValue({ name: '', type: 'vless', enabled: true, listen: '::', listenPort: undefined, settings: {}, certificateId: null });
    setProtocolDrawer(true);
  };
  const openEditProtocol = (value: ResourceProtocol) => {
    setEditingProtocol(value);
    setProtocolType(value.type);
    protocolForm.setFieldsValue({
      ...value,
      settings: value.settings as Record<string, SettingValue>,
      certificateId: value.certificateId || null
    });
    setProtocolDrawer(true);
  };
  const openCreateCertificate = () => { setEditingCertificate(null); certificateForm.resetFields(); setCertificateDrawer(true); };
  const openEditCertificate = (value: ResourceCertificate) => { setEditingCertificate(value); certificateForm.setFieldsValue({ name: value.name, serverName: '' }); setCertificateDrawer(true); };

  const protocolColumns: ColumnsType<ResourceProtocol> = [
    { title: '名称', dataIndex: 'name', width: 180, fixed: screens.md ? 'left' : undefined, render: (value: string) => <Typography.Text strong>{value}</Typography.Text> },
    { title: '类型', dataIndex: 'type', width: 150, render: (value: ResourceProtocolType) => <Tag color="blue">{protocolLabels[value]}</Tag> },
    { title: '监听', width: 180, render: (_, value) => <Typography.Text code>{value.listen}:{value.listenPort}</Typography.Text> },
    { title: '状态', dataIndex: 'enabled', width: 90, render: (value: boolean) => <Tag color={value ? 'success' : 'default'}>{value ? '启用' : '停用'}</Tag> },
    {
      title: '运行状态', width: 150,
      render: (_, value) => {
        const item = runtime.data?.items.find((entry) => entry.id === value.id);
        if (!item) return '-';
        const tooltip = item.portHop && item.portHop.rule !== 'ok'
          ? portHopRuleLabels[item.portHop.rule]
          : item.status === 'not_listening'
            ? '端口未在监听：检查内核是否已同步该协议，或端口是否被其他进程占用。'
            : item.status === 'unknown' ? runtime.data?.listeningReason : undefined;
        return (
          <Space size={4}>
            <Tooltip title={tooltip}><Tag color={runtimeStatusColors[item.status]}>{runtimeStatusLabels[item.status] || item.status}</Tag></Tooltip>
            {item.activeConnections != null ? <Typography.Text type="secondary">{item.activeConnections} 连接</Typography.Text> : null}
          </Space>
        );
      }
    },
    { title: '证书', dataIndex: 'certificateId', width: 160, responsive: ['lg'], render: (id?: string | null) => certificates.data?.items.find((item) => item.id === id)?.name || '-' },
    {
      title: '操作', width: 168, fixed: screens.md ? 'right' : undefined,
      render: (_, value) => <Space size={2}>
        <Tooltip title="编辑"><Button type="text" icon={<EditOutlined />} onClick={() => openEditProtocol(value)} /></Tooltip>
        <Tooltip title="复制"><Button type="text" icon={<CopyOutlined />} onClick={() => { setCloneSource(value); cloneForm.setFieldsValue({ name: `${value.name}-copy`, listenPort: value.listenPort + 1 }); }} /></Tooltip>
        <Popconfirm title="轮换后旧密钥立即失效，继续？" onConfirm={() => rotateSecret.mutate(value.id)}><Tooltip title="轮换密钥"><Button type="text" icon={<KeyOutlined />} /></Tooltip></Popconfirm>
        <Popconfirm title="确认删除该协议？" onConfirm={() => removeProtocol.mutate(value.id)}><Tooltip title="删除"><Button type="text" danger icon={<DeleteOutlined />} /></Tooltip></Popconfirm>
      </Space>
    }
  ];
  const certificateColumns: ColumnsType<ResourceCertificate> = [
    { title: '名称', dataIndex: 'name', width: 160, render: (value: string) => <Typography.Text strong>{value}</Typography.Text> },
    { title: '来源', dataIndex: 'source', width: 90, render: (value: string) => <Tag color={value === 'acme' ? 'success' : value === 'self_signed' ? 'blue' : 'default'}>{certificateSourceLabels[value] || value}</Tag> },
    { title: '域名', dataIndex: 'domain', width: 150, render: (value?: string) => value || '-' },
    { title: '指纹', dataIndex: 'fingerprint', ellipsis: true, render: (value: string) => <Typography.Text code copyable>{value}</Typography.Text> },
    { title: '到期时间', dataIndex: 'notAfter', width: 170, render: (value: string) => new Date(value).toLocaleString() },
    {
      title: '操作', width: 110,
      render: (_, value) => <Space size={2}>
        {value.source === 'acme'
          ? <Tooltip title="续期"><Button type="text" icon={<RedoOutlined />} loading={renewAcmeCertificate.isPending} onClick={() => renewAcmeCertificate.mutate(value.id)} /></Tooltip>
          : <Tooltip title="重新生成证书"><Button type="text" icon={<RedoOutlined />} onClick={() => openEditCertificate(value)} /></Tooltip>}
        <Popconfirm title="确认删除该证书？" onConfirm={() => removeCertificate.mutate(value.id)}><Tooltip title="删除"><Button type="text" danger icon={<DeleteOutlined />} /></Tooltip></Popconfirm>
      </Space>
    }
  ];
  const loadError = tab === 'protocols' ? protocols.error : certificates.error;
  if (loadError) return <Result status={loadError instanceof ApiError && loadError.status === 403 ? '403' : 'error'} title={loadError.message} extra={<Button type="primary" icon={<ReloadOutlined />} onClick={() => tab === 'protocols' ? void protocols.refetch() : void certificates.refetch()}>重试</Button>} />;

  return (
    <Page title="接入协议">
      <Tabs activeKey={tab} onChange={setTab} items={[
        { key: 'protocols', label: '协议', children: <>
          <div className="page-toolbar"><Typography.Text type="secondary">服务器对外提供的连接方式，保存后自动同步到本机 sing-box。</Typography.Text><Button type="primary" icon={<PlusOutlined />} onClick={openCreateProtocol}>添加接入协议</Button></div>
          <Table rowKey="id" loading={protocols.isLoading} dataSource={protocols.data?.items || []} columns={protocolColumns} pagination={false} scroll={{ x: screens.md ? 1080 : 430 }} />
        </> },
        { key: 'certificates', label: '证书', children: <>
          <div className="page-toolbar"><Typography.Text type="secondary">{certificates.data?.items.length || 0} 个证书</Typography.Text><Space><Button icon={<SafetyCertificateOutlined />} onClick={() => { acmeForm.resetFields(); setAcmeDrawer(true); }}>申请 ACME 证书</Button><Button type="primary" icon={<SafetyCertificateOutlined />} onClick={openCreateCertificate}>生成自签名证书</Button></Space></div>
          <Table rowKey="id" loading={certificates.isLoading} dataSource={certificates.data?.items || []} columns={certificateColumns} pagination={false} scroll={{ x: 860 }} />
        </> }
      ]} />

      <Drawer title={editingProtocol ? '编辑协议' : '创建协议'} width={520} open={protocolDrawer} destroyOnClose onClose={closeProtocol} extra={<Button type="primary" loading={createProtocol.isPending || updateProtocol.isPending} onClick={() => protocolForm.submit()}>保存</Button>}>
        <Form<ProtocolFormValues> form={protocolForm} layout="vertical" onFinish={(values) => editingProtocol ? updateProtocol.mutate(values) : createProtocol.mutate(values)}>
          <Form.Item name="name" label="名称" rules={[{ required: true, whitespace: true }]}><Input /></Form.Item>
          <Form.Item name="type" label="连接方式" rules={[{ required: true }]} extra={editingProtocol ? undefined : '推荐从 VLESS Reality 开始；其他选项用于兼容旧客户端或特定网络环境。'}><Select disabled={Boolean(editingProtocol)} options={protocolOptions} onChange={(value: ResourceProtocolType) => applySchema(value)} /></Form.Item>
          <Space align="start" style={{ width: '100%' }}>
            <Form.Item name="listen" label="监听地址"><Input style={{ width: 200 }} /></Form.Item>
            <Form.Item name="listenPort" label="监听端口" rules={[{ required: true }]} extra={!editingProtocol && suggestedPort ? `已推荐可用端口 ${suggestedPort}` : undefined}><InputNumber min={1} max={65535} style={{ width: 180 }} /></Form.Item>
          </Space>
          <Form.Item name="enabled" label="启用协议" valuePropName="checked"><Switch /></Form.Item>
          {schema.data?.fields.filter((field) => visibleProtocolField(field.name, settings, editingProtocol !== null, schema.data?.type)).map((field) => (
            <Form.Item key={field.name} name={['settings', field.name]} label={field.label} rules={field.required ? [{ required: true }] : undefined} extra={field.hint || undefined}>
              {field.kind === 'select' ? <Select options={field.options?.map((value) => ({ value, label: value }))} />
                : field.kind === 'number' ? <InputNumber style={{ width: '100%' }} />
                  : field.kind === 'boolean' ? <Switch />
                    : field.kind === 'password' ? <Input.Password placeholder={editingProtocol ? '留空则保持原值' : '留空由服务端生成'} /> : <Input />}
            </Form.Item>
          ))}
          {(certificateRequired || editingProtocol?.certificateId) ? <Form.Item name="certificateId" label="TLS 证书" rules={certificateRequired ? [{ required: true, message: '请选择或生成 TLS 证书' }] : undefined}>
            <Space.Compact style={{ width: '100%' }}>
              <Select allowClear style={{ width: '100%' }} options={certificateOptions} placeholder="暂无可用证书" />
              {!editingProtocol ? <Tooltip title="生成自签名证书"><Button icon={<SafetyCertificateOutlined />} loading={createSelfSignedCertificate.isPending} onClick={() => {
                const serverName = String(protocolForm.getFieldValue(['settings', 'serverName']) || '').trim();
                if (!serverName) {
                  protocolForm.setFields([{ name: ['settings', 'serverName'], errors: ['请先填写服务器名称'] }]);
                  return;
                }
                createSelfSignedCertificate.mutate({ name: `${serverName}-self-signed`, serverName });
              }} /></Tooltip> : null}
            </Space.Compact>
            {certificateRequired && certificateOptions.length === 0 ? <div className="ant-form-item-extra" style={{ marginTop: 4 }}>还没有可用证书，可点击右侧按钮生成自签名证书，或在「证书」页签申请 ACME 受信任证书。</div> : null}
          </Form.Item> : null}
        </Form>
      </Drawer>

      <Drawer title={editingCertificate ? '重新生成证书' : '生成自签名证书'} width={520} open={certificateDrawer} destroyOnClose onClose={closeCertificate} extra={<Button type="primary" loading={createCertificate.isPending || regenerateCertificate.isPending} onClick={() => certificateForm.submit()}>{editingCertificate ? '重新生成' : '生成'}</Button>}>
        <Form<CertificateFormValues> form={certificateForm} layout="vertical" onFinish={(values) => editingCertificate ? regenerateCertificate.mutate(values) : createCertificate.mutate(values)}>
          {!editingCertificate ? <Form.Item name="name" label="证书名称" rules={[{ required: true, whitespace: true }]}><Input /></Form.Item> : <Form.Item label="证书名称"><Input value={editingCertificate.name} disabled /></Form.Item>}
          <Form.Item name="serverName" label="服务器名称" extra="支持公网 IP 或完整域名；生成或重新生成后需发布才会由 sing-box 使用。" rules={[{ required: true, whitespace: true }]}><Input placeholder="例如：203.0.113.10 或 panel.example.com" /></Form.Item>
        </Form>
      </Drawer>

      <Drawer title="申请 ACME 证书" width={440} open={acmeDrawer} destroyOnClose onClose={() => { setAcmeDrawer(false); acmeForm.resetFields(); }} extra={<Button type="primary" loading={issueAcmeCertificate.isPending} onClick={() => acmeForm.submit()}>签发</Button>}>
        <Form<AcmeFormValues> form={acmeForm} layout="vertical" onFinish={(values) => issueAcmeCertificate.mutate(values)}>
          <Form.Item name="name" label="证书名称" rules={[{ required: true, whitespace: true }]}><Input placeholder="例如 panel.example.com" /></Form.Item>
          <Form.Item name="domain" label="域名" extra="需在 config.yaml 的 acme 段配置 email 与 api_token（Cloudflare DNS）。签发通过 DNS-01 完成，不占用端口。" rules={[{ required: true, whitespace: true }]}><Input placeholder="panel.example.com" /></Form.Item>
        </Form>
      </Drawer>

      <Modal title="复制协议" open={Boolean(cloneSource)} confirmLoading={cloneProtocol.isPending} onOk={() => cloneForm.submit()} onCancel={() => setCloneSource(null)} destroyOnClose>
        <Form<CloneFormValues> form={cloneForm} layout="vertical" onFinish={(values) => cloneProtocol.mutate(values)}>
          <Form.Item name="name" label="副本名称" rules={[{ required: true }]}><Input /></Form.Item>
          <Form.Item name="listenPort" label="监听端口" rules={[{ required: true }]}><InputNumber min={1} max={65535} style={{ width: '100%' }} /></Form.Item>
        </Form>
      </Modal>
    </Page>
  );
}

function visibleProtocolField(name: string, settings: Record<string, SettingValue> | undefined, editing: boolean, type?: string) {
  const transport = settings?.transport;
  const tlsMode = settings?.tlsMode;
  switch (name) {
    case 'wsPath': return transport === 'ws';
    case 'grpcServiceName': return transport === 'grpc';
    case 'httpupgradePath': return transport === 'httpupgrade';
    case 'httpPath': return transport === 'http';
    case 'handshakeServerPort': return tlsMode === 'reality';
    case 'realityPrivateKey':
    case 'realityPublicKey':
    case 'shortId':
      return editing && tlsMode === 'reality';
    case 'serverName':
      if (type === 'vmess') return tlsMode === 'certificate';
      return true;
    default:
      return true;
  }
}
