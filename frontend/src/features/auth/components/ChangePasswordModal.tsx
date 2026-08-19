import { useMutation } from '@tanstack/react-query';
import { App as AntApp, Form, Input, Modal } from 'antd';
import { setToken } from '../../../services/http';
import { identityApi } from '../../../services/identity';
import { queryClient } from '../../../services/queryClient';

type ChangePasswordValues = {
  currentPassword: string;
  newPassword: string;
  confirmPassword: string;
};

interface Props {
  open: boolean;
  onClose: () => void;
}

export function ChangePasswordModal({ open, onClose }: Props) {
  const { message } = AntApp.useApp();
  const [form] = Form.useForm<ChangePasswordValues>();
  const mutation = useMutation({
    mutationFn: (values: ChangePasswordValues) => identityApi.changePassword(values.currentPassword, values.newPassword),
    onSuccess: () => {
      setToken(null);
      queryClient.clear();
      message.success('密码已修改，请重新登录');
      // 重新登录会给用户带上当前页面，但密码变更后旧会话失效，跳登录页
      window.location.href = '/login';
    },
    onError: (error: Error) => message.error(error.message)
  });

  const handleOk = () => {
    form.submit();
  };

  const handleCancel = () => {
    if (mutation.isPending) return;
    form.resetFields();
    onClose();
  };

  return (
    <Modal
      title="修改密码"
      open={open}
      onOk={handleOk}
      onCancel={handleCancel}
      confirmLoading={mutation.isPending}
      okText="保存"
      cancelText="取消"
      destroyOnClose
    >
      <Form<ChangePasswordValues>
        form={form}
        layout="vertical"
        onFinish={(values) => mutation.mutate(values)}
        style={{ marginTop: 16 }}
      >
        <Form.Item name="currentPassword" label="当前密码" rules={[{ required: true, message: '请输入当前密码' }]}>
          <Input.Password autoComplete="current-password" />
        </Form.Item>
        <Form.Item
          name="newPassword"
          label="新密码"
          rules={[
            { required: true, message: '请输入新密码' },
            { min: 10, message: '至少 10 个字符' },
            { pattern: /[A-Za-z]/, message: '必须包含字母' },
            { pattern: /\d/, message: '必须包含数字' }
          ]}
        >
          <Input.Password autoComplete="new-password" />
        </Form.Item>
        <Form.Item
          name="confirmPassword"
          label="确认新密码"
          dependencies={['newPassword']}
          rules={[
            { required: true, message: '请再次输入新密码' },
            ({ getFieldValue }) => ({
              validator(_, value) {
                return !value || getFieldValue('newPassword') === value ? Promise.resolve() : Promise.reject(new Error('两次输入的密码不一致'));
              }
            })
          ]}
        >
          <Input.Password autoComplete="new-password" />
        </Form.Item>
      </Form>
    </Modal>
  );
}