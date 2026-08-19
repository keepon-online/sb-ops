const tokenKey = 'sb-token';
const returnToKey = 'sb-return-to';

// 管理 API 统一响应信封：code 为数字业务码（0 成功），与后端
// internal/httpx/codes.go 的注册表一一对应，发布后只增不改。
export const ApiCode = {
  ValidationFailed: 1001,
  InternalError: 1002,
  NotFound: 1003,
  TrafficInvalid: 1004,
  Unauthorized: 2001,
  InvalidCredentials: 2002,
  PasswordPolicy: 2003,
  RateLimited: 2004,
  AlreadyInitialized: 2005,
  UsernameConflict: 2006,
  RevisionConflict: 3001,
  ResourceReferenced: 3002,
  NameConflict: 3003,
  CredentialRotationRequired: 3004,
  RuntimeUnavailable: 5001,
  RuntimeFailed: 5002,
  SyncFailed: 5003,
} as const;

type ErrorPayload = {
  code?: number;
  message?: string;
  fieldErrors?: Record<string, string>;
  requestId?: string;
};

type Envelope<T> = {
  code: number;
  message: string;
  data: T;
  requestId?: string;
};

export class ApiError extends Error {
  constructor(
    message: string,
    readonly status: number,
    readonly code: number,
    readonly requestId: string,
    readonly fieldErrors: Record<string, string> = {}
  ) {
    super(message);
    this.name = 'ApiError';
  }
}

export function getToken() {
  return localStorage.getItem(tokenKey);
}

export function setToken(token: string | null) {
  if (token) {
    localStorage.setItem(tokenKey, token);
  } else {
    localStorage.removeItem(tokenKey);
  }
}

export function clearSessionAndRedirect() {
  setToken(null);
  if (window.location.pathname !== '/login') {
    sessionStorage.setItem(returnToKey, `${window.location.pathname}${window.location.search}`);
    window.location.href = '/login';
  }
}

export function consumeReturnTo() {
  const returnTo = sessionStorage.getItem(returnToKey);
  sessionStorage.removeItem(returnToKey);
  return returnTo;
}

async function parsePayload(response: Response): Promise<ErrorPayload | undefined> {
  if (response.status === 204) return undefined;
  const contentType = response.headers.get('Content-Type') || '';
  if (!contentType.includes('json')) return undefined;
  return (await response.json()) as ErrorPayload;
}

function apiError(response: Response, payload?: ErrorPayload) {
  const message = payload?.message || '请求失败';
  const fieldErrors = payload?.fieldErrors || {};
  const details = Object.entries(fieldErrors).map(([field, reason]) => `${field}: ${reason}`);
  return new ApiError(
    details.length > 0 ? `${message} (${details.join('; ')})` : message,
    response.status,
    typeof payload?.code === 'number' ? payload.code : -1,
    payload?.requestId || response.headers.get('X-Request-ID') || '',
    fieldErrors
  );
}

async function fetchWithAuth(path: string, options: RequestInit) {
  const headers = new Headers(options.headers);
  if (options.body !== undefined) headers.set('Content-Type', 'application/json');
  const token = getToken();
  if (token) headers.set('Authorization', `Bearer ${token}`);
  return { response: await fetch(path, { ...options, headers }), token };
}

export async function managementRequest<T>(path: string, options: RequestInit = {}): Promise<T> {
  const { response, token } = await fetchWithAuth(path, options);
  const payload = await parsePayload(response);
  if (!response.ok) {
    if (response.status === 401 && token) clearSessionAndRedirect();
    throw apiError(response, payload);
  }
  if (payload && typeof payload.code === 'number' && payload.code !== 0) {
    throw apiError(response, payload);
  }
  return (payload as Envelope<T> | undefined)?.data as T;
}
