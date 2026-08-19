import { useEffect, useState } from 'react';

// 与 styles.css 历史断点保持一致：<992px 视为移动端（侧栏换 Drawer）。
const QUERY = '(max-width: 991px)';

export function useIsMobile() {
  const [isMobile, setIsMobile] = useState(() => window.matchMedia(QUERY).matches);

  useEffect(() => {
    const media = window.matchMedia(QUERY);
    const onChange = (event: MediaQueryListEvent) => setIsMobile(event.matches);
    media.addEventListener('change', onChange);
    return () => media.removeEventListener('change', onChange);
  }, []);

  return isMobile;
}
