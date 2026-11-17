<script lang="ts">
	import type { PageProps } from './$types';
	import { enhance } from '$app/forms';
	import { untrack } from 'svelte';
	import { Button } from '#lib/components/ui/button/index.js';
	import { Input } from '#lib/components/ui/input/index.js';
	import PageHeader from '#lib/components/PageHeader.svelte';
	import ConfigurationTable from '#lib/components/ConfigurationTable.svelte';
	import ModelDetail from '#lib/components/ModelDetail.svelte';
	import * as Tabs from '#lib/components/ui/tabs/index.js';
	let { data, form }: PageProps = $props();
	let task = $state('all'),
		selectedId = $state<number | null>(null),
		open = $state(false);
	const selected = $derived(data.models.find((model) => model.id === selectedId));
	let provider = $state(untrack(() => form && 'connection_id' in form ? String(form.connection_id) : 'all'));
	let catalogQuery = $state('');
	const catalog = $derived(form && 'catalog' in form && form.catalog ? form.catalog : []);
	const catalogProvider = $derived(form && 'connection_id' in form ? form.connection_id : null);
	let pending = $state(false);
	const connection = $derived(data.connections.find((item) => String(item.id) === provider));
	const filteredModels = $derived(data.models.filter((model) =>
		(task === 'all' || model.task === task) && (provider === 'all' || data.deployments.some((deployment) => deployment.model_id === model.id && String(deployment.connection_id) === provider))
	));

</script>

<PageHeader title="Models" path="connect / models" description="Discover cloud and local provider models, register deployments, and use them through the router." />
<section class="panel panel-body mb-5">
	<h2 class="mb-3">Provider models</h2>
	<label for="model-provider" class="block mb-2">Provider</label>
	<select id="model-provider" class="native-select mb-3" bind:value={provider}>
		<option value="all">All registered models</option>
		{#each ['cloud', 'local'] as location}<optgroup label={location === 'cloud' ? 'Cloud providers' : 'Local providers'}>
			{#each data.connections.filter((item) => item.location_kind === location) as item}<option value={String(item.id)}>{item.name} · {item.enabled ? 'enabled' : 'disabled'}</option>{/each}
		</optgroup>{/each}
	</select>
	{#if connection}
		<p class="muted text-xs mb-3">{connection.name} · {connection.adapter} · {connection.base_url}</p>
        {#if connection.adapter_support}<p class="muted text-xs mb-3">{connection.adapter_support.tasks.join(', ')} · {connection.adapter_support.protocol}. {connection.adapter_support.notes}</p>{/if}
		{#if !connection.inference_supported}<p class="mb-3">This provider adapter is not implemented yet. See Implemented adapters under Providers for the current supported tasks.</p>
		{:else if !connection.enabled}<p class="mb-3">Enable this provider to discover and register its models.</p>
		{:else if !connection.credential_ready}<p class="mb-3">Save this provider's API key under Providers, or configure its environment credential reference.</p>
		{:else if data.user?.role === 'admin'}
			{#if connection.adapter_support?.discovery}<form method="POST" action="?/discover" use:enhance={() => {
				pending = true;
				return async ({ update }) => { try { await update({reset: false}); } finally { pending = false; } };
			}}>
				<input type="hidden" name="connection_id" value={connection.id} />
				<Button type="submit" size="sm" disabled={pending}>{pending ? 'Loading models...' : 'Load provider models'}</Button>
			</form>{:else}<p class="muted text-xs">Enter the provider model or deployed model ID below.</p>{/if}
		{/if}
		<a href="/manage/connections" class="table-link block mt-3">Configure providers →</a>
	{:else}<p class="muted text-xs">Choose a cloud or local provider to load its live catalog. Credentials stay in the API service. <a href="/manage/connections" class="table-link">Add a provider →</a></p>{/if}
	{#if form && 'error' in form && form.error}<p role="alert" class="negative mt-3">{form.error}</p>{/if}
	{#if form && 'imported' in form && form.imported}<p role="status" class="positive mt-3">Registered deployment:{form.imported}. Use this selector in your app, or add it to a routing policy. <a class="table-link" href="/manage/deployments">Edit capabilities and pricing →</a></p>{/if}
	{#if connection?.inference_supported && connection.enabled && connection.credential_ready && data.user?.role === 'admin'}
		<div class="mt-4">
			<p class="muted text-xs mb-3">{catalogProvider === connection.id ? catalog.length : 0} models loaded. Only speech tasks are supported. Choose the audio task implemented by the adapter and verify model access with a real request.</p>
			<form method="POST" action="?/import" class="flex flex-col gap-3" use:enhance={() => {
				pending = true;
				return async ({ update }) => { try { await update({reset: false}); } finally { pending = false; } };
			}}>
				<input type="hidden" name="connection_id" value={connection.id} />
				<Input aria-label="Search provider models" placeholder="Filter provider models..." bind:value={catalogQuery} />
				<label for="upstream-model">Provider model</label>
				{#if catalogProvider === connection.id && catalog.length}<select id="upstream-model" name="upstream_model_id" class="native-select" required><option value="" disabled selected>Choose a model</option>{#each catalog.filter((item) => item.id.toLowerCase().includes(catalogQuery.toLowerCase())) as item}<option value={item.id}>{item.id}{item.owned_by ? ` · ${item.owned_by}` : ''}</option>{/each}</select>{:else}<Input id="upstream-model" name="upstream_model_id" placeholder="Exact provider model or deployment ID" required maxlength={500} />{/if}
				<label for="import-task">Task</label>
				<select id="import-task" name="task" class="native-select" required>{#each connection.adapter_support?.tasks ?? [] as supportedTask}<option value={supportedTask}>{supportedTask === 's2s' ? 'Speech to speech' : supportedTask === 'stt' ? 'Speech to text' : 'Text to speech'}</option>{/each}</select>
				<label for="import-capabilities">Verified capabilities (JSON)</label>
				<textarea id="import-capabilities" name="capabilities_json" class="native-textarea" rows="3">{'{}'}</textarea>
				<p class="muted text-xs">Declare the provider's actual limits and features. TTS requires voices and response_formats; audio input limits use max_input_bytes. For example, only if supported: {JSON.stringify({voices:['your-voice-id'],response_formats:['mp3']})}. No features or prices are inferred from model names.</p>
				<Button type="submit" size="sm" disabled={pending}>Register and enable model</Button>
			</form>
		</div>
	{/if}
</section>
<h2 class="mb-3">Registered models</h2>
<Tabs.Root bind:value={task}
	><Tabs.List class="mb-3"
		>{#each [{ id: 'all', label: 'All' }, { id: 'stt', label: 'Speech to text' }, { id: 's2s', label: 'Speech to speech' }, { id: 'tts', label: 'Text to speech' }] as item}<Tabs.Trigger
				value={item.id}>{item.label}</Tabs.Trigger
			>{/each}</Tabs.List
	></Tabs.Root
>
<ConfigurationTable
	resource="models"
	configuration={data}
	records={filteredModels}
	onview={(id) => {
		selectedId = id;
		open = true;
	}}
/>
{#if selected}<ModelDetail
		model={selected}
		configuration={data}
		health={data.health}
		languages={data.languages}
		bind:open
	/>{/if}
