<script lang="ts">
	import { enhance } from '$app/forms';
	import type { PageProps } from './$types';
	import PageHeader from '#lib/components/PageHeader.svelte';
	import { Button } from '#lib/components/ui/button/index.js';
	import { Input } from '#lib/components/ui/input/index.js';
	import * as Field from '#lib/components/ui/field/index.js';
	let { data, form }: PageProps = $props();
	let pending = $state(false);
	let routerKey = $state('');
	let model = $state('');
	let prompt = $state('Reply with one short sentence: what is policy routing?');
	const policies = $derived(data.policies.filter((policy) => policy.task === 'llm'));
	const deployments = $derived(data.deployments.filter((deployment) => deployment.enabled && data.models.some((model) => model.id === deployment.model_id && model.task === 'llm')));
</script>

<svelte:head><title>Test a route · Voice Cloud</title></svelte:head>
<PageHeader title="Test a route" path="connect / playground" description="Send one short chat request through a configured LLM route."
	>{#snippet actions()}<Button href="/quickstart" variant="outline" size="sm">Setup guide</Button>{/snippet}</PageHeader>
<div class="two-column">
	<section class="panel panel-body">
		<form method="POST" class="flex flex-col gap-4" use:enhance={() => {
			pending = true;
			return async ({ update }) => {
				routerKey = '';
				try { await update({ reset: false }); } finally { pending = false; }
			};
		}}>
			<Field.Group>
				<Field.Field><Field.Label for="router-key">Router API key</Field.Label>
					<Input id="router-key" name="router_key" type="password" autocomplete="off" maxlength={256} bind:value={routerKey} required />
					<Field.Description>Use a key from <a class="table-link" href="/api-keys">API keys</a>. It is used for this request and cleared afterward.</Field.Description>
				</Field.Field>
				<Field.Field><Field.Label for="route-model">LLM route</Field.Label>
					<select id="route-model" name="model" class="native-select" bind:value={model} required>
						<option value="" disabled>Choose a policy or deployment</option>
						<optgroup label="Policies">{#each policies as policy}<option value={`policy:${policy.id}`}>{policy.name} / policy:{policy.id}</option>{/each}</optgroup>
						<optgroup label="Deployments">{#each deployments as deployment}<option value={`deployment:${deployment.id}`}>{deployment.name} / deployment:{deployment.id}</option>{/each}</optgroup>
					</select>
					<Field.Description>A policy applies routing and fallback. A deployment tests that endpoint directly.</Field.Description>
				</Field.Field>
				<Field.Field><Field.Label for="test-prompt">Message</Field.Label>
					<textarea id="test-prompt" name="prompt" class="native-textarea" rows="5" maxlength={2000} bind:value={prompt} required></textarea>
				</Field.Field>
			</Field.Group>
			<p class="muted text-xs">This sends a real request and may incur provider and decision-service charges. The test requests at most 128 output tokens and waits up to one minute.</p>
			<Button type="submit" disabled={pending || (!policies.length && !deployments.length)} aria-busy={pending}>{pending ? 'Testing route...' : 'Send test request →'}</Button>
		</form>
	</section>
	<section aria-live="polite" class="panel panel-body">
		<h2 class="mb-3">Result</h2>
		{#if form?.error}<p class="negative" role="alert">{form.error}</p>
		{:else if form?.text}<pre class="whitespace-pre-wrap break-words text-xs">{form.text}</pre>
		{:else}<p class="muted">Choose a route, paste a router key, and send a short message. The reply and request link will appear here.</p>{/if}
		{#if form?.request_id}<p class="mt-4"><a class="table-link break-all" href={`/activity?request=${form.request_id}`}>Inspect request {form.request_id} →</a></p>{/if}
		{#if !policies.length && !deployments.length}<p class="mt-4">Create an LLM model, enabled deployment, and policy first. <a href="/quickstart" class="table-link">Follow setup →</a></p>{/if}
		<div class="section"><h3 class="mb-2">Testing speech?</h3><p class="muted text-xs">Quickstart includes transcription and speech commands. Use a voice and output format supported by every deployment in a TTS policy.</p><a href="/quickstart" class="table-link">Open speech examples →</a></div>
	</section>
</div>
