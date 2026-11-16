import { error } from '@sveltejs/kit';
import { api } from '#lib/server/api.js';
import type { RequestRecord, Attempt } from '#lib/api-types.js';
import type { PageServerLoad } from './$types';

export const load: PageServerLoad = async (event) => {
	const offset = Number(event.url.searchParams.get('offset') || 0);
	if (!Number.isSafeInteger(offset) || offset < 0) error(400, 'Invalid offset');
	const requests = await api<RequestRecord[]>(event, `requests?limit=50&offset=${offset}`);
	const logs = await Promise.all(
		requests.map(async (request) => ({
			request,
			attempts: await api<Attempt[]>(
				event,
				`requests/${encodeURIComponent(request.id)}/attempts`,
			),
		})),
	);
	return { logs, offset };
};
