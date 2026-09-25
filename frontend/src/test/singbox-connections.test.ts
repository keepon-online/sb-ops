import { describe, expect, it } from 'vitest';
import { formatInboundTag } from '../features/singbox/pages/SingBoxPage';

describe('formatInboundTag', () => {
  it('deduplicates repetitive protocol prefixes', () => {
    expect(formatInboundTag('vless-vless-grpc-reality')).toBe('vless-grpc-reality');
    expect(formatInboundTag('vless-vless-h2-reality')).toBe('vless-h2-reality');
    expect(formatInboundTag('shadowsocks-shadowsocks')).toBe('shadowsocks');
    expect(formatInboundTag('shadowsocks-shadowsocks-2022')).toBe('shadowsocks-2022');
    expect(formatInboundTag('vmess-vmess-ws')).toBe('vmess-ws');
    expect(formatInboundTag('tuic-tuic')).toBe('tuic');
    expect(formatInboundTag('tuic-tuic-main')).toBe('tuic-main');
    expect(formatInboundTag('hysteria2-hysteria2-port-hop')).toBe('hysteria2-port-hop');
  });

  it('keeps normal tags untouched', () => {
    expect(formatInboundTag('vless-grpc-reality')).toBe('vless-grpc-reality');
    expect(formatInboundTag('shadowsocks')).toBe('shadowsocks');
    expect(formatInboundTag('vmess-ws')).toBe('vmess-ws');
    expect(formatInboundTag('direct')).toBe('direct');
    expect(formatInboundTag('')).toBe('');
    expect(formatInboundTag(undefined)).toBe('');
  });
});
