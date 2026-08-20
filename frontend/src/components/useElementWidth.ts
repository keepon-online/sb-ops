import { useEffect, useRef, useState } from 'react';

// useElementWidth returns the observed pixel width of the returned ref so SVG
// charts can render at native scale. Charts that stretch a fixed 1000-unit
// viewBox with preserveAspectRatio="none" also distort their <text> labels
// (horizontally squeezed on narrow cards); rendering at the real width keeps
// ticks legible. Falls back to `fallback` before the observer reports and in
// environments without ResizeObserver (jsdom).
export function useElementWidth<T extends HTMLElement>(fallback = 1000) {
  const ref = useRef<T | null>(null);
  const [width, setWidth] = useState(fallback);
  useEffect(() => {
    const element = ref.current;
    if (!element || typeof ResizeObserver === 'undefined') return;
    const observer = new ResizeObserver((entries) => {
      const next = entries[0]?.contentRect.width ?? 0;
      if (next > 0) setWidth(Math.round(next));
    });
    observer.observe(element);
    return () => observer.disconnect();
  }, []);
  return [ref, width] as const;
}
