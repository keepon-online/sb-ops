import { MoonOutlined, SunOutlined } from '@ant-design/icons';
import { Button, Popover, Space, Typography } from 'antd';
import { themeColors, useAppTheme, type ThemeColor } from '../context/ThemeContext';

export function ThemeSwitcher() {
  const { themeMode, themeColor, setThemeColor, toggleThemeMode } = useAppTheme();

  return (
    <Popover
      placement="bottomRight"
      trigger="hover"
      arrow={{ pointAtCenter: true }}
      content={
        <div style={{ padding: '4px 2px', width: 168 }}>
          <div style={{ marginBottom: 10 }}>
            <Typography.Text type="secondary" style={{ fontSize: 12, display: 'block', marginBottom: 8 }}>
              主题色彩
            </Typography.Text>
            <Space size={8}>
              {(['blue', 'purple', 'green', 'orange'] as ThemeColor[]).map((color) => (
                <button
                  key={color}
                  className="theme-swatch"
                  style={{
                    backgroundColor: themeColors[color][themeMode],
                    borderColor: themeColor === color ? 'var(--text-color)' : 'transparent',
                    width: 22,
                    height: 22
                  }}
                  aria-label={`切换为${color}主题`}
                  title={color}
                  onClick={() => setThemeColor(color)}
                />
              ))}
            </Space>
          </div>
          <div style={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between', paddingTop: 8, borderTop: '1px solid var(--border-color)' }}>
            <Typography.Text type="secondary" style={{ fontSize: 12 }}>模式外观</Typography.Text>
            <Button
              size="small"
              icon={themeMode === 'light' ? <MoonOutlined /> : <SunOutlined />}
              onClick={toggleThemeMode}
            >
              {themeMode === 'light' ? '深色' : '浅色'}
            </Button>
          </div>
        </div>
      }
    >
      <Button
        type="text"
        shape="circle"
        className="login-theme-btn"
        icon={themeMode === 'light' ? <MoonOutlined style={{ fontSize: 16 }} /> : <SunOutlined style={{ fontSize: 16 }} />}
        onClick={toggleThemeMode}
        aria-label="外观与主题"
      />
    </Popover>
  );
}
