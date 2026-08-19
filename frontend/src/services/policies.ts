import type { components } from '../generated/management-api';
import { managementRequest } from './http';

export type ResourcePolicy = components['schemas']['ResourcePolicy'];
export type ResourcePolicyRule = components['schemas']['ResourcePolicyRule'];
export type ResourcePolicyConditions = components['schemas']['ResourcePolicyConditions'];
export type ResourcePolicyDryRunResult = components['schemas']['ResourcePolicyDryRunResult'];
export type ResourcePolicyValidationResult = components['schemas']['ResourcePolicyValidationResult'];

const policyPath = (id = '', suffix = '') => `/api/v1/policies${id ? `/${encodeURIComponent(id)}` : ''}${suffix}`;

export const policiesApi = {
  list: () => managementRequest<components['schemas']['ResourcePoliciesResponse']>(policyPath()),
  listRuleSets: () => managementRequest<components['schemas']['PolicyRuleSetsResponse']>(policyPath('', '/rule-sets')),
  create: (input: components['schemas']['ResourceCreatePolicyRequest']) => managementRequest<ResourcePolicy>(policyPath(), { method: 'POST', body: JSON.stringify(input) }),
  update: (id: string, input: components['schemas']['ResourceUpdatePolicyRequest']) => managementRequest<ResourcePolicy>(policyPath(id), { method: 'PATCH', body: JSON.stringify(input) }),
  remove: (id: string) => managementRequest<void>(policyPath(id), { method: 'DELETE' }),
  clone: (id: string, name: string) => managementRequest<ResourcePolicy>(policyPath(id, '/clone'), { method: 'POST', body: JSON.stringify({ name }) }),
  listRules: (id: string) => managementRequest<components['schemas']['ResourcePolicyRulesResponse']>(policyPath(id, '/rules')),
  createRule: (id: string, input: components['schemas']['ResourceCreatePolicyRuleRequest']) => managementRequest<ResourcePolicyRule>(policyPath(id, '/rules'), { method: 'POST', body: JSON.stringify(input) }),
  updateRule: (policyID: string, ruleID: string, input: components['schemas']['ResourceUpdatePolicyRuleRequest']) => managementRequest<ResourcePolicyRule>(policyPath(policyID, `/rules/${encodeURIComponent(ruleID)}`), { method: 'PATCH', body: JSON.stringify(input) }),
  removeRule: (policyID: string, ruleID: string) => managementRequest<void>(policyPath(policyID, `/rules/${encodeURIComponent(ruleID)}`), { method: 'DELETE' }),
  orderRules: (id: string, ids: string[]) => managementRequest<components['schemas']['ResourcePolicyRulesResponse']>(policyPath(id, '/rules/order'), { method: 'PUT', body: JSON.stringify({ ids }) }),
  dryRun: (id: string, input: components['schemas']['ResourcePolicyDryRunRequest']) => managementRequest<ResourcePolicyDryRunResult>(policyPath(id, '/dry-run'), { method: 'POST', body: JSON.stringify(input) }),
  validate: (id: string) => managementRequest<ResourcePolicyValidationResult>(policyPath(id, '/validate'), { method: 'POST', body: '{}' })
};
