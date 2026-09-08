import { Space, Tag } from 'antd';
import dayjs from 'dayjs';
import { formatDateTime } from '../util/time';

// 证书到期倒计时 Tag。30 天是前端可视化分界；后端 config.yaml 的
// certificates.expiry_warning（热重载）独立驱动续期判断，两者刻意不联动。
export const CERTIFICATE_WARNING_DAYS = 30;

export function certificateDaysLeft(notAfter: string): number {
  return dayjs(notAfter).startOf('day').diff(dayjs().startOf('day'), 'day');
}

export function CertificateExpiryTag({ notAfter, showDate = true }: { notAfter: string; showDate?: boolean }) {
  const daysLeft = certificateDaysLeft(notAfter);
  const isExpired = daysLeft < 0;
  const isWarning = !isExpired && daysLeft <= CERTIFICATE_WARNING_DAYS;
  return (
    <Space size={6}>
      {showDate && <span style={{ fontFamily: "'JetBrains Mono', ui-monospace, monospace" }}>{formatDateTime(notAfter)}</span>}
      <Tag color={isExpired ? 'error' : isWarning ? 'warning' : 'success'}>
        {isExpired ? `已过期 ${Math.abs(daysLeft)} 天` : `剩余 ${daysLeft} 天`}
      </Tag>
    </Space>
  );
}
