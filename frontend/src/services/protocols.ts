import type { components } from '../generated/management-api';
import { managementRequest } from './http';

export type ResourceProtocol = components['schemas']['ResourceProtocol'];
export type ResourceProtocolType = components['schemas']['ResourceProtocolType'];
export type ResourceProtocolSchema = components['schemas']['ResourceProtocolSchema'];
export type ResourceProtocolWrite = components['schemas']['ResourceCreateProtocolRequest'];
export type ResourceCertificate = components['schemas']['ResourceCertificate'];
export type ProtocolRuntimeStatus = components['schemas']['ProtocolRuntimeStatusResult'];
export type ProbeResult = components['schemas']['ProbeResult'];

const resourcePath = (resource: string, id: string, suffix = '') => `/api/v1/${resource}/${encodeURIComponent(id)}${suffix}`;

export const protocolsApi = {
  schema: (type: ResourceProtocolType) => managementRequest<ResourceProtocolSchema>(`/api/v1/protocol-schemas/${type}`),
  suggestPort: (type: ResourceProtocolType) => managementRequest<components['schemas']['ResourceProtocolPortSuggestion']>('/api/v1/protocols/suggest-port', { method: 'POST', body: JSON.stringify({ type }) }),
  list: () => managementRequest<components['schemas']['ResourceProtocolsResponse']>('/api/v1/protocols'),
  runtimeStatus: () => managementRequest<ProtocolRuntimeStatus>('/api/v1/protocols/runtime-status'),
  probeResults: () => managementRequest<components['schemas']['StoredProbeResults']>('/api/v1/protocols/probe-results'),
  runProbe: (protocolId?: string) => managementRequest<ProbeResult>(`/api/v1/protocols/probe${protocolId ? `?protocolId=${encodeURIComponent(protocolId)}` : ''}`, { method: 'POST', body: '{}' }),
  create: (input: ResourceProtocolWrite) => managementRequest<ResourceProtocol>('/api/v1/protocols', { method: 'POST', body: JSON.stringify(input) }),
  update: (id: string, input: components['schemas']['ResourceUpdateProtocolRequest']) => managementRequest<ResourceProtocol>(resourcePath('protocols', id), { method: 'PATCH', body: JSON.stringify(input) }),
  remove: (id: string) => managementRequest<void>(resourcePath('protocols', id), { method: 'DELETE' }),
  clone: (id: string, input: components['schemas']['ResourceCloneProtocolRequest']) => managementRequest<ResourceProtocol>(resourcePath('protocols', id, '/clone'), { method: 'POST', body: JSON.stringify(input) }),
  rotateSecret: (id: string) => managementRequest<components['schemas']['ResourceProtocolSecretRotation']>(resourcePath('protocols', id, '/rotate-secret'), { method: 'POST', body: '{}' })
};

export const certificatesApi = {
  list: () => managementRequest<components['schemas']['ResourceCertificatesResponse']>('/api/v1/certificates'),
  createSelfSigned: (input: components['schemas']['ResourceCreateSelfSignedCertificateRequest']) =>
    managementRequest<ResourceCertificate>('/api/v1/certificates/self-signed', { method: 'POST', body: JSON.stringify(input) }),
  issueAcme: (input: components['schemas']['ResourceCreateAcmeCertificateRequest']) =>
    managementRequest<ResourceCertificate>('/api/v1/certificates/acme', { method: 'POST', body: JSON.stringify(input) }),
  renewAcme: (id: string) =>
    managementRequest<ResourceCertificate>(resourcePath('certificates', id, '/renew'), { method: 'POST', body: '{}' }),
  regenerateSelfSigned: (id: string, input: components['schemas']['ResourceRegenerateSelfSignedCertificateRequest']) =>
    managementRequest<ResourceCertificate>(resourcePath('certificates', id, '/regenerate'), { method: 'POST', body: JSON.stringify(input) }),
  remove: (id: string) => managementRequest<void>(resourcePath('certificates', id), { method: 'DELETE' })
};
