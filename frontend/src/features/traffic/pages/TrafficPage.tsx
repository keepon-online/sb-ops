import {
  ArrowDownOutlined,
  ArrowUpOutlined,
  CloudDownloadOutlined,
  DashboardOutlined,
  FieldTimeOutlined,
  LineChartOutlined,
  ReloadOutlined,
  RiseOutlined,
  UserOutlined
} from '@ant-design/icons';
import { useQuery } from '@tanstack/react-query';
import { Button, DatePicker, Empty, Progress, Segmented, Select, Space, Table, Tag, Tooltip, Typography } from 'antd';
import dayjs, { type Dayjs } from 'dayjs';
import { useMemo, useState } from 'react';
import { useSearchParams } from 'react-router-dom';
import { InteractiveChart, type ChartType } from '../../../components/InteractiveChart';
import { Page } from '../../../components/Page';
import { protocolsApi } from '../../../services/protocols';
import { trafficApi, type TrafficProtocolShare, type TrafficRanking } from '../../../services/traffic';
import { usersApi } from '../../../services/users';
import { useAppTheme } from '../../../context/ThemeContext';

const bytes = (value: number) => {
  const units = ['B', 'KB', 'MB', 'GB', 'TB'];
  let current = value;
  let index = 0;
  while (current >= 1024 && index < units.length - 1) { current /= 1024; index += 1; }
  return `${current.toFixed(index === 0 ? 0 : 2)} ${units[index]}`;
};

const formatRate = (bps: number) => {
  const units = ['bps', 'Kbps', 'Mbps', 'Gbps', 'Tbps'];
  let current = Math.max(0, bps);
  let index = 0;
  while (current >= 1000 && index < units.length - 1) {
    current /= 1000;
    index += 1;
  }
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
  const [hoveredId, setHoveredId] = useState<string | null>(null);
  const total = shares.reduce((sum, item) => sum + item.totalBytes, 0);
  if (total <= 0) return <Empty image={Empty.PRESENTED_IMAGE_SIMPLE} description="暂无协议流量" />;
  const radius = 60;
  const circumference = 2 * Math.PI * radius;
  let accumulated = 0;
  const activeShare = shares.find((s) => s.id === hoveredId);

  return <div style={{ display: 'flex', alignItems: 'center', gap: 32, flexWrap: 'wrap', padding: '8px 0' }}>
    <svg viewBox="0 0 160 160" width={160} height={160} role="img" aria-label="协议流量占比环形图">
      <g transform="rotate(-90 80 80)">
        {shares.map((share, index) => {
          const fraction = share.totalBytes / total;
          const dash = `${fraction * circumference} ${circumference}`;
          const offset = -accumulated * circumference;
          accumulated += fraction;
          const isHovered = hoveredId === share.id;
          const isDimmed = hoveredId !== null && !isHovered;
          return (
            <circle
              key={share.id}
              cx="80"
              cy="80"
              r={radius}
              fill="none"
              stroke={shareColors[index % shareColors.length]}
              strokeWidth={isHovered ? 28 : 24}
              strokeDasharray={dash}
              strokeDashoffset={offset}
              opacity={isDimmed ? 0.35 : 1}
              style={{ cursor: 'pointer', transition: 'stroke-width 0.2s ease, opacity 0.2s ease' }}
              onMouseEnter={() => setHoveredId(share.id)}
              onMouseLeave={() => setHoveredId(null)}
            />
          );
        })}
      </g>
      {activeShare ? (
        <>
          <text x="80" y="68" textAnchor="middle" fill="var(--text-color)" fontSize="12" fontWeight="500">
            {activeShare.name.length > 8 ? `${activeShare.name.slice(0, 7)}…` : activeShare.name}
          </text>
          <text x="80" y="86" textAnchor="middle" fill="var(--text-color)" fontSize="13" fontWeight="600">
            {bytes(activeShare.totalBytes)}
          </text>
          <text x="80" y="102" textAnchor="middle" fill="var(--text-secondary)" fontSize="11">
            {((activeShare.totalBytes / total) * 100).toFixed(1)}%
          </text>
        </>
      ) : (
        <>
          <text x="80" y="76" textAnchor="middle" fill="var(--text-color)" fontSize="13">合计</text>
          <text x="80" y="94" textAnchor="middle" fill="var(--text-color)" fontSize="14" fontWeight="600">{bytes(total)}</text>
        </>
      )}
    </svg>
    <Space direction="vertical" size={4}>
      {shares.map((share, index) => {
        const isHovered = hoveredId === share.id;
        return (
          <div
            key={share.id}
            style={{
              display: 'flex',
              alignItems: 'center',
              gap: 8,
              padding: '4px 8px',
              borderRadius: 6,
              cursor: 'pointer',
              background: isHovered ? 'rgba(var(--primary-color-rgb), 0.12)' : 'transparent',
              transition: 'background-color 0.2s ease'
            }}
            onMouseEnter={() => setHoveredId(share.id)}
            onMouseLeave={() => setHoveredId(null)}
          >
            <span style={{ width: 10, height: 10, borderRadius: 2, background: shareColors[index % shareColors.length], display: 'inline-block' }} />
            <Typography.Text strong={isHovered}>{share.name}</Typography.Text>
            <Typography.Text type="secondary">{bytes(share.totalBytes)} · {((share.totalBytes / total) * 100).toFixed(1)}%</Typography.Text>
          </div>
        );
      })}
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

function slotSecondsFor(mode: BucketMode, key?: string): number {
  if (mode === 'hour') return 3600;
  if (mode === 'day') return 86400;
  if (mode === 'month') {
    if (key) {
      const d = dayjs(key);
      if (d.isValid()) return d.daysInMonth() * 86400;
    }
    return 30 * 86400;
  }
  return 365 * 86400;
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
  const { trafficDownColor, chartAccentColor } = useAppTheme();
  const [searchParams, setSearchParams] = useSearchParams();
  const initialDim = searchParams.get('dimension');
  const [dimension, setDimension] = useState<'user' | 'protocol' | 'device'>(
    initialDim === 'protocol' ? 'protocol' : initialDim === 'device' ? 'device' : 'user'
  );

  // Presentation states: volume vs rate & chart styles
  const [metricType, setMetricType] = useState<'volume' | 'rate'>('volume');
  const [chartType, setChartType] = useState<ChartType>('area');

  // 时间范围统一用日期选择(含快捷预设);兼容旧链接的 range 小时数参数。
  const defaultFromDay = dayjs().subtract(29, 'day').format('YYYY-MM-DD');
  const defaultToDay = dayjs().format('YYYY-MM-DD');
  const legacyHours = Number(searchParams.get('range')) || 0;
  const [fromDay, setFromDay] = useState(searchParams.get('from') || (legacyHours > 0 ? dayjs().subtract(legacyHours - 1, 'hour').format('YYYY-MM-DD') : defaultFromDay));
  const [toDay, setToDay] = useState(searchParams.get('to') || defaultToDay);

  // 互斥过滤：用户与设备统计不含协议归属、入站统计不含用户与设备归属
  const [userId, setUserId] = useState(dimension === 'protocol' ? '' : searchParams.get('userId') || '');
  const [protocolId, setProtocolId] = useState(dimension !== 'protocol' ? '' : searchParams.get('protocolId') || '');
  const [deviceId, setDeviceId] = useState(dimension === 'protocol' ? '' : searchParams.get('deviceId') || '');

  const updateFilters = (next: {
    dimension?: 'user' | 'protocol' | 'device';
    userId?: string;
    protocolId?: string;
    deviceId?: string;
    fromDay?: string;
    toDay?: string;
  }) => {
    const merged = { dimension, userId, protocolId, deviceId, fromDay, toDay, ...next };
    if (merged.dimension === 'user') {
      merged.protocolId = '';
    } else if (merged.dimension === 'protocol') {
      merged.userId = '';
      merged.deviceId = '';
    } else if (merged.dimension === 'device') {
      merged.protocolId = '';
    }
    // 当清除用户时，联动清空设备
    if ('userId' in next && !next.userId && !('deviceId' in next)) {
      merged.deviceId = '';
    }
    if (!merged.fromDay || !merged.toDay) {
      merged.fromDay = defaultFromDay;
      merged.toDay = defaultToDay;
    }
    setDimension(merged.dimension);
    setUserId(merged.userId);
    setProtocolId(merged.protocolId);
    setDeviceId(merged.deviceId);
    setFromDay(merged.fromDay);
    setToDay(merged.toDay);

    const query = new URLSearchParams({ dimension: merged.dimension });
    if (merged.fromDay) query.set('from', merged.fromDay);
    if (merged.toDay) query.set('to', merged.toDay);
    if (merged.userId) query.set('userId', merged.userId);
    if (merged.protocolId) query.set('protocolId', merged.protocolId);
    if (merged.deviceId) query.set('deviceId', merged.deviceId);
    setSearchParams(query, { replace: true });
  };

  // 展示粒度由日期范围自动推导;后端只需 hour/day,月/年由前端聚合。
  const mode = useMemo(() => bucketModeFor(fromDay, toDay), [fromDay, toDay]);
  const queryInterval: 'hour' | 'day' = mode === 'hour' ? 'hour' : 'day';
  const filter = useMemo(() => {
    // toDay 含当天(统计到 23:59:59)。URL 可被手工修改，非法日期回退到
    // 当前页面已解析出的默认范围，避免 Invalid Date.toISOString 抛异常。
    const safeFromDay = dayjs(fromDay).isValid() ? fromDay : dayjs().subtract(30, 'day').format('YYYY-MM-DD');
    const safeToDay = dayjs(toDay).isValid() ? toDay : dayjs().format('YYYY-MM-DD');
    const to = new Date(`${safeToDay}T23:59:59.999`);
    return {
      from: new Date(`${safeFromDay}T00:00:00`).toISOString(),
      to: to.toISOString(),
      interval: queryInterval,
      userId,
      protocolId,
      deviceId
    };
  }, [queryInterval, userId, protocolId, deviceId, fromDay, toDay]);

  // 排行过滤：协议维度剥离用户，用户/设备维度剥离协议；排行榜不绑定单个设备走势过滤
  const rankingFilter = useMemo(() => {
    if (dimension === 'protocol') {
      return { ...filter, userId: '', deviceId: '' };
    }
    if (dimension === 'user') {
      return { ...filter, protocolId: '', deviceId: '' };
    }
    // dimension === 'device'
    return { ...filter, protocolId: '', deviceId: '' };
  }, [filter, dimension]);

  const users = useQuery({ queryKey: ['resources', 'users', 'traffic-filter'], queryFn: () => usersApi.list({ limit: 100, sort: 'name:asc' }), retry: false });
  const protocols = useQuery({ queryKey: ['resources', 'protocols'], queryFn: protocolsApi.list, retry: false });
  const devices = useQuery({
    queryKey: ['resources', 'users', userId, 'devices'],
    queryFn: () => usersApi.listDevices(userId),
    enabled: Boolean(userId),
    retry: false
  });

  const series = useQuery({ queryKey: ['traffic', 'series', filter], queryFn: () => trafficApi.series(filter) });
  const summary = useQuery({ queryKey: ['traffic', 'summary', filter], queryFn: () => trafficApi.summary(filter) });
  const rankings = useQuery({ queryKey: ['traffic', 'rankings', rankingFilter, dimension], queryFn: () => trafficApi.rankings({ ...rankingFilter, dimension, limit: 10 }) });
  const activeUsers = useQuery({ queryKey: ['traffic', 'active-users', filter], queryFn: () => trafficApi.activeUsers(filter) });
  const protocolShares = useQuery({ queryKey: ['traffic', 'protocol-shares', filter], queryFn: () => trafficApi.protocolShares(filter) });

  const refresh = () => {
    void series.refetch();
    void summary.refetch();
    void rankings.refetch();
    void activeUsers.refetch();
    void protocolShares.refetch();
    if (userId) void devices.refetch();
  };

  // 归一化槽位:小时/天补齐缺失时段,月/年由日数据聚合;图表与表格共用同一份。
  const trendSlots = useMemo(() => {
    const points = series.data?.items || [];
    if (points.length === 0) return [];
    return bucketSlots(points, (point) => pointBucketKey(point.bucket, mode), fromDay, toDay, mode).map((slot) => {
      const up = slot.items.reduce((sum, item) => sum + item.upBytes, 0);
      const down = slot.items.reduce((sum, item) => sum + item.downBytes, 0);
      const sec = slotSecondsFor(mode, slot.key);
      return {
        label: slot.label,
        key: slot.key,
        sec,
        up,
        down,
        total: up + down
      };
    });
  }, [series.data, fromDay, toDay, mode]);

  // 峰值速率与平均速率计算 (bps)
  const { peakRate, avgRate } = useMemo(() => {
    if (trendSlots.length === 0) return { peakRate: 0, avgRate: 0 };
    let maxRate = 0;
    let totalBits = 0;
    let totalSec = 0;
    for (const s of trendSlots) {
      const slotRate = s.sec > 0 ? ((s.up + s.down) * 8) / s.sec : 0;
      if (slotRate > maxRate) maxRate = slotRate;
      totalBits += (s.up + s.down) * 8;
      totalSec += s.sec;
    }
    const meanRate = totalSec > 0 ? totalBits / totalSec : 0;
    return { peakRate: maxRate, avgRate: meanRate };
  }, [trendSlots]);

  const activeSlots = useMemo(() => {
    const points = activeUsers.data?.items || [];
    if (points.length === 0) return [];
    return bucketSlots(points, (point) => pointBucketKey(point.bucket, mode), fromDay, toDay, mode).map((slot) => ({
      label: slot.label,
      users: slot.items.reduce((peak, item) => Math.max(peak, item.users), 0)
    }));
  }, [activeUsers.data, fromDay, toDay, mode]);

  const selectedDeviceName = devices.data?.items.find((d) => d.id === deviceId)?.name;
  const rankingTitle = dimension === 'user' ? '用户用量排行' : dimension === 'protocol' ? '协议流量排行' : '设备用量排行';
  const rankingDesc = dimension === 'user'
    ? '用户统计不含协议归属，支持单键下钻设备明细'
    : dimension === 'protocol'
    ? '基于接入协议入站统计，不含用户与设备归属'
    : (userId ? '当前选中用户的名下设备排行' : '全量设备流量排行（展示设备及所属用户）');

  return (
    <Page title="流量分析">
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
          <Segmented
            value={dimension}
            onChange={(value) => updateFilters({ dimension: value as 'user' | 'protocol' | 'device' })}
            options={[
              { label: '用户排行', value: 'user' },
              { label: '协议排行', value: 'protocol' },
              { label: '设备排行', value: 'device' }
            ]}
          />
          <Tooltip title={dimension === 'protocol' ? '与协议排行维度互斥：入站统计不含用户归属' : undefined}>
            <Select
              allowClear
              showSearch
              optionFilterProp="label"
              value={userId || undefined}
              onChange={(value) => updateFilters({ userId: value || '', deviceId: '' })}
              placeholder="全部用户"
              style={{ width: 170 }}
              disabled={dimension === 'protocol'}
              loading={users.isLoading}
              options={users.data?.items.map((item) => ({ value: item.id, label: item.name }))}
            />
          </Tooltip>
          <Tooltip title={dimension === 'protocol' ? '入站统计不含设备归属' : (!userId ? '需先选定用户再筛选设备' : undefined)}>
            <Select
              allowClear
              showSearch
              optionFilterProp="label"
              value={deviceId || undefined}
              onChange={(value) => updateFilters({ deviceId: value || '' })}
              placeholder={userId ? '全部设备' : '设备 (需先选用户)'}
              style={{ width: 180 }}
              disabled={dimension === 'protocol' || !userId}
              loading={devices.isLoading}
              options={devices.data?.items.map((item) => ({ value: item.id, label: item.name }))}
            />
          </Tooltip>
          <Tooltip title={dimension === 'user' || dimension === 'device' ? '与用户/设备排行维度互斥：用户统计不含协议归属' : undefined}>
            <Select
              allowClear
              showSearch
              optionFilterProp="label"
              value={protocolId || undefined}
              onChange={(value) => updateFilters({ protocolId: value || '' })}
              placeholder="全部接入协议"
              style={{ width: 170 }}
              disabled={dimension === 'user' || dimension === 'device'}
              loading={protocols.isLoading}
              options={protocols.data?.items.map((item) => ({ value: item.id, label: `${item.name} (${item.type})` }))}
            />
          </Tooltip>
        </Space>
        <Button
          icon={<ReloadOutlined />}
          loading={series.isFetching || summary.isFetching || rankings.isFetching || activeUsers.isFetching || protocolShares.isFetching}
          onClick={refresh}
        >
          刷新
        </Button>
      </div>

      <div className="stat-grid">
        <div className="stat-card">
          <div className="stat-icon"><CloudDownloadOutlined /></div>
          <div className="stat-label">所选范围总流量</div>
          <div className="stat-value">{bytes(summary.data?.totalBytes || 0)}</div>
        </div>
        <div className="stat-card">
          <div className="stat-icon"><ArrowUpOutlined /></div>
          <div className="stat-label">上行流量</div>
          <div className="stat-value" style={{ color: 'var(--primary-color)' }}>{bytes(summary.data?.upBytes || 0)}</div>
        </div>
        <div className="stat-card">
          <div className="stat-icon"><ArrowDownOutlined /></div>
          <div className="stat-label">下行流量</div>
          <div className="stat-value" style={{ color: trafficDownColor }}>{bytes(summary.data?.downBytes || 0)}</div>
        </div>
        <div className="stat-card">
          <div className="stat-icon"><RiseOutlined /></div>
          <div className="stat-label">峰值网络速率</div>
          <div className="stat-value" style={{ color: 'var(--primary-accent)' }}>{formatRate(peakRate)}</div>
        </div>
        <div className="stat-card">
          <div className="stat-icon"><DashboardOutlined /></div>
          <div className="stat-label">平均网络速率</div>
          <div className="stat-value">{formatRate(avgRate)}</div>
        </div>
        <div className="stat-card">
          <div className="stat-icon"><UserOutlined /></div>
          <div className="stat-label">活跃用户</div>
          <div className="stat-value">{activeUsers.data?.totalUsers || 0}</div>
        </div>
        <div className="stat-card">
          <div className="stat-icon"><FieldTimeOutlined /></div>
          <div className="stat-label">采样次数</div>
          <div className="stat-value">{summary.data?.samples || 0}</div>
        </div>
      </div>

      <section className="overview-card">
        <div className="overview-card-title" style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', flexWrap: 'wrap', gap: 12 }}>
          <div>
            <Typography.Title level={4} style={{ margin: 0 }}>趋势分析</Typography.Title>
            <Space wrap size="small" style={{ marginTop: 2 }}>
              <Typography.Text type="secondary">
                <LineChartOutlined /> 上行 (↑) · 下行 (↓)
              </Typography.Text>
              {userId && (
                <Tag color="processing" closable onClose={() => updateFilters({ userId: '', deviceId: '' })}>
                  用户: {users.data?.items.find((u) => u.id === userId)?.name || userId}
                </Tag>
              )}
              {deviceId && (
                <Tag color="processing" closable onClose={() => updateFilters({ deviceId: '' })}>
                  设备: {selectedDeviceName || deviceId}
                </Tag>
              )}
              {protocolId && (
                <Tag color="processing" closable onClose={() => updateFilters({ protocolId: '' })}>
                  协议: {protocols.data?.items.find((p) => p.id === protocolId)?.name || protocolId}
                </Tag>
              )}
            </Space>
          </div>
          <Space wrap size="small">
            <Segmented
              size="small"
              value={metricType}
              onChange={(val) => setMetricType(val as 'volume' | 'rate')}
              options={[
                { label: '流量体积', value: 'volume' },
                { label: '网络速率', value: 'rate' }
              ]}
            />
            <Segmented
              size="small"
              value={chartType}
              onChange={(val) => setChartType(val as ChartType)}
              options={[
                { label: '折线', value: 'line' },
                { label: '面积', value: 'area' },
                { label: '堆叠', value: 'stacked' }
              ]}
            />
          </Space>
        </div>
        <InteractiveChart
          height={280}
          chartType={chartType}
          ariaLabel={metricType === 'rate' ? '网络速率趋势，上行与下行，悬停查看数值' : '流量趋势，上行与下行，悬停查看数值'}
          emptyText="当前范围暂无流量"
          labels={trendSlots.map((slot) => slot.label)}
          formatValue={metricType === 'rate' ? formatRate : bytes}
          series={[
            {
              name: '上行',
              color: 'var(--primary-color)',
              values: trendSlots.map((slot) => (metricType === 'rate' ? (slot.sec > 0 ? (slot.up * 8) / slot.sec : 0) : slot.up))
            },
            {
              name: '下行',
              color: trafficDownColor,
              values: trendSlots.map((slot) => (metricType === 'rate' ? (slot.sec > 0 ? (slot.down * 8) / slot.sec : 0) : slot.down))
            }
          ]}
        />
      </section>

      <section className="overview-card">
        <div className="overview-card-title"><Typography.Title level={4} style={{ margin: 0 }}>活跃用户</Typography.Title><Typography.Text type="secondary">每个时段内有流量的独立用户数</Typography.Text></div>
        <InteractiveChart height={140} integerTicks ariaLabel="活跃用户数趋势，每时段有流量的独立用户数，悬停查看数值" emptyText="当前范围暂无活跃用户" labels={activeSlots.map((slot) => slot.label)} formatValue={(value) => String(Math.round(value))} series={[{ name: '活跃用户', color: chartAccentColor, values: activeSlots.map((slot) => slot.users) }]} />
      </section>

      <section className="overview-card">
        <div className="overview-card-title" style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', flexWrap: 'wrap', gap: 12 }}>
          <div>
            <Typography.Title level={4} style={{ margin: 0 }}>{rankingTitle}</Typography.Title>
            <Typography.Text type="secondary">{rankingDesc}</Typography.Text>
          </div>
          <Segmented
            value={dimension}
            onChange={(value) => updateFilters({ dimension: value as 'user' | 'protocol' | 'device' })}
            options={[
              { label: '用户排行', value: 'user' },
              { label: '协议排行', value: 'protocol' },
              { label: '设备排行', value: 'device' }
            ]}
          />
        </div>
        <Table
          rowKey="id"
          size="small"
          pagination={false}
          loading={rankings.isLoading}
          dataSource={rankings.data?.items || []}
          scroll={{ x: 600 }}
          columns={[
            {
              title: dimension === 'user' ? '用户' : dimension === 'protocol' ? '接入协议' : '设备对象',
              dataIndex: 'name',
              render: (name: string, record: TrafficRanking) => (
                <Space>
                  <Typography.Text strong>{name}</Typography.Text>
                  {dimension === 'user' && userId === record.id && <Tag color="processing">当前已筛选</Tag>}
                  {dimension === 'device' && deviceId === record.id && <Tag color="processing">当前已筛选</Tag>}
                  {dimension === 'protocol' && protocolId === record.id && <Tag color="processing">当前已筛选</Tag>}
                </Space>
              )
            },
            { title: '上行', dataIndex: 'upBytes', render: bytes, width: 110 },
            { title: '下行', dataIndex: 'downBytes', render: bytes, width: 110 },
            {
              title: '总流量',
              dataIndex: 'totalBytes',
              render: (value: number) => {
                const max = Math.max(1, ...(rankings.data?.items || []).map((item) => item.totalBytes));
                return (
                  <div style={{ display: 'flex', alignItems: 'center', gap: 8, minWidth: 160 }}>
                    <Progress
                      percent={(value / max) * 100}
                      showInfo={false}
                      size="small"
                      strokeColor={dimension === 'protocol' ? trafficDownColor : 'var(--primary-color)'}
                      style={{ flex: 1, margin: 0 }}
                    />
                    <Typography.Text style={{ whiteSpace: 'nowrap' }}>{bytes(value)}</Typography.Text>
                  </div>
                );
              }
            },
            {
              title: '操作',
              key: 'action',
              width: 180,
              render: (_, record: TrafficRanking) => {
                if (dimension === 'user') {
                  return (
                    <Space size="small">
                      <Button
                        size="small"
                        type="link"
                        style={{ padding: 0 }}
                        onClick={() => updateFilters({ dimension: 'device', userId: record.id, deviceId: '' })}
                      >
                        设备下钻
                      </Button>
                      {userId === record.id ? (
                        <Button
                          size="small"
                          type="link"
                          danger
                          style={{ padding: 0 }}
                          onClick={() => updateFilters({ userId: '', deviceId: '' })}
                        >
                          取消筛选
                        </Button>
                      ) : (
                        <Button
                          size="small"
                          type="link"
                          style={{ padding: 0 }}
                          onClick={() => updateFilters({ userId: record.id, deviceId: '' })}
                        >
                          筛选走势
                        </Button>
                      )}
                    </Space>
                  );
                }
                if (dimension === 'device') {
                  return (
                    <Space size="small">
                      {deviceId === record.id ? (
                        <Button
                          size="small"
                          type="link"
                          danger
                          style={{ padding: 0 }}
                          onClick={() => updateFilters({ deviceId: '' })}
                        >
                          取消设备走势
                        </Button>
                      ) : (
                        <Button
                          size="small"
                          type="link"
                          style={{ padding: 0 }}
                          onClick={() => updateFilters({ deviceId: record.id })}
                        >
                          仅看此设备走势
                        </Button>
                      )}
                    </Space>
                  );
                }
                return (
                  <Space size="small">
                    {protocolId === record.id ? (
                      <Button
                        size="small"
                        type="link"
                        danger
                        style={{ padding: 0 }}
                        onClick={() => updateFilters({ protocolId: '' })}
                      >
                        取消筛选
                      </Button>
                    ) : (
                      <Button
                        size="small"
                        type="link"
                        style={{ padding: 0 }}
                        onClick={() => updateFilters({ protocolId: record.id })}
                      >
                        筛选走势
                      </Button>
                    )}
                  </Space>
                );
              }
            }
          ]}
        />
      </section>

      <section className="overview-card">
        <div className="overview-card-title">
          <Typography.Title level={4} style={{ margin: 0 }}>协议流量占比</Typography.Title>
          <Typography.Text type="secondary">基于入站统计，不含用户维度</Typography.Text>
        </div>
        <ProtocolDonut shares={protocolShares.data?.items || []} />
      </section>
    </Page>
  );
}

