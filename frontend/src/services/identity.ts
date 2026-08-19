import type { components } from '../generated/management-api';
import { managementRequest } from './http';

export type IdentityAccount = components['schemas']['IdentityAccount'];

export const identityApi = {
  setupStatus: () => managementRequest<components['schemas']['SetupStatusResponse']>('/api/v1/setup/status'),
  initializeAdmin: (username: string, password: string) =>
    managementRequest<IdentityAccount>('/api/v1/setup/admin', { method: 'POST', body: JSON.stringify({ username, password }) }),
  login: (username: string, password: string) =>
    managementRequest<components['schemas']['IdentityLoginResponse']>('/api/v1/auth/login', {
      method: 'POST',
      body: JSON.stringify({ username, password })
    }),
  me: () => managementRequest<components['schemas']['IdentityMeResponse']>('/api/v1/auth/me'),
  changePassword: (currentPassword: string, newPassword: string) =>
    managementRequest<IdentityAccount>('/api/v1/auth/change-password', {
      method: 'POST',
      body: JSON.stringify({ currentPassword, newPassword })
    }),
  logout: () => managementRequest<void>('/api/v1/auth/logout', { method: 'POST', body: '{}' })
};
