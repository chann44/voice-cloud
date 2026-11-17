import { api } from '#lib/server/api.js';
import type { Analytics, Health, RequestRecord } from '#lib/api-types.js';
import type { PageServerLoad } from './$types';
export const load: PageServerLoad = async (event) => {
 const data = await event.parent();
 const [requests, analytics, results] = await Promise.all([
  api<RequestRecord[]>(event, 'requests?limit=5'),
  api<Analytics>(event, 'analytics'),
  Promise.all(data.deployments.map(async (deployment) => [deployment.id, await api<Health>(event, `deployments/${deployment.id}/health`)] as const)),
 ]);
 const health: Record<number, Health> = Object.fromEntries(results);
 return { requests, analytics, health };
};
