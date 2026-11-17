export type PlaygroundResult = {
	status: number;
	text?: string;
	error?: string;
	request_id?: string;
};

export async function testChatRoute(
	apiUrl: string,
	key: string,
	model: string,
	prompt: string,
): Promise<PlaygroundResult> {
	if (!/^vc_[a-f0-9]{64}$/.test(key)) return { status: 400, error: 'Enter a router API key from API keys.' };
	if (!/^(policy|deployment):[1-9]\d*$/.test(model)) return { status: 400, error: 'Choose an LLM policy or deployment.' };
	if (!prompt.trim() || prompt.length > 2000) return { status: 400, error: 'Enter a message of 1–2,000 characters.' };
	try {
		const response = await fetch(`${apiUrl}/v1/chat/completions`, {
			method: 'POST',
			headers: { Authorization: `Bearer ${key}`, 'Content-Type': 'application/json' },
			body: JSON.stringify({ model, messages: [{ role: 'user', content: prompt }], max_tokens: 128 }),
			signal: AbortSignal.timeout(60_000),
			redirect: 'error',
		});
		const rawId = response.headers.get('x-request-id') ?? '';
		const request_id = /^[a-f0-9]{32}$/.test(rawId) ? rawId : undefined;
		const reader = response.body?.getReader();
		if (!reader) return { status: 502, error: 'The API returned an empty response.', request_id };
		const chunks: Uint8Array[] = [];
		let size = 0;
		while (true) {
			const { value, done } = await reader.read();
			if (done) break;
			size += value.byteLength;
			if (size > 64 * 1024) {
				await reader.cancel();
				return { status: 502, error: 'The test response exceeded 64 KiB. Use the API directly for larger responses.', request_id };
			}
			chunks.push(value);
		}
		const bytes = new Uint8Array(size);
		let offset = 0;
		for (const chunk of chunks) { bytes.set(chunk, offset); offset += chunk.byteLength; }
		let result;
		try { result = JSON.parse(new TextDecoder().decode(bytes)); }
		catch { return { status: 502, error: 'The API did not return valid JSON.', request_id }; }
		if (!response.ok) {
			const error = typeof result?.error === 'string' ? result.error : 'The route could not complete this request.';
			return { status: response.status, error: error.replaceAll(key, '[redacted]'), request_id };
		}
		const text = result?.choices?.[0]?.message?.content;
		if (typeof text !== 'string' || !text.trim()) return { status: 502, error: 'The model returned no text. Inspect this request in Activity.', request_id };
		return { status: 200, text: text.replaceAll(key, '[redacted]'), request_id };
	} catch {
		return { status: 503, error: 'The route test failed or took longer than one minute. Check the API and Activity before retrying.' };
	}
}
