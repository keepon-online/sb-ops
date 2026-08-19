import type { components } from '../generated/management-api';
import { managementRequest } from './http';

export type OperationalAlert = components['schemas']['Alert'];

export const alertsApi = {
  list: (query: { status?: string; severity?: string; type?: string } = {}) => {
    const params = new URLSearchParams();
    Object.entries(query).forEach(([key, value]) => { if (value) params.set(key, value); });
    return managementRequest<components['schemas']['AlertPage']>(`/api/v1/alerts?${params}`);
  },
  acknowledge: (id: string) => managementRequest<OperationalAlert>(`/api/v1/alerts/${id}/acknowledge`, { method: 'POST' })
};
