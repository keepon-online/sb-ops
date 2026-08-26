import {
  ApiOutlined,
  AuditOutlined,
  AlertOutlined,
  CloudUploadOutlined,
  ControlOutlined,
  DashboardOutlined,
  LockOutlined,
  LogoutOutlined,
  MenuOutlined,
  MonitorOutlined,
  MoonOutlined,
  SafetyCertificateOutlined,
  SettingOutlined,
  SunOutlined,
  TeamOutlined,
  UserOutlined,
  LineChartOutlined
} from '@ant-design/icons';
import { Avatar, Button, Drawer, Dropdown, Layout, Menu, Space, Tooltip, Typography } from 'antd';
import type { ItemType } from 'antd/es/menu/interface';
import { useEffect, useRef, useState } from 'react';
import { Outlet, useLocation, useNavigate } from 'react-router-dom';
import { useAppTheme, themeColors, type ThemeColor } from '../context/ThemeContext';
import { useIdentity } from '../hooks/useIdentity';
import { useIsMobile } from '../hooks/useIsMobile';
import { clearSessionAndRedirect } from '../services/http';
import { identityApi } from '../services/identity';
import { useEventStream } from '../features/events/useEventStream';
import { ChangePasswordModal } from '../features/auth/components/ChangePasswordModal';

const { Header, Content, Sider } = Layout;
const { Text } = Typography;

const SIDER_WIDTH = 236;
const SIDER_COLLAPSED_WIDTH = 72;
const SIDER_COLLAPSED_STORAGE_KEY = 'sb-sider-collapsed';

// 主导航只保留高频操作；审计日志折叠进低频「系统」分组。
const navigationItems: ItemType[] = [
  { key: '/overview', icon: <DashboardOutlined />, label: '总览' },
  { key: '/users', icon: <TeamOutlined />, label: '用户订阅' },
  { key: '/protocols', icon: <ApiOutlined />, label: '接入协议' },
  { key: '/traffic', icon: <LineChartOutlined />, label: '流量分析' },
  { key: '/alerts', icon: <AlertOutlined />, label: '运行告警' },
  { key: '/policies', icon: <ControlOutlined />, label: '订阅分流' },
  { key: '/singbox', icon: <SettingOutlined />, label: '服务端配置' },
  {
    type: 'group',
    label: '系统',
    children: [
      { key: '/monitor', icon: <MonitorOutlined />, label: '服务器监控' },
      { key: '/system', icon: <CloudUploadOutlined />, label: '系统升级' },
      { key: '/audit', icon: <AuditOutlined />, label: '审计日志' }
    ]
  }
];

export function AppLayout() {
  useEventStream();
  const location = useLocation();
  const navigate = useNavigate();
  const { data } = useIdentity();
  const [mobileNavigationOpen, setMobileNavigationOpen] = useState(false);
  const [collapsed, setCollapsed] = useState(() => localStorage.getItem(SIDER_COLLAPSED_STORAGE_KEY) === '1');
  const [passwordModalOpen, setPasswordModalOpen] = useState(false);
  const { themeMode, themeColor, setThemeColor, toggleThemeMode } = useAppTheme();
  const isMobile = useIsMobile();
  const contentRef = useRef<HTMLElement>(null);

  // 内容区独立滚动后，切换路由需要手动回滚顶部，避免沿用上一页的滚动位置。
  useEffect(() => {
    contentRef.current?.scrollTo({ top: 0 });
  }, [location.pathname]);
  const selected = location.pathname.startsWith('/policies/')
    ? '/policies'
    : location.pathname === '/'
      ? '/overview'
      : location.pathname;

  const navigateFromMenu = (path: string) => {
    navigate(path);
    setMobileNavigationOpen(false);
  };

  const toggleCollapsed = (value: boolean) => {
    setCollapsed(value);
    localStorage.setItem(SIDER_COLLAPSED_STORAGE_KEY, value ? '1' : '0');
  };

  const logout = async () => {
    try {
      await identityApi.logout();
    } finally {
      clearSessionAndRedirect();
    }
  };

  const renderNavigation = (siderCollapsed: boolean) => (
    <div className="sider-content">
      <div className="sider-navigation">
        <div className="brand">
          {siderCollapsed ? <SafetyCertificateOutlined className="brand-icon" /> : 'Sing-box Ops'}
        </div>
        <Menu
          theme={themeMode}
          mode="inline"
          selectedKeys={[selected]}
          items={navigationItems}
          onClick={(item) => navigateFromMenu(item.key)}
        />
      </div>
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
          <Space>
            {isMobile && (
              <Button className="mobile-menu-button" type="text" icon={<MenuOutlined />} aria-label="打开导航" onClick={() => setMobileNavigationOpen(true)} />
            )}
            <SafetyCertificateOutlined style={{ color: 'var(--primary-color)' }} />
            <Text strong>运营控制台</Text>
          </Space>
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
        </Header>
        <Content ref={contentRef} className="content">
          <Outlet />
        </Content>
      </Layout>
      <ChangePasswordModal open={passwordModalOpen} onClose={() => setPasswordModalOpen(false)} />
    </Layout>
  );
}
