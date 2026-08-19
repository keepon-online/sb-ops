import { beforeEach, describe, expect, it, vi } from 'vitest';
import { ApiCode, ApiError, managementRequest, setToken } from '../services/http';

describe('HTTP transports', () => {
  beforeEach(() => {
    vi.unstubAllGlobals();
  });

  it('preserves a standard 403 error and the current session', async () => {
    setToken('token');
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue(new Response(JSON.stringify({
      code: ApiCode.InternalError,
      message: 'permission denied',
      requestId: 'request-1',
      fieldErrors: { role: 'required' }
    }), { status: 403, headers: { 'Content-Type': 'application/json' } })));

    const error = await managementRequest('/api/v1/accounts').catch((value: unknown) => value);
    expect(error).toBeInstanceOf(ApiError);
    expect(error).toMatchObject({
      status: 403,
      code: ApiCode.InternalError,
      requestId: 'request-1',
      message: 'permission denied (role: required)'
    });
    expect(localStorage.getItem('sb-token')).toBe('token');
  });

  it('unwraps the success envelope and returns data only', async () => {
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue(new Response(JSON.stringify({
      code: 0,
      message: 'success',
      data: { items: [1, 2] },
      requestId: 'request-2'
    }), { status: 200, headers: { 'Content-Type': 'application/json' } })));

    await expect(managementRequest('/api/v1/users')).resolves.toEqual({ items: [1, 2] });
  });

  it('throws an ApiError when a 200 body carries a non-zero business code', async () => {
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue(new Response(JSON.stringify({
      code: ApiCode.SyncFailed,
      message: '同步失败',
      data: null,
      requestId: 'request-3'
    }), { status: 200, headers: { 'Content-Type': 'application/json' } })));

    const error = await managementRequest('/api/v1/singbox').catch((value: unknown) => value);
    expect(error).toBeInstanceOf(ApiError);
    expect(error).toMatchObject({ status: 200, code: ApiCode.SyncFailed, message: '同步失败' });
  });
});
