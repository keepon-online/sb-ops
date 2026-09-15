import { LockOutlined, LoginOutlined, UserOutlined } from '@ant-design/icons';
import { useMutation, useQuery } from '@tanstack/react-query';
import { Alert, App as AntApp, Button, Card, Form, Input, Spin, Typography } from 'antd';
import { Navigate, useLocation, useNavigate } from 'react-router-dom';
import { BrandLogo } from '../../../components/BrandLogo';
import { ThemeSwitcher } from '../../../components/ThemeSwitcher';
import { consumeReturnTo, setToken } from '../../../services/http';
import { identityApi } from '../../../services/identity';
import { queryClient } from '../../../services/queryClient';

export function LoginPage() {
  const navigate = useNavigate();
  const location = useLocation();
  const { message } = AntApp.useApp();
  const setup = useQuery({ queryKey: ['setup', 'status'], queryFn: identityApi.setupStatus, retry: false });
  const mutation = useMutation({
    mutationFn: (values: { username: string; password: string }) => identityApi.login(values.username, values.password),
    onSuccess: (data) => {
      setToken(data.token);
      queryClient.clear();
      message.success('登录成功');
      const state = location.state as { returnTo?: string } | null;
      navigate(state?.returnTo || consumeReturnTo() || '/overview', { replace: true });
    },
    onError: (err: Error) => message.error(err.message)
  });

  if (setup.isLoading) return <div className="centered"><Spin /></div>;
  if (setup.data && !setup.data.initialized) return <Navigate to="/setup" replace />;

  return (
    <div className="login-shell">
      <div className="login-theme-bar">
        <ThemeSwitcher />
      </div>

      <Card className="glass-card login-panel" variant="borderless">
        <div className="login-header">
          <div style={{ display: 'flex', justifyContent: 'center', marginBottom: 14 }}>
            <BrandLogo size={56} />
          </div>
          <div className="login-logo">Sing-box Ops</div>
          <Typography.Text type="secondary" className="login-subtitle">
            轻量级 sing-box 节点与网络运维控制台
          </Typography.Text>
        </div>

        {setup.isError ? (
          <Alert
            type="warning"
            showIcon
            style={{ marginBottom: 16, textAlign: 'left', borderRadius: 8 }}
            message="无法连接到面板服务"
            description="检测到本地网络离线或面板后台未启动，当前提交可能会失败。"
          />
        ) : null}

        <Form layout="vertical" onFinish={(values) => mutation.mutate(values)} className="login-form">
          <Form.Item
            name="username"
            label="用户名"
            initialValue="admin"
            rules={[{ required: true, message: '请输入用户名' }]}
          >
            <Input
              size="large"
              prefix={<UserOutlined style={{ color: 'var(--text-secondary)' }} />}
              autoComplete="username"
              placeholder="请输入用户名"
            />
          </Form.Item>
          <Form.Item
            name="password"
            label="密码"
            rules={[{ required: true, message: '请输入密码' }]}
          >
            <Input.Password
              size="large"
              prefix={<LockOutlined style={{ color: 'var(--text-secondary)' }} />}
              autoComplete="current-password"
              placeholder="请输入密码"
            />
          </Form.Item>
          <Button
            type="primary"
            htmlType="submit"
            size="large"
            loading={mutation.isPending}
            icon={<LoginOutlined />}
            block
            className="login-submit-btn"
          >
            登录
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
