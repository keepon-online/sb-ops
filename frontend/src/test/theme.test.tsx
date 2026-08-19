import { ConfigProvider } from 'antd';
import { Button } from 'antd';
import { cleanup, render } from '@testing-library/react';
import { afterEach, describe, expect, it } from 'vitest';
import { ThemeProvider, useAntdThemeConfig } from '../context/ThemeContext';

// 用应用真实的主题配置渲染按钮，校验 antd cssinjs 运行时注入的样式：
// default/outlined 按钮 hover/active 背景必须随主题色生成，且不再出现旧的硬编码蓝色。
function renderWithTheme(mode: 'light' | 'dark', color: 'blue' | 'purple' | 'green' | 'orange') {
  localStorage.setItem('sb-theme-mode', mode);
  localStorage.setItem('sb-theme-color', color);

  function Probe() {
    const themeConfig = useAntdThemeConfig();
    return (
      <ConfigProvider theme={themeConfig}>
        <Button>普通按钮</Button>
        <Button danger>危险按钮</Button>
      </ConfigProvider>
    );
  }

  const { container } = render(
    <ThemeProvider>
      <Probe />
    </ThemeProvider>
  );
  return container;
}

function injectedCss(): string {
  return Array.from(document.querySelectorAll('style'))
    .map((node) => node.textContent ?? '')
    .join('\n');
}

describe('theme token adaptation', () => {
  afterEach(() => cleanup());

  it('default/outlined button hover/active backgrounds derive from the active theme color', () => {
    renderWithTheme('light', 'green');

    const btn = document.querySelector('button.ant-btn-variant-outlined:not(.ant-btn-dangerous)');
    expect(btn).not.toBeNull();
    expect(btn?.className).toContain('ant-btn-color-default');

    // 浅色 green 主题：hover 10%、active 18% 的主题色浅底
    const css = injectedCss();
    expect(css).toContain('rgba(5, 150, 105, 0.1)');
    expect(css).toContain('rgba(5, 150, 105, 0.18)');
    // 旧版硬编码的蓝色 hover/active 与选中背景不应再出现
    expect(css).not.toContain('#0369a1');
    expect(css).not.toContain('rgba(2, 132, 199');
    expect(css).not.toContain('rgba(3, 105, 161');
    expect(css).not.toContain('rgba(96, 165, 250');
  });

  it('dark mode uses stronger tint alphas from the dark variant color', () => {
    renderWithTheme('dark', 'orange');

    // 深色 orange 主题：hover 16%、active 26%
    const css = injectedCss();
    expect(css).toContain('rgba(251, 146, 60, 0.16)');
    expect(css).toContain('rgba(251, 146, 60, 0.26)');
    expect(css).not.toContain('#60a5fa');
  });

  it('danger outlined button keeps red text and border', () => {
    renderWithTheme('light', 'green');

    const danger = document.querySelector('button.ant-btn-dangerous.ant-btn-variant-outlined');
    expect(danger).not.toBeNull();
    // danger 按钮的主色仍是错误红，而非主题绿
    const css = injectedCss();
    expect(css).toContain('#ef4444');
  });
});
