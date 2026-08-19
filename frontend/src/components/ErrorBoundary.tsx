import { Component, type ErrorInfo, type ReactNode } from 'react';
import { HomeOutlined, ReloadOutlined } from '@ant-design/icons';
import { Button, Result, Typography } from 'antd';
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
      <Result
        status="error"
        title="页面渲染出错"
        subTitle={(
          <>
            <div>{this.state.message}</div>
            {this.state.requestId ? <Typography.Text type="secondary" copyable>{this.state.requestId}</Typography.Text> : null}
          </>
        )}
        extra={[
          <Button key="retry" type="primary" icon={<ReloadOutlined />} onClick={this.handleRetry}>
            重试
          </Button>,
          <Button key="home" icon={<HomeOutlined />} onClick={this.handleHome}>
            返回首页
          </Button>,
        ]}
      />
    );
  }
}
