import { LoginOutlined } from '@ant-design/icons';
import { useMutation, useQuery } from '@tanstack/react-query';
import { App as AntApp, Button, Card, Form, Input, Spin } from 'antd';
import { Navigate, useLocation, useNavigate } from 'react-router-dom';
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
      <Card className="glass-card login-panel" bordered={false}>
        <div className="login-logo">Sing-box Ops</div>
        <Form layout="vertical" onFinish={(values) => mutation.mutate(values)} className="login-form">
          <Form.Item name="username" label="用户名" initialValue="admin" rules={[{ required: true }]}>
            <Input autoComplete="username" placeholder="请输入用户名" />
          </Form.Item>
          <Form.Item name="password" label="密码" rules={[{ required: true }]}>
            <Input.Password autoComplete="current-password" placeholder="请输入密码" />
          </Form.Item>
          <Button type="primary" htmlType="submit" loading={mutation.isPending} icon={<LoginOutlined />} block style={{ height: 40 }}>
            登录
          </Button>
        </Form>
      </Card>
    </div>
  );
}
