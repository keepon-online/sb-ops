import { HomeOutlined, RollbackOutlined } from '@ant-design/icons';
import { Button, Result, Space, Typography } from 'antd';
import { useEffect } from 'react';
import { useNavigate } from 'react-router-dom';

export function NotFoundPage() {
  const navigate = useNavigate();

  useEffect(() => {
    document.title = '404 · Sing-box Ops';
  }, []);

  return (
    <div
      style={{
        display: 'flex',
        alignItems: 'center',
        justifyContent: 'center',
        minHeight: '65vh',
        padding: '24px 16px'
      }}
    >
      <div
        className="overview-card"
        style={{
          maxWidth: 480,
          width: '100%',
          textAlign: 'center',
          padding: '36px 24px',
          borderRadius: 16,
          border: '1px solid var(--border-color)'
        }}
      >
        <Result
          status="404"
          title={<span style={{ fontSize: 22, fontWeight: 700 }}>404 · 页面不存在</span>}
          subTitle={
            <Typography.Text type="secondary">
              你访问的页面可能已被移动、删除或地址输入有误。
            </Typography.Text>
          }
          extra={
            <Space size={12} wrap style={{ justifyContent: 'center' }}>
              <Button type="primary" icon={<HomeOutlined />} onClick={() => navigate('/overview')}>
                返回控制总览
              </Button>
              <Button icon={<RollbackOutlined />} onClick={() => window.history.back()}>
                返回上一页
              </Button>
            </Space>
          }
        />
      </div>
    </div>
  );
}

