import { useQuery } from '@tanstack/react-query';
import { identityApi } from '../services/identity';
import { getToken } from '../services/http';

export const identityQueryKey = ['identity', 'me'] as const;

export function useIdentity() {
  return useQuery({
    queryKey: identityQueryKey,
    queryFn: identityApi.me,
    enabled: Boolean(getToken()),
    retry: false
  });
}
