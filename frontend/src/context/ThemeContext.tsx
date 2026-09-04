import { createContext, useContext, useEffect, useState, ReactNode, useMemo } from 'react';
import { theme as antdThemeHelper, ThemeConfig } from 'antd';

export type ThemeMode = 'light' | 'dark';
export type ThemeColor = 'blue' | 'purple' | 'green' | 'orange';

interface ThemeContextType {
  themeMode: ThemeMode;
  themeColor: ThemeColor;
  setThemeMode: (mode: ThemeMode) => void;
  setThemeColor: (color: ThemeColor) => void;
  toggleThemeMode: () => void;
}

const ThemeContext = createContext<ThemeContextType | undefined>(undefined);

// Theme color definitions for light and dark modes
export const themeColors: Record<ThemeColor, { light: string; dark: string; rgb: string; accentLight: string; accentDark: string }> = {
  blue: {
    light: '#0284c7', // Sky blue
    dark: '#38bdf8',  // Cyan/sky dark
    rgb: '56, 189, 248',
    accentLight: '#6366f1',
    accentDark: '#818cf8'
  },
  purple: {
    light: '#7c3aed', // Violet
    dark: '#a78bfa',  // Purple dark
    rgb: '167, 139, 250',
    accentLight: '#c026d3',
    accentDark: '#f472b6'
  },
  green: {
    light: '#059669', // Emerald
    dark: '#34d399',  // Mint dark
    rgb: '52, 211, 153',
    accentLight: '#0891b2',
    accentDark: '#22d3ee'
  },
  orange: {
    light: '#ea580c', // Orange/Sunset
    dark: '#fb923c',  // Amber dark
    rgb: '251, 146, 60',
    accentLight: '#d97706',
    accentDark: '#facc15'
  }
};

export function ThemeProvider({ children }: { children: ReactNode }) {
  const [themeMode, setThemeModeState] = useState<ThemeMode>(() => {
    return (localStorage.getItem('sb-theme-mode') as ThemeMode) || 'dark';
  });

  const [themeColor, setThemeColorState] = useState<ThemeColor>(() => {
    return (localStorage.getItem('sb-theme-color') as ThemeColor) || 'blue';
  });

  const setThemeMode = (mode: ThemeMode) => {
    setThemeModeState(mode);
    localStorage.setItem('sb-theme-mode', mode);
  };

  const setThemeColor = (color: ThemeColor) => {
    setThemeColorState(color);
    localStorage.setItem('sb-theme-color', color);
  };

  const toggleThemeMode = () => {
    setThemeMode(themeMode === 'light' ? 'dark' : 'light');
  };

  // Sync theme configurations with CSS variables
  useEffect(() => {
    const root = document.documentElement;
    const isDark = themeMode === 'dark';
    const activeColor = themeColors[themeColor];
    const primaryHex = isDark ? activeColor.dark : activeColor.light;
    const primaryRGB = isDark ? activeColor.rgb : hexToRgb(activeColor.light);
    const accentHex = isDark ? activeColor.accentDark : activeColor.accentLight;

    // Apply primary color variable
    root.style.setProperty('--primary-color', primaryHex);
    root.style.setProperty('--primary-color-rgb', primaryRGB);
    root.style.setProperty('--primary-accent', accentHex);
    root.style.setProperty('--traffic-down', themeColor === 'green' ? '#38bdf8' : '#42a56f');

    // Apply light/dark color variables
    if (isDark) {
      root.classList.add('dark-theme');
      root.classList.remove('light-theme');
      root.style.setProperty('--bg-layout', '#0b0f19');
      root.style.setProperty('--bg-container', '#131b2e');
      root.style.setProperty('--bg-container-rgb', '19, 27, 46');
      root.style.setProperty('--border-color', '#1e293b');
      root.style.setProperty('--text-color', '#f3f4f6');
      root.style.setProperty('--text-secondary', '#94a3b8');
      root.style.setProperty('--color-success', '#34d399');
      root.style.setProperty('--color-warning', '#fbbf24');
      root.style.setProperty('--color-error', '#f87171');
      root.style.setProperty('--color-info', '#60a5fa');
      root.style.setProperty('--card-shadow', '0 4px 20px -2px rgba(0, 0, 0, 0.4)');
      root.style.setProperty('--glass-bg', 'rgba(19, 27, 46, 0.7)');
      root.style.setProperty('--glass-border', 'rgba(255, 255, 255, 0.06)');
    } else {
      root.classList.add('light-theme');
      root.classList.remove('dark-theme');
      root.style.setProperty('--bg-layout', '#f8fafc');
      root.style.setProperty('--bg-container', '#ffffff');
      root.style.setProperty('--bg-container-rgb', '255, 255, 255');
      root.style.setProperty('--border-color', '#e2e8f0');
      root.style.setProperty('--text-color', '#1e293b');
      root.style.setProperty('--text-secondary', '#64748b');
      root.style.setProperty('--color-success', '#10b981');
      root.style.setProperty('--color-warning', '#f59e0b');
      root.style.setProperty('--color-error', '#ef4444');
      root.style.setProperty('--color-info', '#3b82f6');
      root.style.setProperty('--card-shadow', '0 4px 20px -2px rgba(0, 0, 0, 0.05)');
      root.style.setProperty('--glass-bg', 'rgba(255, 255, 255, 0.75)');
      root.style.setProperty('--glass-border', 'rgba(0, 0, 0, 0.06)');
    }
  }, [themeMode, themeColor]);

  const value = useMemo(() => ({
    themeMode,
    themeColor,
    setThemeMode,
    setThemeColor,
    toggleThemeMode
  }), [themeMode, themeColor]);

  return <ThemeContext.Provider value={value}>{children}</ThemeContext.Provider>;
}

export function useAppTheme() {
  const context = useContext(ThemeContext);
  if (!context) {
    return {
      themeMode: 'dark' as ThemeMode,
      themeColor: 'blue' as ThemeColor,
      setThemeMode: () => {},
      setThemeColor: () => {},
      toggleThemeMode: () => {}
    };
  }
  return context;
}

// Helper to convert hex to rgb string for CSS custom properties
function hexToRgb(hex: string): string {
  const normalizedHex = hex.replace('#', '');
  const r = parseInt(normalizedHex.substring(0, 2), 16);
  const g = parseInt(normalizedHex.substring(2, 4), 16);
  const b = parseInt(normalizedHex.substring(4, 6), 16);
  return `${r}, ${g}, ${b}`;
}

// Generate the ConfigProvider Theme object based on context values
export function useAntdThemeConfig(): ThemeConfig {
  const { themeMode, themeColor } = useAppTheme();

  return useMemo(() => {
    const isDark = themeMode === 'dark';
    const activeColor = themeColors[themeColor];
    const primaryColor = isDark ? activeColor.dark : activeColor.light;
    const primaryRGB = hexToRgb(primaryColor);
    const primaryAlpha = (alpha: number) => `rgba(${primaryRGB}, ${alpha})`;

    return {
      algorithm: isDark ? antdThemeHelper.darkAlgorithm : antdThemeHelper.defaultAlgorithm,
      token: {
        colorPrimary: primaryColor,
        borderRadius: 10,
        fontFamily: "'Plus Jakarta Sans', -apple-system, BlinkMacSystemFont, 'Segoe UI', Roboto, 'PingFang SC', 'Hiragino Sans GB', 'Microsoft YaHei', sans-serif",
        colorBgLayout: isDark ? '#0b0f19' : '#f8fafc',
        colorBgContainer: isDark ? '#131b2e' : '#ffffff',
        colorBorder: isDark ? '#1e293b' : '#e2e8f0',
        colorText: isDark ? '#f3f4f6' : '#1e293b',
        colorTextDescription: isDark ? '#94a3b8' : '#64748b',
        colorBgContainerDisabled: isDark ? '#1e293b' : '#f5f5f5',
        colorBorderDisabled: isDark ? '#334155' : '#d9d9d9',
        colorTextDisabled: isDark ? '#475569' : '#bfbfbf',
        colorBgElevated: isDark ? '#1e293b' : '#ffffff',
        controlItemBgHover: isDark ? 'rgba(255, 255, 255, 0.08)' : 'rgba(0, 0, 0, 0.04)',
        controlItemBgActive: isDark ? 'rgba(255, 255, 255, 0.12)' : 'rgba(0, 0, 0, 0.08)',
        colorSuccess: isDark ? '#34d399' : '#10b981',
        colorWarning: isDark ? '#fbbf24' : '#f59e0b',
        colorError: isDark ? '#f87171' : '#ef4444',
        colorInfo: isDark ? '#60a5fa' : '#3b82f6'
      },
      components: {
        Button: {
          defaultBorderColor: isDark ? '#334155' : '#d9d9d9',
          defaultColor: isDark ? '#e2e8f0' : '#1e293b',
          defaultHoverBorderColor: primaryColor,
          defaultHoverColor: primaryColor,
          defaultActiveBorderColor: primaryColor,
          defaultActiveColor: primaryColor,
          defaultHoverBg: primaryAlpha(isDark ? 0.16 : 0.1),
          defaultActiveBg: primaryAlpha(isDark ? 0.26 : 0.18),
          defaultBg: isDark ? '#131b2e' : '#ffffff',
          primaryShadow: `0 2px 0 ${primaryColor}`,
          dangerShadow: `0 2px 0 #ef4444`,
          ghostBg: 'transparent',
          algorithm: true
        },
        Layout: {
          colorBgHeader: isDark ? '#0f172a' : '#ffffff',
          colorBgBody: isDark ? '#0b0f19' : '#f8fafc',
          colorBgTrigger: isDark ? '#0f172a' : '#ffffff'
        },
        Card: {
          colorBorderSecondary: isDark ? '#1e293b' : '#e2e8f0'
        },
        Menu: {
          colorItemBgSelected: primaryAlpha(isDark ? 0.15 : 0.1)
        },
        Input: {
          activeBorderColor: primaryColor,
          hoverBorderColor: isDark ? '#475569' : '#94a3b8'
        },
        Select: {
          colorPrimary: primaryColor,
          optionSelectedBg: primaryAlpha(isDark ? 0.15 : 0.1)
        },
        Switch: {
          colorPrimary: primaryColor
        },
        Tabs: {
          colorPrimary: primaryColor,
          inkBarColor: primaryColor,
          itemActiveColor: primaryColor,
          itemHoverColor: primaryColor,
          itemSelectedColor: primaryColor
        },
        Table: {
          headerBg: isDark ? '#131b2e' : '#f5f7fa',
          headerColor: isDark ? '#94a3b8' : '#64748b',
          colorBgContainer: isDark ? '#131b2e' : '#ffffff',
          rowHoverBg: isDark ? 'rgba(255, 255, 255, 0.04)' : 'rgba(0, 0, 0, 0.02)'
        },
        Drawer: {
          colorBgElevated: isDark ? '#131b2e' : '#ffffff'
        },
        Modal: {
          contentBg: isDark ? '#131b2e' : '#ffffff',
          headerBg: isDark ? '#131b2e' : '#ffffff'
        },
        Pagination: {
          colorPrimary: primaryColor,
          itemActiveBg: primaryAlpha(isDark ? 0.15 : 0.1),
          itemActiveColor: primaryColor
        },
        Dropdown: {
          colorBgElevated: isDark ? '#1e293b' : '#ffffff',
          controlItemBgHover: isDark ? 'rgba(255, 255, 255, 0.08)' : 'rgba(0, 0, 0, 0.04)'
        },
        Popover: {
          colorBgElevated: isDark ? '#1e293b' : '#ffffff'
        },
        Tooltip: {
          colorBgSpotlight: isDark ? '#1e293b' : '#1e293b',
          colorTextLightSolid: isDark ? '#f3f4f6' : '#ffffff'
        },
        Alert: {
          colorInfoBg: isDark ? 'rgba(96, 165, 250, 0.1)' : 'rgba(59, 130, 246, 0.08)',
          colorSuccessBg: isDark ? 'rgba(52, 211, 153, 0.1)' : 'rgba(16, 185, 129, 0.08)',
          colorWarningBg: isDark ? 'rgba(251, 191, 36, 0.1)' : 'rgba(245, 158, 11, 0.08)',
          colorErrorBg: isDark ? 'rgba(248, 113, 113, 0.1)' : 'rgba(239, 68, 68, 0.08)'
        }
      }
    };
  }, [themeMode, themeColor]);
}
