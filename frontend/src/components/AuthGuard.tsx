import type { ReactNode } from 'react';
import { Navigate, useLocation } from 'react-router-dom';
import { Button, Result, Spin } from 'antd';
import { ReloadOutlined } from '@ant-design/icons';
import { getToken } from '../services/http';
import { useIdentity } from '../hooks/useIdentity';

export function AuthGuard({ children }: { children: ReactNode }) {
  const location = useLocation();
  const token = getToken();
  const { data, isLoading, isError, refetch } = useIdentity();
  if (!token) {
    return <Navigate to="/login" replace state={{ returnTo: location.pathname }} />;
  }
  if (isLoading) {
    return <div className="centered"><Spin /></div>;
  }
  if (isError || !data) {
    return (
      <Result
        status="error"
        title="无法验证登录状态"
        subTitle="请检查服务连接后重试。"
        extra={<Button type="primary" icon={<ReloadOutlined />} onClick={() => void refetch()}>重试</Button>}
      />
    );
  }
  return <>{children}</>;
}
