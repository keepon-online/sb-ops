import type { components } from '../generated/management-api';
import { managementRequest } from './http';

export type ResourceUser = components['schemas']['ResourceUser'];
export type ResourceUserStatus = components['schemas']['ResourceUserStatus'];
export type ResourceUserPage = components['schemas']['ResourceUsersPage'];
export type ResourceUserWrite = components['schemas']['ResourceCreateUserRequest'];
export type ResourceUserUpdate = components['schemas']['ResourceUpdateUserRequest'];
export type IssuedCredential = components['schemas']['ResourceIssuedCredential'];
export type ResourceDevice = components['schemas']['ResourceDevice'];

export type UserListParams = {
  cursor?: string;
  limit?: number;
  query?: string;
  status?: ResourceUserStatus;
  sort?: string;
};

function userPath(id: string, suffix = '') {
  return `/api/v1/users/${encodeURIComponent(id)}${suffix}`;
}

export function subscriptionUrl(token: string, format: string, baseUrl?: string) {
  const base = (baseUrl || window.location.origin).replace(/\/+$/, '');
  return `${base}/subscribe/${encodeURIComponent(token)}/${encodeURIComponent(format)}`;
}

export const usersApi = {
  list: (params: UserListParams) => {
    const query = new URLSearchParams();
    if (params.cursor) query.set('cursor', params.cursor);
    if (params.limit) query.set('limit', String(params.limit));
    if (params.query) query.set('query', params.query);
    if (params.status) query.set('status', params.status);
    if (params.sort) query.set('sort', params.sort);
    return managementRequest<ResourceUserPage>(`/api/v1/users?${query}`);
  },
  create: (input: ResourceUserWrite) => managementRequest<components['schemas']['ResourceUserMutationResponse']>('/api/v1/users', {
    method: 'POST', body: JSON.stringify(input)
  }),
  update: (id: string, input: ResourceUserUpdate) => managementRequest<ResourceUser>(userPath(id), {
    method: 'PATCH', body: JSON.stringify(input)
  }),
  remove: (id: string) => managementRequest<void>(userPath(id), { method: 'DELETE' }),
  batchStatus: (ids: string[], enabled: boolean) => managementRequest<void>('/api/v1/users/batch-status', {
    method: 'POST', body: JSON.stringify({ ids, enabled })
  }),
  listDevices: (id: string) => managementRequest<components['schemas']['ResourceDeviceList']>(userPath(id, '/devices')),
  updateDevice: (id: string, did: string, enabled: boolean) => managementRequest<void>(userPath(id, `/devices/${encodeURIComponent(did)}`), {
    method: 'PATCH', body: JSON.stringify({ enabled })
  }),
  deleteDevice: (id: string, did: string) => managementRequest<void>(userPath(id, `/devices/${encodeURIComponent(did)}`), {
    method: 'DELETE'
  }),
  rotateDevice: (id: string, did: string) => managementRequest<components['schemas']['ResourceDeviceMutationResponse']>(userPath(id, `/devices/${encodeURIComponent(did)}/credentials/rotate`), {
    method: 'POST', body: '{}'
  }),
  rotateSubscription: (id: string) => managementRequest<{ subscriptionToken: string; subscriptionBaseUrl?: string }>(userPath(id, '/subscription/rotate'), {
    method: 'POST', body: '{}'
  }),
  resetTraffic: (id: string) => managementRequest<ResourceUser>(userPath(id, '/traffic/reset'), {
    method: 'POST', body: '{}'
  }),
  adjustTraffic: (id: string, input: components['schemas']['ResourceTrafficAdjustmentRequest']) => managementRequest<ResourceUser>(userPath(id, '/traffic/adjust'), {
    method: 'POST', body: JSON.stringify(input)
  }),
  adjustQuota: (id: string, input: components['schemas']['ResourceTrafficAdjustmentRequest']) => managementRequest<ResourceUser>(userPath(id, '/quota/adjust'), {
    method: 'POST', body: JSON.stringify(input)
  })
};
