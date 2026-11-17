import { api } from '#lib/server/api.js';
import type { Adapter, Connection, Deployment, Model, Policy } from '#lib/api-types.js';
import type { LayoutServerLoad } from './$types';

export const load: LayoutServerLoad = async (event) => {
	if (!event.locals.user)
		return { user: null, connections: [], models: [], deployments: [], policies: [], adapters: [] };
	const [connections, models, deployments, policies, adapters] = await Promise.all([
		api<Connection[]>(event, 'connections'),
		api<Model[]>(event, 'models'),
		api<Deployment[]>(event, 'deployments'),
		api<Policy[]>(event, 'policies'),
		api<Adapter[]>(event, 'adapters'),
	]);
	const speechModels = models.filter((model) => ['stt','tts','s2s'].includes(model.task));
 const speechDeployments = deployments.filter((deployment) => speechModels.some((model) => model.id === deployment.model_id));
 return { user: event.locals.user, connections: connections.filter((connection) => !['anthropic','cerebras','gemini','baseten','openrouter'].includes(connection.adapter)), models: speechModels, deployments: speechDeployments, policies: policies.filter((policy) => ['stt','tts','s2s'].includes(policy.task)), adapters };
};
