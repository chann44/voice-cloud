import { error } from '@sveltejs/kit';
import { api, actionError } from '#lib/server/api.js';
import { configurationActions } from '#lib/server/configuration.js';
import type { Health, Language } from '#lib/api-types.js';
import type { Actions, PageServerLoad } from './$types';
export const load: PageServerLoad = async (event) => {
	const data = await event.parent(),
		health: Record<number, Health> = {},
		languages: Record<number, Language[]> = {};
	for (const deployment of data.deployments) {
		health[deployment.id] = await api<Health>(event, `deployments/${deployment.id}/health`);
		languages[deployment.id] = await api<Language[]>(
			event,
			`deployments/${deployment.id}/languages`,
		);
	}
	return { health, languages };
};
export const actions = {
	...configurationActions(() => 'models'),
	discover: async (event) => {
		try {
			const form = await event.request.formData();
			const connection = String(form.get('connection_id') ?? '');
			if (!/^[1-9]\d*$/.test(connection)) error(400, 'Choose a provider');
			const catalog = await api<{id: string; owned_by: string}[]>(event, `connections/${connection}/discover`, {method: 'POST'});
			return {catalog, connection_id: Number(connection)};
		} catch (problem) { return actionError(problem); }
	},
	import: async (event) => {
		try {
			const form = await event.request.formData();
			const connection = String(form.get('connection_id') ?? '');
			if (!/^[1-9]\d*$/.test(connection)) error(400, 'Choose a provider');
			let capabilities;
			try { capabilities = JSON.parse(String(form.get('capabilities_json') || '{}')); }
			catch { error(400, 'Capabilities must be a JSON object'); }
			const deployment = await api<{id: number}>(event, `connections/${connection}/models`, {
				method: 'POST', body: JSON.stringify({upstream_model_id: String(form.get('upstream_model_id') ?? ''), task: String(form.get('task') ?? ''), capabilities_json: capabilities})
			});
			return {imported: deployment.id, connection_id: Number(connection)};
		} catch (problem) { return actionError(problem); }
	},
} satisfies Actions;
