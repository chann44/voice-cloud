import { afterEach, expect, test } from 'bun:test';
import { testChatRoute } from '../src/lib/server/playground';

const originalFetch = globalThis.fetch;
const key = `vc_${'a'.repeat(64)}`;
afterEach(() => { globalThis.fetch = originalFetch; });

test('route test validates inputs before network access', async () => {
	globalThis.fetch = (async () => { throw new Error('must not call'); }) as typeof fetch;
	expect((await testChatRoute('http://api.test', 'wrong', 'policy:1', 'hello')).status).toBe(400);
	expect((await testChatRoute('http://api.test', key, 'gpt-model', 'hello')).status).toBe(400);
	expect((await testChatRoute('http://api.test', key, 'policy:1', '')).status).toBe(400);
});

test('route test uses the router key but never returns it', async () => {
	globalThis.fetch = (async (url, init) => {
		expect(String(url)).toBe('http://api.test/v1/chat/completions');
		expect(new Headers(init?.headers).get('authorization')).toBe(`Bearer ${key}`);
		expect(JSON.parse(String(init?.body)).model).toBe('policy:1');
		return Response.json({ choices: [{ message: { content: `Answer ${key}` } }] }, { headers: { 'x-request-id': 'a'.repeat(32) } });
	}) as typeof fetch;
	const result = await testChatRoute('http://api.test', key, 'policy:1', 'hello');
	expect(result.status).toBe(200);
	expect(result.text).toBe('Answer [redacted]');
	expect(JSON.stringify(result)).not.toContain(key);
	expect(result.request_id).toBe('a'.repeat(32));
});

test('route test reports errors safely and bounds response size', async () => {
	globalThis.fetch = (async () => Response.json({ error: `bad ${key}` }, { status: 502 })) as typeof fetch;
	const failed = await testChatRoute('http://api.test', key, 'policy:1', 'hello');
	expect(failed.error).toBe('bad [redacted]');
	globalThis.fetch = (async () => new Response('x'.repeat(65537))) as typeof fetch;
	expect((await testChatRoute('http://api.test', key, 'policy:1', 'hello')).error).toContain('64 KiB');
});
