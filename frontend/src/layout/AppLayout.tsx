import {
  AlertOutlined,
  ApiOutlined,
  AuditOutlined,
  BellOutlined,
  BgColorsOutlined,
  CloseOutlined,
  CloudServerOutlined,
  CloudUploadOutlined,
  ControlOutlined,
  DashboardOutlined,
  LineChartOutlined,
  LockOutlined,
  LogoutOutlined,
  MenuFoldOutlined,
  MenuOutlined,
  MonitorOutlined,
  MoonOutlined,
  SafetyCertificateOutlined,
  SettingOutlined,
  SunOutlined,
  TeamOutlined,
  UserOutlined
} from '@ant-design/icons';
import { useQuery } from '@tanstack/react-query';
import { Avatar, Badge, Button, Drawer, Dropdown, Grid, Layout, Menu, Popover, Space, Tag, Tooltip, Typography } from 'antd';
import type { ItemType } from 'antd/es/menu/interface';
import { useEffect, useRef, useState } from 'react';
import { Outlet, useLocation, useNavigate } from 'react-router-dom';
import { useAppTheme, themeColors, type ThemeColor } from '../context/ThemeContext';
import { useIdentity } from '../hooks/useIdentity';
import { useIsMobile } from '../hooks/useIsMobile';
import { alertsApi } from '../services/alerts';
import { controlPlaneApi } from '../services/controlPlane';
import { clearSessionAndRedirect } from '../services/http';
import { identityApi } from '../services/identity';
import { systemApi } from '../services/system';
import { useEventStream } from '../features/events/useEventStream';
import { ChangePasswordModal } from '../features/auth/components/ChangePasswordModal';
import { configStatusLabels, kernelStatusLabels } from '../util/labels';

const { Header, Content, Sider } = Layout;
const { Text } = Typography;

const SIDER_WIDTH = 240;
const SIDER_COLLAPSED_WIDTH = 72;
const SIDER_COLLAPSED_STORAGE_KEY = 'sb-sider-collapsed';

export function AppLayout() {
  useEventStream();
  const location = useLocation();
  const navigate = useNavigate();
  const { data } = useIdentity();
  const [mobileNavigationOpen, setMobileNavigationOpen] = useState(false);
  const [collapsed, setCollapsed] = useState(() => localStorage.getItem(SIDER_COLLAPSED_STORAGE_KEY) === '1');
  const [openKeys, setOpenKeys] = useState<string[]>(() => {
    try {
      const saved = localStorage.getItem('sb-menu-open-keys');
      return saved ? JSON.parse(saved) : ['system-group'];
    } catch {
      return ['system-group'];
    }
  });
  const [passwordModalOpen, setPasswordModalOpen] = useState(false);
  const { themeMode, themeColor, setThemeColor, toggleThemeMode } = useAppTheme();
  const isMobile = useIsMobile();
  const contentRef = useRef<HTMLElement>(null);

  const screens = Grid.useBreakpoint();
  const openAlertsQuery = useQuery({
    queryKey: ['alerts', 'open-summary'],
    queryFn: () => alertsApi.list({ status: 'open' }),
    retry: false,
    refetchInterval: 30000
  });
  const overviewQuery = useQuery({
    queryKey: ['overview'],
    queryFn: controlPlaneApi.overview,
    retry: false,
    refetchInterval: 30000
  });
  const upgradeQuery = useQuery({
    queryKey: ['system', 'upgrade'],
    queryFn: systemApi.upgradeStatus,
    retry: false,
    staleTime: 60000
  });

  const openAlerts = openAlertsQuery.data?.items || [];
  const openAlertsCount = openAlerts.length;
  const hasCriticalAlert = openAlerts.some((item) => item.severity === 'critical');
  const hasUpgradeAlert = openAlerts.some((item) => item.type === 'upgrade_available') || Boolean(upgradeQuery.data?.latest?.upgradable);
  // 接口失败时留空——版本号 Tag 随之隐藏，不显示过时的兜底版本。
  const currentVersion = upgradeQuery.data?.currentVersion
    ? (upgradeQuery.data.currentVersion.startsWith('v') ? upgradeQuery.data.currentVersion : `v${upgradeQuery.data.currentVersion}`)
    : '';
  const kernelHealthy = overviewQuery.data?.kernelStatus === 'running';
  const kernelError = overviewQuery.data?.kernelStatus === 'unhealthy';

  // 内容区独立滚动后，切换路由需要手动回滚顶部，避免沿用上一页的滚动位置。
  useEffect(() => {
    contentRef.current?.scrollTo?.({ top: 0 });
  }, [location.pathname]);
  const selected = location.pathname.startsWith('/policies/')
    ? '/policies'
    : location.pathname === '/'
      ? '/overview'
      : location.pathname;

  // 路由跳转至系统运维子项时，确保所属分组处于展开状态
  const prevSelectedRef = useRef(selected);
  useEffect(() => {
    if (prevSelectedRef.current !== selected) {
      prevSelectedRef.current = selected;
      if (['/monitor', '/system', '/audit'].includes(selected)) {
        setOpenKeys((prev) => (prev.includes('system-group') ? prev : [...prev, 'system-group']));
      }
    }
  }, [selected]);

  // 全局快捷键 Ctrl/Cmd + B 切换菜单折叠
  useEffect(() => {
    const handleKeyDown = (e: KeyboardEvent) => {
      if ((e.ctrlKey || e.metaKey) && e.key.toLowerCase() === 'b') {
        const target = e.target as HTMLElement;
        if (target && (target.tagName === 'INPUT' || target.tagName === 'TEXTAREA' || target.isContentEditable)) {
          return;
        }
        e.preventDefault();
        toggleCollapsed(!collapsed);
      }
    };
    window.addEventListener('keydown', handleKeyDown);
    return () => window.removeEventListener('keydown', handleKeyDown);
  }, [collapsed]);

  const navigateFromMenu = (path: string) => {
    if (path.startsWith('/')) {
      navigate(path);
      setMobileNavigationOpen(false);
    }
  };

  const toggleCollapsed = (value: boolean) => {
    setCollapsed(value);
    localStorage.setItem(SIDER_COLLAPSED_STORAGE_KEY, value ? '1' : '0');
  };

  const handleOpenChange = (keys: string[]) => {
    setOpenKeys(keys);
    try {
      localStorage.setItem('sb-menu-open-keys', JSON.stringify(keys));
    } catch {
      // ignore
    }
  };

  const logout = async () => {
    try {
      await identityApi.logout();
    } finally {
      clearSessionAndRedirect();
    }
  };

  // 方案 A 菜单项：控制总览 → 业务与协议接入 → 策略规则 → 流量与告警 → 可折叠系统运维
  const getNavigationItems = (siderCollapsed: boolean): ItemType[] => [
    { key: '/overview', icon: <DashboardOutlined />, label: '控制总览' },
    { key: '/users', icon: <TeamOutlined />, label: '用户订阅' },
    { key: '/protocols', icon: <ApiOutlined />, label: '接入协议' },
    { key: '/singbox', icon: <SettingOutlined />, label: '服务配置' },
    { key: '/policies', icon: <ControlOutlined />, label: '订阅分流' },
    { key: '/traffic', icon: <LineChartOutlined />, label: '流量分析' },
    {
      key: '/alerts',
      icon: (
        <Badge
          count={siderCollapsed ? openAlertsCount : 0}
          size="small"
          dot={siderCollapsed && openAlertsCount > 0}
          offset={[2, -2]}
          style={{
            backgroundColor: hasCriticalAlert ? 'var(--color-error)' : 'var(--color-warning)'
          }}
        >
          <AlertOutlined />
        </Badge>
      ),
      label: (
        <span className="nav-menu-item-content">
          <span>运行告警</span>
          {openAlertsCount > 0 && !siderCollapsed && (
            <span className={`nav-menu-badge-count ${hasCriticalAlert ? 'is-critical' : 'is-warning'}`}>
              {openAlertsCount > 99 ? '99+' : openAlertsCount}
            </span>
          )}
        </span>
      )
    },
    {
      key: 'system-group',
      icon: (
        <Badge dot={siderCollapsed && hasUpgradeAlert} offset={[2, -2]} status="processing">
          <CloudServerOutlined />
        </Badge>
      ),
      label: (
        <span className="nav-menu-item-content">
          <span>系统运维</span>
          {hasUpgradeAlert && !siderCollapsed && !openKeys.includes('system-group') && (
            <span className="nav-menu-badge-new">New</span>
          )}
        </span>
      ),
      children: [
        { key: '/monitor', icon: <MonitorOutlined />, label: '主机监控' },
        {
          key: '/system',
          icon: <CloudUploadOutlined />,
          label: (
            <span className="nav-menu-item-content">
              <span>系统管理</span>
              {hasUpgradeAlert && <span className="nav-menu-badge-new">New</span>}
            </span>
          )
        },
        { key: '/audit', icon: <AuditOutlined />, label: '审计日志' }
      ]
    }
  ];

  const renderNavigation = (siderCollapsed: boolean) => (
    <div className="sider-content">
      <div className="sider-navigation">
        <div className="brand">
          <div
            className="brand-main"
            onClick={() => {
              if (siderCollapsed) {
                toggleCollapsed(false);
              } else {
                navigateFromMenu('/overview');
              }
            }}
            style={{ cursor: 'pointer' }}
          >
            <Tooltip title={siderCollapsed ? '展开菜单 (Ctrl+B)' : 'Sing-box Ops 控制台'} placement={siderCollapsed ? 'right' : 'bottom'}>
              <SafetyCertificateOutlined className="brand-icon" />
            </Tooltip>
            {!siderCollapsed && (
              <div className="brand-info">
                <span className="brand-title">Sing-box Ops</span>
                {currentVersion ? (
                  <Tag bordered={false} className="brand-version">
                    {currentVersion}
                  </Tag>
                ) : null}
              </div>
            )}
          </div>
          {!siderCollapsed && (
            <Tooltip title={isMobile ? '关闭导航' : '收起菜单 (Ctrl+B)'} placement="right">
              <Button
                type="text"
                size="small"
                className="sider-toggle-btn"
                icon={isMobile ? <CloseOutlined /> : <MenuFoldOutlined />}
                onClick={(e) => {
                  e.stopPropagation();
                  if (isMobile) {
                    setMobileNavigationOpen(false);
                  } else {
                    toggleCollapsed(true);
                  }
                }}
                aria-label={isMobile ? '关闭导航' : '收起菜单'}
              />
            </Tooltip>
          )}
        </div>
        <Menu
          theme={themeMode}
          mode="inline"
          inlineCollapsed={siderCollapsed}
          selectedKeys={[selected]}
          openKeys={siderCollapsed ? undefined : openKeys}
          onOpenChange={handleOpenChange}
          items={getNavigationItems(siderCollapsed)}
          onClick={(item) => navigateFromMenu(item.key)}
        />
      </div>
      {siderCollapsed ? (
        <div className="theme-controls-collapsed">
          <Popover
            placement="rightBottom"
            trigger="click"
            title={<Text strong style={{ fontSize: 13 }}>外观与主题</Text>}
            content={
              <div style={{ padding: '4px 0', minWidth: 150 }}>
                <div style={{ marginBottom: 12 }}>
                  <Text type="secondary" style={{ fontSize: 12, display: 'block', marginBottom: 8 }}>主题色彩</Text>
                  <Space size={8}>
                    {(['blue', 'purple', 'green', 'orange'] as ThemeColor[]).map((color) => (
                      <button
                        key={color}
                        className="theme-swatch"
                        style={{
                          backgroundColor: themeColors[color][themeMode],
                          borderColor: themeColor === color ? 'var(--text-color)' : 'transparent',
                          width: 22,
                          height: 22
                        }}
                        aria-label={`切换为${color}主题`}
                        title={color}
                        onClick={() => setThemeColor(color)}
                      />
                    ))}
                  </Space>
                </div>
                <div style={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between', paddingTop: 8, borderTop: '1px solid var(--border-color)' }}>
                  <Text type="secondary" style={{ fontSize: 12 }}>模式外观</Text>
                  <Button
                    size="small"
                    icon={themeMode === 'light' ? <MoonOutlined /> : <SunOutlined />}
                    onClick={toggleThemeMode}
                  >
                    {themeMode === 'light' ? '深色' : '浅色'}
                  </Button>
                </div>
              </div>
            }
          >
            <Tooltip title="切换主题外观" placement="right">
              <Button
                type="text"
                shape="circle"
                icon={<BgColorsOutlined style={{ color: 'var(--primary-color)', fontSize: 17 }} />}
                aria-label="切换主题外观"
              />
            </Tooltip>
          </Popover>
        </div>
      ) : (
        <div className="theme-controls">
          <Space size={6}>
            {(['blue', 'purple', 'green', 'orange'] as ThemeColor[]).map((color) => (
              <button
                key={color}
                className="theme-swatch"
                style={{
                  backgroundColor: themeColors[color][themeMode],
                  borderColor: themeColor === color ? 'var(--text-color)' : 'transparent'
                }}
                aria-label={`切换为${color}主题`}
                title={color}
                onClick={() => setThemeColor(color)}
              />
            ))}
          </Space>
          <Tooltip title={themeMode === 'light' ? '深色模式' : '浅色模式'}>
            <Button type="text" shape="circle" icon={themeMode === 'light' ? <MoonOutlined /> : <SunOutlined />} onClick={toggleThemeMode} />
          </Tooltip>
        </div>
      )}
    </div>
  );

  return (
    <Layout className="app-shell">
      {isMobile ? (
        <Drawer
          className="mobile-navigation"
          placement="left"
          width={280}
          open={mobileNavigationOpen}
          onClose={() => setMobileNavigationOpen(false)}
        >
          {renderNavigation(false)}
        </Drawer>
      ) : (
        <Sider
          className={collapsed ? 'sider sider-collapsed' : 'sider'}
          width={SIDER_WIDTH}
          collapsedWidth={SIDER_COLLAPSED_WIDTH}
          collapsible
          collapsed={collapsed}
          onCollapse={toggleCollapsed}
        >
          {renderNavigation(collapsed)}
        </Sider>
      )}
      <Layout>
        <Header className="topbar">
          <Space size={screens.md ? 16 : 8}>
            {isMobile && (
              <Button className="mobile-menu-button" type="text" icon={<MenuOutlined />} aria-label="打开导航" onClick={() => setMobileNavigationOpen(true)} />
            )}
            <SafetyCertificateOutlined style={{ color: 'var(--primary-color)' }} />
            <Text strong>运营控制台</Text>
            {overviewQuery.data && (
              <Tooltip title={`内核状态：${kernelStatusLabels[overviewQuery.data.kernelStatus] || overviewQuery.data.kernelStatus}，配置：${configStatusLabels[overviewQuery.data.configStatus] || overviewQuery.data.configStatus}`}>
                <Tag
                  color={kernelHealthy ? 'success' : kernelError ? 'error' : 'default'}
                  style={{ cursor: 'pointer', borderRadius: 10, padding: '0 8px', fontSize: 12, display: 'inline-flex', alignItems: 'center' }}
                  onClick={() => navigate('/singbox')}
                >
                  <span className={kernelHealthy ? 'pulse-dot' : ''} style={{ width: 6, height: 6, borderRadius: '50%', background: 'currentColor', marginRight: 5 }} />
                  {kernelHealthy ? '内核正常' : kernelError ? '内核异常' : '正在检查'}
                </Tag>
              </Tooltip>
            )}
          </Space>
          <Space size={14}>
            <Popover
              placement="bottomRight"
              title={
                <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', width: 260 }}>
                  <Text strong>运行告警 ({openAlertsCount})</Text>
                  <Button type="link" size="small" style={{ padding: 0 }} onClick={() => navigate('/alerts')}>查看全部</Button>
                </div>
              }
              content={
                <div style={{ width: 260, maxHeight: 300, overflowY: 'auto' }}>
                  {openAlerts.length === 0 ? (
                    <div style={{ textAlign: 'center', padding: '16px 0', color: 'var(--text-secondary)', fontSize: 12 }}>暂无未恢复告警</div>
                  ) : (
                    <Space direction="vertical" size={8} style={{ width: '100%' }}>
                      {openAlerts.slice(0, 5).map((item) => (
                        <div key={item.id} style={{ borderBottom: '1px solid var(--border-color)', paddingBottom: 6 }}>
                          <div style={{ display: 'flex', justifyContent: 'space-between', marginBottom: 2 }}>
                            <Tag color={item.severity === 'critical' ? 'error' : 'warning'} style={{ margin: 0, fontSize: 11 }}>
                              {item.severity === 'critical' ? '严重' : '警告'}
                            </Tag>
                            <span style={{ fontSize: 11, color: 'var(--text-secondary)' }}>
                              {new Date(item.lastSeenAt).toLocaleTimeString()}
                            </span>
                          </div>
                          <div style={{ fontSize: 12, lineHeight: 1.4, wordBreak: 'break-word' }}>{item.message}</div>
                        </div>
                      ))}
                    </Space>
                  )}
                </div>
              }
            >
              <Badge count={openAlertsCount} size="small" overflowCount={99}>
                <Button
                  type="text"
                  shape="circle"
                  icon={<BellOutlined style={{ fontSize: 16, color: hasCriticalAlert ? '#ff4d4f' : undefined }} />}
                  onClick={() => navigate('/alerts')}
                  aria-label="查看告警"
                />
              </Badge>
            </Popover>

            <Dropdown
              menu={{
                items: [
                  { key: 'password', icon: <LockOutlined />, label: '修改密码' },
                  { type: 'divider' },
                  { key: 'logout', icon: <LogoutOutlined />, label: '退出登录', danger: true }
                ],
                onClick: ({ key }) => {
                  if (key === 'password') setPasswordModalOpen(true);
                  if (key === 'logout') void logout();
                }
              }}
              trigger={['click']}
            >
              <Space className="account-entry" size={8}>
                <Avatar size="small" icon={<UserOutlined />} style={{ backgroundColor: 'var(--primary-color)' }} />
                <Text className="account-name" type="secondary">{data?.user.username}</Text>
              </Space>
            </Dropdown>
          </Space>
        </Header>
        <Content ref={contentRef} className="content">
          <Outlet />
        </Content>
      </Layout>
      <ChangePasswordModal open={passwordModalOpen} onClose={() => setPasswordModalOpen(false)} />
    </Layout>
  );
}
