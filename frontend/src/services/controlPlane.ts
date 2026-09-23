import type { components } from '../generated/management-api';
import { managementRequest } from './http';

export type ManagementOverview = components['schemas']['ManagementOverview'];
export type ManagementAuditEntry = components['schemas']['ManagementAuditEntry'];
export type ManagementWarpRotateResult = components['schemas']['ManagementWarpRotateResult'];

export const controlPlaneApi = {
  overview: () => managementRequest<ManagementOverview>('/api/v1/overview'),
  warpRotate: () => managementRequest<ManagementWarpRotateResult>('/api/v1/system/warp/rotate', { method: 'POST' }),
  audit: (query: Record<string, string | number | undefined>) => {
    const params = new URLSearchParams();
    Object.entries(query).forEach(([key, value]) => { if (value !== undefined && value !== '') params.set(key, String(value)); });
    return managementRequest<components['schemas']['ManagementAuditPage']>(`/api/v1/audit-logs?${params}`);
  }
};
