import { useEffect } from 'react';
import { getToken } from '../../services/http';
import { queryClient } from '../../services/queryClient';

const lastEventKey = 'sb-last-event-id';

function invalidate(type: string) {
  if (type.startsWith('traffic.')) void queryClient.invalidateQueries({ queryKey: ['traffic'] });
  if (type.startsWith('alert.')) void queryClient.invalidateQueries({ queryKey: ['alerts'] });
  if (type === 'reset') void queryClient.invalidateQueries();
  void queryClient.invalidateQueries({ queryKey: ['overview'] });
}

export function useEventStream() {
  useEffect(() => {
    const token = getToken();
    if (!token) return;
    const controller = new AbortController();
    let retry = 1000;
    let timer = 0;

    const connect = async () => {
      try {
        const headers: Record<string, string> = { Authorization: `Bearer ${token}` };
        const last = localStorage.getItem(lastEventKey);
        if (last) headers['Last-Event-ID'] = last;
        const response = await fetch('/api/v1/events', { headers, signal: controller.signal });
        if (!response.ok || !response.body) throw new Error(`SSE HTTP ${response.status}`);
        retry = 1000;
        const reader = response.body.getReader();
        const decoder = new TextDecoder();
        let buffer = '';
        while (!controller.signal.aborted) {
          const { value, done } = await reader.read();
          if (done) break;
          buffer += decoder.decode(value, { stream: true });
          let boundary = buffer.indexOf('\n\n');
          while (boundary >= 0) {
            const block = buffer.slice(0, boundary);
            buffer = buffer.slice(boundary + 2);
            const id = block.split('\n').find((line) => line.startsWith('id: '))?.slice(4);
            const type = block.split('\n').find((line) => line.startsWith('event: '))?.slice(7);
            if (id) localStorage.setItem(lastEventKey, id);
            if (type) invalidate(type);
            boundary = buffer.indexOf('\n\n');
          }
        }
      } catch {
        if (controller.signal.aborted) return;
      }
      timer = window.setTimeout(connect, retry);
      retry = Math.min(retry * 2, 30000);
    };
    void connect();
    return () => {
      controller.abort();
      window.clearTimeout(timer);
    };
  }, []);
}
