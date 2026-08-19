import React from 'react';
import ReactDOM from 'react-dom/client';
import { QueryClientProvider } from '@tanstack/react-query';
import { ConfigProvider } from 'antd';
import zhCN from 'antd/locale/zh_CN';
import dayjs from 'dayjs';
import 'dayjs/locale/zh-cn';
import App from './app/App';
import { ThemeProvider, useAntdThemeConfig } from './context/ThemeContext';
import { queryClient } from './services/queryClient';
import './styles.css';

dayjs.locale('zh-cn');

function AppRoot() {
  const themeConfig = useAntdThemeConfig();
  return (
    <ConfigProvider locale={zhCN} theme={themeConfig}>
      <App />
    </ConfigProvider>
  );
}

ReactDOM.createRoot(document.getElementById('root')!).render(
  <React.StrictMode>
    <QueryClientProvider client={queryClient}>
      <ThemeProvider>
        <AppRoot />
      </ThemeProvider>
    </QueryClientProvider>
  </React.StrictMode>
);
