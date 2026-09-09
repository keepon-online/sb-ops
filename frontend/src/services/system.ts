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

// 备份恢复是仓库首个 multipart 上传端点：managementRequest 只发 JSON，这里
// 照 backupDownload 的 Bearer fetch 先例手动构造 FormData（不要手工设置
// Content-Type，边界由浏览器生成）。返回 200 仅代表恢复已受理，面板随后
// 重启——调用方负责进入「等待面板重启回来」的流程。
async function backupRestoreRequest(url: string, body: FormData): Promise<void> {
  const token = getToken();
  const response = await fetch(url, {
    method: 'POST',
    headers: token ? { Authorization: `Bearer ${token}` } : {},
    body
  });
  const payload = (await response.json().catch(() => null)) as { code?: number; message?: string; requestId?: string } | null;
  if (!response.ok || (payload && payload.code !== 0)) {
    throw new ApiError(payload?.message || '恢复备份失败', response.status, payload?.code ?? -1, payload?.requestId || '');
  }
}

export async function backupRestoreUpload(file: File): Promise<void> {
  const body = new FormData();
  body.append('file', file);
  await backupRestoreRequest('/api/v1/system/restore', body);
}

export async function backupRestoreExisting(name: string): Promise<void> {
  await backupRestoreRequest(`/api/v1/system/backups/${encodeURIComponent(name)}/restore`, new FormData());
}

// waitForPanelRestart 等面板从恢复重启中回来：健康检查免认证。停机与 exec
// 重生之间的不可达窗口可能不足一秒，轮询未必恰好观测到，故优先认「先观察到
// 不可用再 ready」的强信号；超过 graceStillReady 后持续 ready 也接受（重启
// 瞬间完成的兜底，误判最坏是看到旧面板、刷新即可）。超时提示手动刷新。
export function waitForPanelRestart(timeoutMs = 60000): Promise<void> {
  const startedAt = Date.now();
  const graceStillReady = 8000;
  let sawOutage = false;
  return new Promise((resolve, reject) => {
    const probe = async () => {
      let ready = false;
      try {
        const response = await fetch('/health/ready', { cache: 'no-store' });
        ready = response.ok;
      } catch {
        ready = false;
      }
      if (!ready) {
        sawOutage = true;
      } else if (sawOutage || Date.now() - startedAt >= graceStillReady) {
        resolve();
        return;
      }
      if (Date.now() - startedAt >= timeoutMs) {
        reject(new Error('等待面板重启超时，请手动刷新页面确认恢复结果'));
        return;
      }
      window.setTimeout(probe, 750);
    };
    void probe();
  });
}
