import type { components } from '../generated/management-api';
import { managementRequest } from './http';

export type MonitorStatus = components['schemas']['ManagementMonitorStatus'];

export const monitorApi = {
  status: () => managementRequest<MonitorStatus>('/api/v1/monitor'),
};
