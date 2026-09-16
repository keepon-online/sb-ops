import type { CSSProperties } from 'react';

interface BrandLogoProps {
  size?: number;
  className?: string;
  style?: CSSProperties;
}

// 主题消费约定（勿硬编码色值）：
// - 主面/描边消费 var(--primary-color)，副面与霓虹终点消费
//   var(--primary-accent)，随 blue/purple/green/orange 四套主题整体换装；
// - 变量经 style 内联 CSS 注入（stopColor 属性写 var() 跨浏览器解析不可靠），
//   并带 #0284c7 / #a78bfa 回退，无 ThemeProvider 的孤立渲染仍是默认蓝紫；
// - 背景暗板、白色高光与投影属结构性颜色，不随主题变化。
export function BrandLogo({ size = 24, className, style }: BrandLogoProps) {
  return (
    <svg
      xmlns="http://www.w3.org/2000/svg"
      viewBox="0 0 512 512"
      width={size}
      height={size}
      className={className}
      style={{ display: 'inline-block', verticalAlign: 'middle', flexShrink: 0, ...style }}
      aria-hidden="true"
    >
      <defs>
        <radialGradient id="blBgGlow" cx="50%" cy="40%" r="60%">
          <stop offset="0%" stopColor="#1e293b" />
          <stop offset="60%" stopColor="#0f172a" />
          <stop offset="100%" stopColor="#070b14" />
        </radialGradient>
        <linearGradient id="blBorderGrad" x1="0%" y1="0%" x2="100%" y2="100%">
          <stop offset="0%" style={{ stopColor: 'var(--primary-color, #0284c7)' }} stopOpacity="0.8" />
          <stop offset="40%" style={{ stopColor: 'var(--primary-accent, #a78bfa)' }} stopOpacity="0.4" />
          <stop offset="100%" style={{ stopColor: 'var(--primary-color, #0284c7)' }} stopOpacity="0.1" />
        </linearGradient>
        <linearGradient id="blTopFace" x1="0%" y1="100%" x2="100%" y2="0%">
          <stop offset="0%" style={{ stopColor: 'var(--primary-color, #0284c7)' }} />
          <stop offset="50%" style={{ stopColor: 'color-mix(in srgb, var(--primary-color, #0284c7) 55%, white)' }} />
          <stop offset="100%" style={{ stopColor: 'color-mix(in srgb, var(--primary-color, #0284c7) 25%, white)' }} />
        </linearGradient>
        <linearGradient id="blLeftFace" x1="0%" y1="0%" x2="100%" y2="100%">
          <stop offset="0%" style={{ stopColor: 'var(--primary-accent, #a78bfa)' }} />
          <stop offset="40%" style={{ stopColor: 'color-mix(in srgb, var(--primary-accent, #a78bfa) 85%, black)' }} />
          <stop offset="100%" style={{ stopColor: 'color-mix(in srgb, var(--primary-accent, #a78bfa) 70%, black)' }} />
        </linearGradient>
        <linearGradient id="blRightFace" x1="0%" y1="0%" x2="100%" y2="100%">
          <stop offset="0%" style={{ stopColor: 'var(--primary-color, #0284c7)' }} />
          <stop offset="60%" style={{ stopColor: 'color-mix(in srgb, var(--primary-color, #0284c7) 80%, black)' }} />
          <stop offset="100%" style={{ stopColor: 'color-mix(in srgb, var(--primary-color, #0284c7) 65%, black)' }} />
        </linearGradient>
        <linearGradient id="blNeonS" x1="0%" y1="0%" x2="100%" y2="100%">
          <stop offset="0%" stopColor="#ffffff" />
          <stop offset="50%" style={{ stopColor: 'var(--primary-color, #0284c7)' }} />
          <stop offset="100%" style={{ stopColor: 'var(--primary-accent, #a78bfa)' }} />
        </linearGradient>
        <filter id="blNeonBlur" x="-30%" y="-30%" width="160%" height="160%">
          <feGaussianBlur stdDeviation="8" result="blur" />
          <feMerge>
            <feMergeNode in="blur" />
            <feMergeNode in="blur" />
            <feMergeNode in="SourceGraphic" />
          </feMerge>
        </filter>
      </defs>
      <rect x="20" y="20" width="472" height="472" rx="112" fill="url(#blBgGlow)" />
      <rect x="20" y="20" width="472" height="472" rx="112" fill="none" stroke="url(#blBorderGrad)" strokeWidth="6" />
      <circle cx="256" cy="256" r="130" style={{ fill: 'var(--primary-color, #0284c7)' }} opacity="0.12" />
      <circle cx="256" cy="256" r="80" style={{ fill: 'var(--primary-accent, #a78bfa)' }} opacity="0.18" />
      <g id="blCube">
        <path d="M 256 414 L 396 336 L 256 380 L 116 336 Z" fill="#000000" opacity="0.5" />
        <path d="M 128 180 L 256 252 L 256 396 L 128 324 Z" fill="url(#blLeftFace)" />
        <path d="M 256 252 L 384 180 L 384 324 L 256 396 Z" fill="url(#blRightFace)" />
        <path d="M 256 108 L 384 180 L 256 252 L 128 180 Z" fill="url(#blTopFace)" />
        <line x1="256" y1="252" x2="256" y2="396" style={{ stroke: 'color-mix(in srgb, var(--primary-color, #0284c7) 30%, white)' }} strokeWidth="3" strokeLinecap="round" opacity="0.9" />
        <line x1="128" y1="180" x2="256" y2="252" stroke="#ffffff" strokeWidth="2.5" strokeLinecap="round" opacity="0.7" />
        <line x1="256" y1="252" x2="384" y2="180" style={{ stroke: 'color-mix(in srgb, var(--primary-color, #0284c7) 30%, white)' }} strokeWidth="2.5" strokeLinecap="round" opacity="0.5" />
        <path d="M 128 180 L 256 108 L 384 180" fill="none" stroke="#ffffff" strokeWidth="4" strokeLinecap="round" opacity="0.95" />
        <g filter="url(#blNeonBlur)">
          <path
            d="M 330 150 C 290 120 190 130 180 180 C 170 230 330 220 330 280 C 330 340 220 370 170 330"
            fill="none"
            stroke="url(#blNeonS)"
            strokeWidth="18"
            strokeLinecap="round"
            strokeLinejoin="round"
          />
          <path
            d="M 330 150 C 290 120 190 130 180 180 C 170 230 330 220 330 280 C 330 340 220 370 170 330"
            fill="none"
            stroke="#ffffff"
            strokeWidth="6"
            strokeLinecap="round"
            strokeLinejoin="round"
          />
          <circle cx="330" cy="150" r="9" fill="#ffffff" />
          <circle cx="170" cy="330" r="9" style={{ fill: 'var(--primary-color, #0284c7)' }} />
          <circle cx="256" cy="252" r="7" fill="#ffffff" />
        </g>
      </g>
    </svg>
  );
}
