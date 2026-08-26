import type { components } from '../generated/management-api';
import { managementRequest } from './http';

export type UpgradeStatus = components['schemas']['UpgradeStatus'];
export type UpgradeRelease = components['schemas']['UpgradeRelease'];

export const systemApi = {
  upgradeStatus: () => managementRequest<UpgradeStatus>('/api/v1/system/upgrade/status'),
  upgradeCheck: () => managementRequest<UpgradeRelease>('/api/v1/system/upgrade/check', { method: 'POST', body: '{}' }),
  upgradeStart: () => managementRequest<UpgradeStatus>('/api/v1/system/upgrade/start', { method: 'POST', body: '{}' })
};
