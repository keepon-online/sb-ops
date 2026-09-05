import type { components } from '../generated/management-api';
import { ApiError, getToken, managementRequest } from './http';

export type UpgradeStatus = components['schemas']['UpgradeStatus'];
export type UpgradeRelease = components['schemas']['UpgradeRelease'];
export type BackupFile = components['schemas']['BackupFile'];

export const systemApi = {
  upgradeStatus: () => managementRequest<UpgradeStatus>('/api/v1/system/upgrade/status'),
  upgradeCheck: () => managementRequest<UpgradeRelease>('/api/v1/system/upgrade/check', { method: 'POST', body: '{}' }),
  upgradeStart: () => managementRequest<UpgradeStatus>('/api/v1/system/upgrade/start', { method: 'POST', body: '{}' }),
  backupList: () => managementRequest<{ items: BackupFile[] }>('/api/v1/system/backups'),
  backupCreate: () => managementRequest<BackupFile>('/api/v1/system/backups', { method: 'POST', body: '{}' }),
  backupDelete: (name: string) => managementRequest<void>(`/api/v1/system/backups/${encodeURIComponent(name)}`, { method: 'DELETE' })
};

// 备份下载是二进制流（信封豁免），managementRequest 只解析 JSON 信封用不上；
// 认证只认 Bearer 头（无 cookie），浏览器直链带不了凭据，故经 fetch 取 blob
// 后以临时 <a download> 本地落盘。
export async function backupDownload(name: string): Promise<void> {
  const token = getToken();
  const response = await fetch(`/api/v1/system/backups/${encodeURIComponent(name)}/download`, {
    headers: token ? { Authorization: `Bearer ${token}` } : {}
  });
  if (!response.ok) {
    let message = '下载备份失败';
    if ((response.headers.get('Content-Type') || '').includes('json')) {
      const payload = (await response.json()) as { message?: string };
      if (payload.message) message = payload.message;
    }
    throw new ApiError(message, response.status, -1, response.headers.get('X-Request-ID') || '');
  }
  const blob = await response.blob();
  const url = URL.createObjectURL(blob);
  try {
    const anchor = document.createElement('a');
    anchor.href = url;
    anchor.download = name;
    anchor.click();
  } finally {
    URL.revokeObjectURL(url);
  }
}
