import { useCallback, useEffect, useRef, useState } from 'react';

// useElementWidth returns the observed pixel width of the returned ref so SVG
// charts can render at native scale. Charts that stretch a fixed 1000-unit
// viewBox with preserveAspectRatio="none" also distort their <text> labels
// (horizontally squeezed on narrow cards); rendering at the real width keeps
// ticks legible. Falls back to `fallback` before the observer reports and in
// environments without ResizeObserver (jsdom).
export function useElementWidth<T extends HTMLElement>(fallback = 1000) {
  const [width, setWidth] = useState(fallback);
  const observerRef = useRef<ResizeObserver | null>(null);
  const elementRef = useRef<T | null>(null);

  const ref = useCallback((element: T | null) => {
    elementRef.current = element;
    if (observerRef.current) {
      observerRef.current.disconnect();
      observerRef.current = null;
    }
    if (!element || typeof ResizeObserver === 'undefined') return;

    const initialWidth = element.getBoundingClientRect().width;
    if (initialWidth > 0) {
      setWidth(Math.round(initialWidth));
    }

    const observer = new ResizeObserver((entries) => {
      const next = entries[0]?.contentRect.width ?? 0;
      if (next > 0) setWidth(Math.round(next));
    });
    observer.observe(element);
    observerRef.current = observer;
  }, []);

  useEffect(() => {
    return () => {
      observerRef.current?.disconnect();
      observerRef.current = null;
    };
  }, []);

  return [ref, width] as const;
}
