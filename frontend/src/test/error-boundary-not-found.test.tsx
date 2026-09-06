import { cleanup, fireEvent, render, screen } from '@testing-library/react';
import { ConfigProvider, App as AntApp } from 'antd';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { MemoryRouter, Route, Routes } from 'react-router-dom';
import { NotFoundPage } from '../pages/NotFoundPage';
import { ErrorBoundary } from '../components/ErrorBoundary';

function ProblemChild({ shouldThrow }: { shouldThrow: boolean }) {
  if (shouldThrow) {
    throw new Error('测试渲染异常发生');
  }
  return <div>正常内容显示</div>;
}

describe('NotFoundPage & ErrorBoundary', () => {
  afterEach(() => {
    cleanup();
    vi.restoreAllMocks();
  });

  it('NotFoundPage 渲染 404 标题与返回总览按钮并正确导航', () => {
    function PathTracker() {
      return <div>当前在总览页</div>;
    }

    render(
      <ConfigProvider>
        <AntApp>
          <MemoryRouter initialEntries={['/invalid-path']}>
            <Routes>
              <Route path="/overview" element={<PathTracker />} />
              <Route path="*" element={<NotFoundPage />} />
            </Routes>
          </MemoryRouter>
        </AntApp>
      </ConfigProvider>
    );

    expect(screen.getByText('404 · 页面不存在')).toBeInTheDocument();
    const homeBtn = screen.getByRole('button', { name: /返回控制总览/ });
    expect(homeBtn).toBeInTheDocument();

    fireEvent.click(homeBtn);
    expect(screen.getByText('当前在总览页')).toBeInTheDocument();
  });

  it('ErrorBoundary 捕获子组件渲染异常并展示友好的错误卡片', () => {
    const consoleSpy = vi.spyOn(console, 'error').mockImplementation(() => {});
    const errorHandler = (e: ErrorEvent) => e.preventDefault();
    window.addEventListener('error', errorHandler);

    try {
      render(
        <ConfigProvider>
          <AntApp>
            <ErrorBoundary>
              <ProblemChild shouldThrow={true} />
            </ErrorBoundary>
          </AntApp>
        </ConfigProvider>
      );

      expect(screen.getByText('页面渲染出错')).toBeInTheDocument();
      expect(screen.getByText('测试渲染异常发生')).toBeInTheDocument();
      expect(screen.getByRole('button', { name: /重试/ })).toBeInTheDocument();
      expect(screen.getByRole('button', { name: /返回控制总览/ })).toBeInTheDocument();
    } finally {
      window.removeEventListener('error', errorHandler);
      consoleSpy.mockRestore();
    }
  });
});

