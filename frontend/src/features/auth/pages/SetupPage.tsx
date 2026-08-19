import { UserAddOutlined } from '@ant-design/icons';
import { useMutation, useQuery } from '@tanstack/react-query';
import { App as AntApp, Button, Card, Form, Input, Result, Spin } from 'antd';
import { Navigate, useNavigate } from 'react-router-dom';
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
  if (status.isError) return <Result status="error" title="无法读取初始化状态" extra={<Button onClick={() => void status.refetch()}>重试</Button>} />;
  if (status.data?.initialized) return <Navigate to="/login" replace />;

  return (
    <div className="login-shell">
      <Card className="glass-card login-panel" bordered={false}>
        <div className="login-logo">初始化 Sing-box Ops</div>
        <Form<SetupValues> layout="vertical" onFinish={(values) => initialize.mutate(values)}>
          <Form.Item name="username" label="管理员用户名" rules={[{ required: true, message: '请输入管理员用户名' }]}>
            <Input autoComplete="username" placeholder="例如：admin" />
          </Form.Item>
          <Form.Item name="password" label="管理员密码" rules={[{ required: true, message: '请输入管理员密码' }, { min: 10, message: '至少 10 个字符' }, { pattern: /[A-Za-z]/, message: '必须包含字母' }, { pattern: /\d/, message: '必须包含数字' }]}>
            <Input.Password autoComplete="new-password" />
          </Form.Item>
          <Form.Item name="confirmPassword" label="确认密码" dependencies={['password']} rules={[{ required: true, message: '请再次输入密码' }, ({ getFieldValue }) => ({ validator(_, value) { return !value || getFieldValue('password') === value ? Promise.resolve() : Promise.reject(new Error('两次输入的密码不一致')); } })]}>
            <Input.Password autoComplete="new-password" />
          </Form.Item>
          <Button type="primary" htmlType="submit" icon={<UserAddOutlined />} loading={initialize.isPending} block style={{ height: 40 }}>
            创建管理员
          </Button>
        </Form>
      </Card>
    </div>
  );
}
