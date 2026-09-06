import { Component, type ErrorInfo, type ReactNode } from 'react';
import { HomeOutlined, ReloadOutlined } from '@ant-design/icons';
import { Button, Result, Space, Typography } from 'antd';
import { ApiError } from '../services/http';

type Props = { children: ReactNode };
type State = { hasError: boolean; message: string; requestId: string };

/**
 * ErrorBoundary catches render errors anywhere in its subtree and shows a
 * friendly fallback instead of a white screen. Class component because React
 * still requires getDerivedStateFromError for this purpose.
 */
export class ErrorBoundary extends Component<Props, State> {
  state: State = { hasError: false, message: '', requestId: '' };

  static getDerivedStateFromError(error: Error): State {
    return { hasError: true, message: error.message || '未知错误', requestId: error instanceof ApiError ? error.requestId : '' };
  }

  componentDidCatch(error: Error, info: ErrorInfo) {
    // eslint-disable-next-line no-console
    console.error('Unhandled render error:', error, info.componentStack);
  }

  private handleRetry = () => {
    this.setState({ hasError: false, message: '', requestId: '' });
  };

  private handleHome = () => {
    window.location.href = '/overview';
  };

  render() {
    if (!this.state.hasError) {
      return this.props.children;
    }
    return (
      <div
        style={{
          display: 'flex',
          alignItems: 'center',
          justifyContent: 'center',
          minHeight: '60vh',
          padding: '24px 16px'
        }}
      >
        <div
          className="overview-card"
          style={{
            maxWidth: 520,
            width: '100%',
            textAlign: 'center',
            padding: '36px 24px',
            borderRadius: 16,
            border: '1px solid var(--border-color)'
          }}
        >
          <Result
            status="error"
            title={<span style={{ fontSize: 20, fontWeight: 700 }}>页面渲染出错</span>}
            subTitle={
              <Space direction="vertical" size={8} style={{ width: '100%', marginTop: 8 }}>
                <Typography.Text type="secondary" style={{ fontSize: 13, wordBreak: 'break-word' }}>
                  {this.state.message}
                </Typography.Text>
                {this.state.requestId ? (
                  <div style={{ fontSize: 12, color: 'var(--text-secondary)' }}>
                    请求 ID：<Typography.Text code copyable>{this.state.requestId}</Typography.Text>
                  </div>
                ) : null}
              </Space>
            }
            extra={
              <Space size={12} wrap style={{ justifyContent: 'center' }}>
                <Button key="retry" type="primary" icon={<ReloadOutlined />} onClick={this.handleRetry}>
                  重试
                </Button>
                <Button key="home" icon={<HomeOutlined />} onClick={this.handleHome}>
                  返回控制总览
                </Button>
              </Space>
            }
          />
        </div>
      </div>
    );
  }
}

