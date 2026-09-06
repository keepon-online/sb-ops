import {
  DisconnectOutlined,
  LockOutlined,
  ReloadOutlined,
  UserAddOutlined,
  UserOutlined
} from '@ant-design/icons';
import { useMutation, useQuery } from '@tanstack/react-query';
import { App as AntApp, Button, Card, Form, Input, Spin, Typography } from 'antd';
import { Navigate, useNavigate } from 'react-router-dom';
import { ThemeSwitcher } from '../../../components/ThemeSwitcher';
import { identityApi } from '../../../services/identity';

type SetupValues = {
  username: string;
  password: string;
  confirmPassword: string;
};

export function SetupPage() {
  const navigate = useNavigate();
  const { message } = AntApp.useApp();
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

  if (status.isError) {
    return (
      <div className="login-shell">
        <div className="login-theme-bar">
          <ThemeSwitcher />
        </div>

        <Card className="glass-card login-panel" variant="borderless" style={{ textAlign: 'center' }}>
          <div className="login-header">
            <div
              className="login-icon-badge"
              style={{
                color: 'var(--color-error)',
                background: 'rgba(239, 68, 68, 0.12)',
                borderColor: 'rgba(239, 68, 68, 0.25)',
                boxShadow: '0 4px 16px rgba(239, 68, 68, 0.18)'
              }}
            >
              <DisconnectOutlined />
            </div>
            <div className="login-logo" style={{ fontSize: 22, marginBottom: 8 }}>
              无法读取初始化状态
            </div>
            <Typography.Text type="secondary" className="login-subtitle" style={{ lineHeight: 1.6, padding: '0 8px' }}>
              控制服务暂时未能连接，请检查本地网络或确认面板后台服务是否正常运行。
            </Typography.Text>
          </div>

          <Button
            type="primary"
            size="large"
            icon={<ReloadOutlined />}
            loading={status.isFetching}
            onClick={() => void status.refetch()}
            block
            className="login-submit-btn"
          >
            重试连接
          </Button>
        </Card>

        <div className="login-footer">
          <Typography.Text type="secondary" style={{ fontSize: 12 }}>
            Sing-box Ops · 单 VPS 自托管控制面板
          </Typography.Text>
        </div>
      </div>
    );
  }

  if (status.data?.initialized) return <Navigate to="/login" replace />;

  return (
    <div className="login-shell">
      <div className="login-theme-bar">
        <ThemeSwitcher />
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
