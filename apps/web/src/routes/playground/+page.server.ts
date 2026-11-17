import { fail } from '@sveltejs/kit';
import { API_URL } from '$app/env/private';
import { testChatRoute } from '#lib/server/playground.js';
import type { Actions } from './$types';

export const actions: Actions = {
	default: async (event) => {
		const form = await event.request.formData();
		const result = await testChatRoute(
			API_URL,
			String(form.get('router_key') ?? '').trim(),
			String(form.get('model') ?? ''),
			String(form.get('prompt') ?? ''),
		);
		return result.status === 200 ? result : fail(result.status, result);
	},
};
