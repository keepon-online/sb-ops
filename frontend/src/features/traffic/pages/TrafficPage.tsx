import { ArrowDownOutlined, ArrowUpOutlined, CloudDownloadOutlined, FieldTimeOutlined, LineChartOutlined, ReloadOutlined, UserOutlined } from '@ant-design/icons';
import { useQuery } from '@tanstack/react-query';
import { Button, DatePicker, Empty, Progress, Segmented, Select, Space, Table, Tooltip, Typography } from 'antd';
import dayjs, { type Dayjs } from 'dayjs';
import { useMemo, useState } from 'react';
import { useSearchParams } from 'react-router-dom';
import { InteractiveChart } from '../../../components/InteractiveChart';
import { Page } from '../../../components/Page';
import { protocolsApi } from '../../../services/protocols';
import { trafficApi, type TrafficProtocolShare } from '../../../services/traffic';
import { usersApi } from '../../../services/users';

const bytes = (value: number) => {
  const units = ['B', 'KB', 'MB', 'GB', 'TB'];
  let current = value;
  let index = 0;
  while (current >= 1024 && index < units.length - 1) { current /= 1024; index += 1; }
  return `${current.toFixed(index === 0 ? 0 : 2)} ${units[index]}`;
};

function pointBucketKey(bucket: string, mode: BucketMode): string {
  const day = dayjs(bucket);
  switch (mode) {
    case 'hour': return `${day.format('YYYY-MM-DD')}-${day.format('HH')}`;
    case 'day': return day.format('YYYY-MM-DD');
    case 'month': return day.format('YYYY-MM');
    default: return day.format('YYYY');
  }
}

const shareColors = ['#4f8ef7', '#42a56f', '#e8a33d', '#d16ba5', '#9b7ede', '#5bc0de', '#e07b54', '#8bd17d'];

function ProtocolDonut({ shares }: { shares: TrafficProtocolShare[] }) {
  const total = shares.reduce((sum, item) => sum + item.totalBytes, 0);
  if (total <= 0) return <Empty image={Empty.PRESENTED_IMAGE_SIMPLE} description="暂无协议流量" />;
  const radius = 60;
  const circumference = 2 * Math.PI * radius;
  let accumulated = 0;
  return <div style={{ display: 'flex', alignItems: 'center', gap: 32, flexWrap: 'wrap', padding: '8px 0' }}>
    <svg viewBox="0 0 160 160" width={160} height={160} role="img" aria-label="协议流量占比环形图">
      <g transform="rotate(-90 80 80)">
        {shares.map((share, index) => {
          const fraction = share.totalBytes / total;
          const dash = `${fraction * circumference} ${circumference}`;
          const offset = -accumulated * circumference;
          accumulated += fraction;
          return <circle key={share.id} cx="80" cy="80" r={radius} fill="none" stroke={shareColors[index % shareColors.length]} strokeWidth="24" strokeDasharray={dash} strokeDashoffset={offset} />;
        })}
      </g>
      <text x="80" y="76" textAnchor="middle" fill="var(--text-color)" fontSize="13">合计</text>
      <text x="80" y="94" textAnchor="middle" fill="var(--text-color)" fontSize="14" fontWeight="600">{bytes(total)}</text>
    </svg>
    <Space direction="vertical" size={6}>
      {shares.map((share, index) => (
        <Space key={share.id} size={8}>
          <span style={{ width: 10, height: 10, borderRadius: 2, background: shareColors[index % shareColors.length], display: 'inline-block' }} />
          <Typography.Text>{share.name}</Typography.Text>
          <Typography.Text type="secondary">{bytes(share.totalBytes)} · {((share.totalBytes / total) * 100).toFixed(1)}%</Typography.Text>
        </Space>
      ))}
    </Space>
  </div>;
}

// BucketMode is derived from the selected date range: one day → hourly slots,
// short ranges → daily, longer → monthly, multi-year → yearly. The backend only
// stores hour/day granularity; month/year are aggregated on the client.
type BucketMode = 'hour' | 'day' | 'month' | 'year';

function bucketModeFor(fromDay: string, toDay: string): BucketMode {
  const days = dayjs(toDay).diff(dayjs(fromDay), 'day') + 1;
  if (days <= 1) return 'hour';
  if (days <= 92) return 'day';
  if (days <= 1461) return 'month';
  return 'year';
}

function bucketSlots<T>(points: T[], bucketOf: (point: T) => string, fromDay: string, toDay: string, mode: BucketMode): Array<{ label: string; key: string; items: T[] }> {
  const byKey = new Map<string, T[]>();
  for (const point of points) {
    const key = bucketOf(point);
    const list = byKey.get(key);
    if (list) list.push(point); else byKey.set(key, [point]);
  }
  const slots: Array<{ label: string; key: string; items: T[] }> = [];
  if (mode === 'hour') {
    for (let hour = 0; hour < 24; hour += 1) {
      const key = `${fromDay}-${String(hour).padStart(2, '0')}`;
      slots.push({ label: `${String(hour).padStart(2, '0')}:00`, key, items: byKey.get(key) || [] });
    }
    return slots;
  }
  if (mode === 'day') {
    for (let day = dayjs(fromDay); day.isBefore(toDay) || day.isSame(toDay, 'day'); day = day.add(1, 'day')) {
      const key = day.format('YYYY-MM-DD');
      slots.push({ label: day.format('M/D'), key, items: byKey.get(key) || [] });
    }
    return slots;
  }
  const unit = mode === 'month' ? 'month' : 'year';
  for (let cursor = dayjs(fromDay).startOf(unit); cursor.isBefore(toDay) || cursor.isSame(toDay, unit); cursor = cursor.add(1, unit)) {
    const key = cursor.format(mode === 'month' ? 'YYYY-MM' : 'YYYY');
    slots.push({ label: mode === 'month' ? cursor.format('YYYY/M') : key, key, items: byKey.get(key) || [] });
  }
  return slots;
}

export function TrafficPage() {
  const [searchParams, setSearchParams] = useSearchParams();
  const [dimension, setDimension] = useState<'user' | 'protocol'>(searchParams.get('dimension') === 'protocol' ? 'protocol' : 'user');
  // 时间范围统一用日期选择(含快捷预设);兼容旧链接的 range 小时数参数。
  const defaultFromDay = dayjs().subtract(29, 'day').format('YYYY-MM-DD');
  const defaultToDay = dayjs().format('YYYY-MM-DD');
  const legacyHours = Number(searchParams.get('range')) || 0;
  const [fromDay, setFromDay] = useState(searchParams.get('from') || (legacyHours > 0 ? dayjs().subtract(legacyHours - 1, 'hour').format('YYYY-MM-DD') : defaultFromDay));
  const [toDay, setToDay] = useState(searchParams.get('to') || defaultToDay);
  // 用户统计不含协议归属、入站统计不含用户归属,维度与另一侧过滤器互斥:初始化时直接丢弃矛盾参数。
  const [userId, setUserId] = useState(dimension === 'protocol' ? '' : searchParams.get('userId') || '');
  const [protocolId, setProtocolId] = useState(dimension === 'user' ? '' : searchParams.get('protocolId') || '');
  const updateFilters = (next: { dimension?: 'user' | 'protocol'; userId?: string; protocolId?: string; fromDay?: string; toDay?: string }) => {
    const merged = { dimension, userId, protocolId, fromDay, toDay, ...next };
    if (merged.dimension === 'user') merged.protocolId = '';
    if (merged.dimension === 'protocol') merged.userId = '';
    if (!merged.fromDay || !merged.toDay) {
      merged.fromDay = defaultFromDay;
      merged.toDay = defaultToDay;
    }
    setDimension(merged.dimension);
    setUserId(merged.userId);
    setProtocolId(merged.protocolId);
    setFromDay(merged.fromDay);
    setToDay(merged.toDay);
    const query = new URLSearchParams({ dimension: merged.dimension });
    if (merged.fromDay) query.set('from', merged.fromDay);
    if (merged.toDay) query.set('to', merged.toDay);
    if (merged.userId) query.set('userId', merged.userId);
    if (merged.protocolId) query.set('protocolId', merged.protocolId);
    setSearchParams(query, { replace: true });
  };
  // 展示粒度由日期范围自动推导;后端只需 hour/day,月/年由前端聚合。
  const mode = useMemo(() => bucketModeFor(fromDay, toDay), [fromDay, toDay]);
  const queryInterval: 'hour' | 'day' = mode === 'hour' ? 'hour' : 'day';
  const filter = useMemo(() => {
    // toDay 含当天(统计到 23:59:59)。
    const to = new Date(`${toDay}T23:59:59.999`);
    return { from: new Date(`${fromDay}T00:00:00`).toISOString(), to: to.toISOString(), interval: queryInterval, userId, protocolId };
  }, [queryInterval, userId, protocolId, fromDay, toDay]);
  // 排行与不适用维度的过滤组合恒为空(数据模型互斥),请求前按维度剥离冲突过滤。
  const rankingFilter = useMemo(() => ({ ...filter, protocolId: dimension === 'user' ? '' : filter.protocolId, userId: dimension === 'protocol' ? '' : filter.userId }), [filter, dimension]);
  const users = useQuery({ queryKey: ['resources', 'users', 'traffic-filter'], queryFn: () => usersApi.list({ limit: 100, sort: 'name:asc' }), retry: false });
  const protocols = useQuery({ queryKey: ['resources', 'protocols'], queryFn: protocolsApi.list, retry: false });
  const series = useQuery({ queryKey: ['traffic', 'series', filter], queryFn: () => trafficApi.series(filter) });
  const summary = useQuery({ queryKey: ['traffic', 'summary', filter], queryFn: () => trafficApi.summary(filter) });
  const rankings = useQuery({ queryKey: ['traffic', 'rankings', rankingFilter, dimension], queryFn: () => trafficApi.rankings({ ...rankingFilter, dimension, limit: 10 }) });
  const activeUsers = useQuery({ queryKey: ['traffic', 'active-users', filter], queryFn: () => trafficApi.activeUsers(filter) });
  const protocolShares = useQuery({ queryKey: ['traffic', 'protocol-shares', filter], queryFn: () => trafficApi.protocolShares(filter) });
  const refresh = () => { void series.refetch(); void summary.refetch(); void rankings.refetch(); void activeUsers.refetch(); void protocolShares.refetch(); };
  // 归一化槽位:小时/天补齐缺失时段,月/年由日数据聚合;图表与表格共用同一份。
  const trendSlots = useMemo(() => {
    const points = series.data?.items || [];
    if (points.length === 0) return [];
    return bucketSlots(points, (point) => pointBucketKey(point.bucket, mode), fromDay, toDay, mode).map((slot) => ({
      label: slot.label,
      up: slot.items.reduce((sum, item) => sum + item.upBytes, 0),
      down: slot.items.reduce((sum, item) => sum + item.downBytes, 0),
      total: slot.items.reduce((sum, item) => sum + item.upBytes + item.downBytes, 0)
    }));
  }, [series.data, fromDay, toDay, mode]);
  const activeSlots = useMemo(() => {
    const points = activeUsers.data?.items || [];
    if (points.length === 0) return [];
    return bucketSlots(points, (point) => pointBucketKey(point.bucket, mode), fromDay, toDay, mode).map((slot) => ({
      label: slot.label,
      users: slot.items.reduce((peak, item) => Math.max(peak, item.users), 0)
    }));
  }, [activeUsers.data, fromDay, toDay, mode]);
  return <Page title="流量分析">
    <div className="page-toolbar traffic-filters">
      <Space wrap>
        <DatePicker.RangePicker
          value={[fromDay ? dayjs(fromDay) : null, toDay ? dayjs(toDay) : null] as [Dayjs | null, Dayjs | null]}
          onChange={(_, dateStrings) => { if (dateStrings[0] && dateStrings[1]) updateFilters({ fromDay: dateStrings[0], toDay: dateStrings[1] }); }}
          disabledDate={(current) => current.isAfter(dayjs().endOf('day'))}
          allowClear={false}
          presets={[
            { label: '今天', value: [dayjs(), dayjs()] },
            { label: '近 7 天', value: [dayjs().subtract(6, 'day'), dayjs()] },
            { label: '近 30 天', value: [dayjs().subtract(29, 'day'), dayjs()] }
          ]}
        />
        <Segmented value={dimension} onChange={(value) => updateFilters({ dimension: value as 'user' | 'protocol' })} options={[{ label: '用户排行', value: 'user' }, { label: '协议排行', value: 'protocol' }]} />
        <Tooltip title={dimension === 'protocol' ? '与协议排行维度互斥：入站统计不含用户归属' : undefined}>
          <Select allowClear showSearch optionFilterProp="label" value={userId || undefined} onChange={(value) => updateFilters({ userId: value || '' })} placeholder="全部用户" style={{ width: 190 }} disabled={dimension === 'protocol'} loading={users.isLoading} options={users.data?.items.map((item) => ({ value: item.id, label: item.name }))} />
        </Tooltip>
        <Tooltip title={dimension === 'user' ? '与用户排行维度互斥：用户统计不含协议归属' : undefined}>
          <Select allowClear showSearch optionFilterProp="label" value={protocolId || undefined} onChange={(value) => updateFilters({ protocolId: value || '' })} placeholder="全部接入协议" style={{ width: 190 }} disabled={dimension === 'user'} loading={protocols.isLoading} options={protocols.data?.items.map((item) => ({ value: item.id, label: `${item.name} (${item.type})` }))} />
        </Tooltip>
      </Space>
      <Button icon={<ReloadOutlined />} loading={series.isFetching || summary.isFetching || rankings.isFetching || activeUsers.isFetching || protocolShares.isFetching} onClick={refresh}>刷新</Button>
    </div>
    <div className="stat-grid">
      <div className="stat-card"><div className="stat-icon"><CloudDownloadOutlined /></div><div className="stat-label">所选范围总流量</div><div className="stat-value">{bytes(summary.data?.totalBytes || 0)}</div></div>
      <div className="stat-card"><div className="stat-icon"><ArrowUpOutlined /></div><div className="stat-label">上行</div><div className="stat-value" style={{ color: 'var(--primary-color)' }}>{bytes(summary.data?.upBytes || 0)}</div></div>
      <div className="stat-card"><div className="stat-icon"><ArrowDownOutlined /></div><div className="stat-label">下行</div><div className="stat-value" style={{ color: '#42a56f' }}>{bytes(summary.data?.downBytes || 0)}</div></div>
      <div className="stat-card"><div className="stat-icon"><UserOutlined /></div><div className="stat-label">活跃用户</div><div className="stat-value">{activeUsers.data?.totalUsers || 0}</div></div>
      <div className="stat-card"><div className="stat-icon"><FieldTimeOutlined /></div><div className="stat-label">采样次数</div><div className="stat-value">{summary.data?.samples || 0}</div></div>
    </div>
    <section className="overview-card"><div className="overview-card-title"><Typography.Title level={4} style={{ margin: 0 }}>趋势</Typography.Title><Typography.Text type="secondary"><LineChartOutlined /> 蓝线上行，绿线下行</Typography.Text></div><InteractiveChart height={280} ariaLabel="流量趋势，蓝线上行，绿线下行，悬停查看数值" emptyText="当前范围暂无流量" labels={trendSlots.map((slot) => slot.label)} formatValue={bytes} series={[{ name: '上行', color: 'var(--primary-color)', values: trendSlots.map((slot) => slot.up) }, { name: '下行', color: '#42a56f', values: trendSlots.map((slot) => slot.down) }]} /></section>
    <section className="overview-card"><div className="overview-card-title"><Typography.Title level={4} style={{ margin: 0 }}>活跃用户</Typography.Title><Typography.Text type="secondary">每个时段内有流量的独立用户数</Typography.Text></div><InteractiveChart height={140} integerTicks ariaLabel="活跃用户数趋势，橙线为每时段有流量的独立用户数，悬停查看数值" emptyText="当前范围暂无活跃用户" labels={activeSlots.map((slot) => slot.label)} formatValue={(value) => String(Math.round(value))} series={[{ name: '活跃用户', color: '#e8a33d', values: activeSlots.map((slot) => slot.users) }]} /></section>
    <section className="overview-card">
      <div className="overview-card-title"><Typography.Title level={4} style={{ margin: 0 }}>{dimension === 'user' ? '用户排行' : '协议排行'}</Typography.Title><Typography.Text type="secondary">用户统计不含协议归属，协议统计不含用户归属</Typography.Text></div>
      <Table rowKey="id" size="small" pagination={false} loading={rankings.isLoading} dataSource={rankings.data?.items || []} columns={[
        { title: '对象', dataIndex: 'name' },
        { title: '上行', dataIndex: 'upBytes', render: bytes },
        { title: '下行', dataIndex: 'downBytes', render: bytes },
        {
          title: '总流量', dataIndex: 'totalBytes',
          render: (value: number) => {
            const max = Math.max(1, ...(rankings.data?.items || []).map((item) => item.totalBytes));
            return <div style={{ display: 'flex', alignItems: 'center', gap: 8, minWidth: 180 }}>
              <Progress percent={(value / max) * 100} showInfo={false} size="small" strokeColor={dimension === 'user' ? 'var(--primary-color)' : '#42a56f'} style={{ flex: 1, margin: 0 }} />
              <Typography.Text style={{ whiteSpace: 'nowrap' }}>{bytes(value)}</Typography.Text>
            </div>;
          }
        }
      ]} />
    </section>
    <section className="overview-card"><div className="overview-card-title"><Typography.Title level={4} style={{ margin: 0 }}>协议流量占比</Typography.Title><Typography.Text type="secondary">基于入站统计，不含用户维度</Typography.Text></div><ProtocolDonut shares={protocolShares.data?.items || []} /></section>
  </Page>;
}
