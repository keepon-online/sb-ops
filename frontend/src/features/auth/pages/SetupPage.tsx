import { LockOutlined, MoonOutlined, SunOutlined, UserAddOutlined, UserOutlined } from '@ant-design/icons';
import { useMutation, useQuery } from '@tanstack/react-query';
import { App as AntApp, Button, Card, Form, Input, Popover, Result, Space, Spin, Typography } from 'antd';
import { Navigate, useNavigate } from 'react-router-dom';
import { useAppTheme, themeColors, type ThemeColor } from '../../../context/ThemeContext';
import { identityApi } from '../../../services/identity';

type SetupValues = {
  username: string;
  password: string;
  confirmPassword: string;
};

export function SetupPage() {
  const navigate = useNavigate();
  const { message } = AntApp.useApp();
  const { themeMode, themeColor, setThemeColor, toggleThemeMode } = useAppTheme();
  const status = useQuery({ queryKey: ['setup', 'status'], queryFn: identityApi.setupStatus, retry: false });
  const initialize = useMutation({
    mutationFn: (values: SetupValues) => identityApi.initializeAdmin(values.username, values.password),
    onSuccess: () => {
      message.success('管理员已创建，请登录继续配置');
      navigate('/login', { replace: true });
    },
    onError: (error: Error) => message.error(error.message)
  });

  if (status.isLoading) return <div className="centered"><Spin /></div>;
  if (status.isError) return <Result status="error" title="无法读取初始化状态" extra={<Button onClick={() => void status.refetch()}>重试</Button>} />;
  if (status.data?.initialized) return <Navigate to="/login" replace />;

  return (
    <div className="login-shell">
      <div className="login-theme-bar">
        <Popover
          placement="bottomRight"
          trigger="hover"
          arrow={{ pointAtCenter: true }}
          content={
            <div style={{ padding: '4px 2px', width: 168 }}>
              <div style={{ marginBottom: 10 }}>
                <Typography.Text type="secondary" style={{ fontSize: 12, display: 'block', marginBottom: 8 }}>
                  主题色彩
                </Typography.Text>
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
                <Typography.Text type="secondary" style={{ fontSize: 12 }}>模式外观</Typography.Text>
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
          <Button
            type="text"
            shape="circle"
            className="login-theme-btn"
            icon={themeMode === 'light' ? <MoonOutlined style={{ fontSize: 16 }} /> : <SunOutlined style={{ fontSize: 16 }} />}
            onClick={toggleThemeMode}
            aria-label="外观与主题"
          />
        </Popover>
      </div>

      <Card className="glass-card login-panel" variant="borderless">
        <div className="login-header">
          <div className="login-icon-badge">
            <UserAddOutlined />
          </div>
          <div className="login-logo">初始化 Sing-box Ops</div>
          <Typography.Text type="secondary" className="login-subtitle">
            首次使用请设置管理员账号与安全密码
          </Typography.Text>
        </div>

        <Form<SetupValues> layout="vertical" onFinish={(values) => initialize.mutate(values)} className="login-form">
          <Form.Item name="username" label="管理员用户名" rules={[{ required: true, message: '请输入管理员用户名' }]}>
            <Input
              size="large"
              prefix={<UserOutlined style={{ color: 'var(--text-secondary)' }} />}
              autoComplete="username"
              placeholder="例如：admin"
            />
          </Form.Item>
          <Form.Item name="password" label="管理员密码" rules={[{ required: true, message: '请输入管理员密码' }, { min: 10, message: '至少 10 个字符' }, { pattern: /[A-Za-z]/, message: '必须包含字母' }, { pattern: /\d/, message: '必须包含数字' }]}>
            <Input.Password
              size="large"
              prefix={<LockOutlined style={{ color: 'var(--text-secondary)' }} />}
              autoComplete="new-password"
            />
          </Form.Item>
          <Form.Item name="confirmPassword" label="确认密码" dependencies={['password']} rules={[{ required: true, message: '请再次输入密码' }, ({ getFieldValue }) => ({ validator(_, value) { return !value || getFieldValue('password') === value ? Promise.resolve() : Promise.reject(new Error('两次输入的密码不一致')); } })]}>
            <Input.Password
              size="large"
              prefix={<LockOutlined style={{ color: 'var(--text-secondary)' }} />}
              autoComplete="new-password"
            />
          </Form.Item>
          <Button
            type="primary"
            htmlType="submit"
            size="large"
            icon={<UserAddOutlined />}
            loading={initialize.isPending}
            block
            className="login-submit-btn"
          >
            创建管理员
          </Button>
        </Form>
      </Card>
      <div className="login-footer">
        <Typography.Text type="secondary" style={{ fontSize: 12 }}>
          Sing-box Ops · 单 VPS 自托管控制面板
        </Typography.Text>
      </div>
    </div>
  );
}
