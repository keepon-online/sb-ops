import type { components } from '../generated/management-api';
import { managementRequest } from './http';

export type TrafficSummary = components['schemas']['TrafficSummary'];
export type TrafficActiveUsers = components['schemas']['TrafficActiveUsersResponse'];
export type TrafficProtocolShare = components['schemas']['TrafficProtocolShare'];

export type TrafficQuery = {
  from: string;
  to: string;
  interval?: 'hour' | 'day';
  dimension?: 'user' | 'protocol';
  userId?: string;
  protocolId?: string;
  limit?: number;
};

function queryString(input: TrafficQuery) {
  const query = new URLSearchParams();
  Object.entries(input).forEach(([key, value]) => {
    if (value !== undefined && value !== '') query.set(key, String(value));
  });
  return query.toString();
}

export const trafficApi = {
  series: (input: TrafficQuery) => managementRequest<components['schemas']['TrafficSeriesResponse']>(`/api/v1/traffic/series?${queryString(input)}`),
  summary: (input: TrafficQuery) => managementRequest<TrafficSummary>(`/api/v1/traffic/summary?${queryString(input)}`),
  rankings: (input: TrafficQuery) => managementRequest<components['schemas']['TrafficRankingResponse']>(`/api/v1/traffic/rankings?${queryString(input)}`),
  activeUsers: (input: TrafficQuery) => managementRequest<TrafficActiveUsers>(`/api/v1/traffic/active-users?${queryString(input)}`),
  protocolShares: (input: TrafficQuery) => managementRequest<components['schemas']['TrafficProtocolShareResponse']>(`/api/v1/traffic/protocol-shares?${queryString(input)}`)
};
