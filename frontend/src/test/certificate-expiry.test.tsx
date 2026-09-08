import { render, screen } from '@testing-library/react';
import dayjs from 'dayjs';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { CertificateExpiryTag, certificateDaysLeft } from '../components/CertificateExpiryTag';

describe('certificate expiry tag', () => {
  beforeEach(() => {
    vi.useFakeTimers();
    vi.setSystemTime(new Date('2026-09-05T12:00:00'));
  });

  const tagClassOf = (text: string) => {
    const node = screen.getByText(text).closest('.ant-tag');
    return node ? node.className : '';
  };

  it('远期证书渲染 success 与剩余天数', () => {
    render(<CertificateExpiryTag notAfter={dayjs().add(60, 'day').toISOString()} />);
    expect(screen.getByText('剩余 60 天')).toBeInTheDocument();
    expect(tagClassOf('剩余 60 天')).toContain('ant-tag-success');
    expect(screen.getByText(dayjs().add(60, 'day').format('YYYY-MM-DD HH:mm:ss'))).toBeInTheDocument();
  });

  it('30 天内渲染 warning', () => {
    render(<CertificateExpiryTag notAfter={dayjs().add(7, 'day').toISOString()} />);
    expect(screen.getByText('剩余 7 天')).toBeInTheDocument();
    expect(tagClassOf('剩余 7 天')).toContain('ant-tag-warning');
  });

  it('已过期渲染 error 与绝对天数', () => {
    render(<CertificateExpiryTag notAfter={dayjs().subtract(3, 'day').toISOString()} />);
    expect(screen.getByText('已过期 3 天')).toBeInTheDocument();
    expect(tagClassOf('已过期 3 天')).toContain('ant-tag-error');
  });

  it('showDate=false 只渲染 Tag', () => {
    render(<CertificateExpiryTag notAfter={dayjs().add(45, 'day').toISOString()} showDate={false} />);
    expect(screen.getByText('剩余 45 天')).toBeInTheDocument();
    expect(screen.queryByText(dayjs().add(45, 'day').format('YYYY-MM-DD HH:mm:ss'))).not.toBeInTheDocument();
  });

  it('certificateDaysLeft 按自然日取整（当天 23:59 仍算剩余 0 天）', () => {
    expect(certificateDaysLeft(dayjs().endOf('day').toISOString())).toBe(0);
    expect(certificateDaysLeft(dayjs().add(1, 'day').startOf('day').toISOString())).toBe(1);
  });
});
