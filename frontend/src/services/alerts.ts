import type { components } from '../generated/management-api';
import { managementRequest } from './http';

export type OperationalAlert = components['schemas']['Alert'];
export type AlertStats = components['schemas']['AlertStats'];

export const alertsApi = {
  list: (query: { status?: string; severity?: string; type?: string } = {}) => {
    const params = new URLSearchParams();
    Object.entries(query).forEach(([key, value]) => { if (value) params.set(key, value); });
    return managementRequest<components['schemas']['AlertPage']>(`/api/v1/alerts?${params}`);
  },
  stats: (days = 30) => managementRequest<AlertStats>(`/api/v1/alerts/stats?days=${days}`),
  acknowledge: (id: string) => managementRequest<OperationalAlert>(`/api/v1/alerts/${id}/acknowledge`, { method: 'POST' })
};
