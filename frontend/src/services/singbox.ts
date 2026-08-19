import type { components } from '../generated/management-api';
import { managementRequest } from './http';

export type SingBoxConfig = components['schemas']['SingBoxConfig'];
export type SingBoxOutbound = components['schemas']['SingBoxOutbound'];
export type SingBoxRouteConditions = components['schemas']['SingBoxRouteConditions'];
export type SingBoxRouteRule = components['schemas']['SingBoxRouteRule'];
export type SingBoxRuleSet = components['schemas']['SingBoxRuleSet'];
export type SingBoxDryRunResult = components['schemas']['SingBoxDryRunResult'];
export type SingBoxValidationResult = components['schemas']['SingBoxValidationResult'];

export type SingBoxRuntimeStatus = components['schemas']['SingBoxRuntimeStatus'];
export type SingBoxCheckResult = components['schemas']['SingBoxCheckResult'];
export type SingBoxConnectionsResult = components['schemas']['SingBoxConnectionsResult'];
export type SingBoxConnection = components['schemas']['SingBoxConnection'];
export type ArgoTunnel = components['schemas']['ArgoTunnel'];
export type WarpOutboundSettings = components['schemas']['WarpOutboundSettings'];
export type WarpRegistrationResult = components['schemas']['WarpRegistrationResult'];

const singBoxPath = (suffix = '') => `/api/v1/singbox${suffix}`;

export const singBoxApi = {
  getConfig: () => managementRequest<SingBoxConfig>(singBoxPath()),
  getRuntime: () => managementRequest<SingBoxRuntimeStatus>(singBoxPath('/runtime')),
  checkRuntime: () => managementRequest<SingBoxCheckResult>(singBoxPath('/check'), { method: 'POST', body: '{}' }),
  reconcileRuntime: () => managementRequest<components['schemas']['SingBoxSyncResult']>(singBoxPath('/reconcile'), { method: 'POST', body: '{}' }),
  restartRuntime: () => managementRequest<void>(singBoxPath('/restart'), { method: 'POST', body: '{}' }),
  connections: () => managementRequest<SingBoxConnectionsResult>(singBoxPath('/connections')),
  closeConnection: (id: string) => managementRequest<void>(singBoxPath(`/connections/${encodeURIComponent(id)}`), { method: 'DELETE' }),
  updateConfig: (input: components['schemas']['SingBoxConfigUpdateRequest']) => managementRequest<SingBoxConfig>(singBoxPath(), { method: 'PATCH', body: JSON.stringify(input) }),
  listOutbounds: () => managementRequest<components['schemas']['SingBoxOutboundsResponse']>(singBoxPath('/outbounds')),
  createOutbound: (input: components['schemas']['SingBoxCreateOutboundRequest']) => managementRequest<SingBoxOutbound>(singBoxPath('/outbounds'), { method: 'POST', body: JSON.stringify(input) }),
  updateOutbound: (id: string, input: components['schemas']['SingBoxUpdateOutboundRequest']) => managementRequest<SingBoxOutbound>(singBoxPath(`/outbounds/${encodeURIComponent(id)}`), { method: 'PATCH', body: JSON.stringify(input) }),
  removeOutbound: (id: string) => managementRequest<void>(singBoxPath(`/outbounds/${encodeURIComponent(id)}`), { method: 'DELETE' }),
  listRouteRules: () => managementRequest<components['schemas']['SingBoxRouteRulesResponse']>(singBoxPath('/route-rules')),
  createRouteRule: (input: components['schemas']['SingBoxCreateRouteRuleRequest']) => managementRequest<SingBoxRouteRule>(singBoxPath('/route-rules'), { method: 'POST', body: JSON.stringify(input) }),
  updateRouteRule: (id: string, input: components['schemas']['SingBoxUpdateRouteRuleRequest']) => managementRequest<SingBoxRouteRule>(singBoxPath(`/route-rules/${encodeURIComponent(id)}`), { method: 'PATCH', body: JSON.stringify(input) }),
  removeRouteRule: (id: string) => managementRequest<void>(singBoxPath(`/route-rules/${encodeURIComponent(id)}`), { method: 'DELETE' }),
  orderRouteRules: (ids: string[]) => managementRequest<components['schemas']['SingBoxRouteRulesResponse']>(singBoxPath('/route-rules/order'), { method: 'PUT', body: JSON.stringify({ ids }) }),
  listRuleSets: () => managementRequest<components['schemas']['SingBoxRuleSetsResponse']>(singBoxPath('/rule-sets')),
  createRuleSet: (input: components['schemas']['SingBoxCreateRuleSetRequest']) => managementRequest<SingBoxRuleSet>(singBoxPath('/rule-sets'), { method: 'POST', body: JSON.stringify(input) }),
  updateRuleSet: (id: string, input: components['schemas']['SingBoxUpdateRuleSetRequest']) => managementRequest<SingBoxRuleSet>(singBoxPath(`/rule-sets/${encodeURIComponent(id)}`), { method: 'PATCH', body: JSON.stringify(input) }),
  removeRuleSet: (id: string) => managementRequest<void>(singBoxPath(`/rule-sets/${encodeURIComponent(id)}`), { method: 'DELETE' }),
  dryRun: (input: components['schemas']['SingBoxDryRunRequest']) => managementRequest<SingBoxDryRunResult>(singBoxPath('/route-rules/dry-run'), { method: 'POST', body: JSON.stringify(input) }),
  validate: () => managementRequest<SingBoxValidationResult>(singBoxPath('/validate'), { method: 'POST', body: '{}' }),
  registerWarp: () => managementRequest<WarpRegistrationResult>(singBoxPath('/outbounds/warp/register'), { method: 'POST', body: '{}' }),
  applyAITemplate: (outboundTag: string) => managementRequest<SingBoxRouteRule>(singBoxPath('/route-rules/ai-template'), { method: 'POST', body: JSON.stringify({ outboundTag }) }),
  listArgos: () => managementRequest<components['schemas']['ArgoTunnelsResponse']>(singBoxPath('/argos')),
  createArgo: (input: components['schemas']['ArgoCreateTunnelRequest']) => managementRequest<ArgoTunnel>(singBoxPath('/argos'), { method: 'POST', body: JSON.stringify(input) }),
  updateArgo: (id: string, input: components['schemas']['ArgoUpdateTunnelRequest']) => managementRequest<ArgoTunnel>(singBoxPath(`/argos/${encodeURIComponent(id)}`), { method: 'PATCH', body: JSON.stringify(input) }),
  removeArgo: (id: string) => managementRequest<void>(singBoxPath(`/argos/${encodeURIComponent(id)}`), { method: 'DELETE' }),
  restartArgo: (id: string) => managementRequest<void>(singBoxPath(`/argos/${encodeURIComponent(id)}/restart`), { method: 'POST', body: '{}' })
};
