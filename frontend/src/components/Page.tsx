import { Space, Typography } from 'antd';
import type { ReactNode } from 'react';

const { Title } = Typography;

export function Page({ title, children }: { title: string; children: ReactNode }) {
  return (
    <Space direction="vertical" size={16} className="page" style={{ width: '100%' }}>
      <Title level={2}>{title}</Title>
      {children}
    </Space>
  );
}
