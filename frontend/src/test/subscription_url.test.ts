import { describe, expect, it } from 'vitest';
import { subscriptionUrl } from '../services/users';

describe('subscriptionUrl', () => {
  it('falls back to window.location.origin when no baseUrl is set', () => {
    const url = subscriptionUrl('test-token-123', 'clash.yaml');
    expect(url).toBe(`${window.location.origin}/subscribe/test-token-123/clash.yaml`);
  });

  it('uses explicitly provided baseUrl over window.location.origin', () => {
    const url = subscriptionUrl('test-token-123', 'clash.yaml', 'https://sub.example.com');
    expect(url).toBe('https://sub.example.com/subscribe/test-token-123/clash.yaml');
  });

  it('trims trailing slashes from explicitly provided baseUrl', () => {
    const url = subscriptionUrl('test-token-123', 'clash.yaml', 'https://sub.example.com///');
    expect(url).toBe('https://sub.example.com/subscribe/test-token-123/clash.yaml');
  });

  it('properly URI encodes token and format', () => {
    const url = subscriptionUrl('token with space&special', 'clash meta.yaml', 'https://sub.example.com');
    expect(url).toBe('https://sub.example.com/subscribe/token%20with%20space%26special/clash%20meta.yaml');
  });
});
