<script lang="ts">
	import type { PageProps } from './$types';
	import { invalidateAll } from '$app/navigation';
	import { toast } from 'svelte-sonner';
	import PageHeader from '#lib/components/PageHeader.svelte';
	import EmptyState from '#lib/components/EmptyState.svelte';
	import { Button } from '#lib/components/ui/button/index.js';
	let { data }: PageProps = $props();
	let refreshing = $state(false);

	async function refresh() {
		refreshing = true;
		try {
			await invalidateAll();
		} catch {
			toast.error('Could not refresh request logs. Try again.');
		} finally {
			refreshing = false;
		}
	}
</script>

<PageHeader
	title="Logs"
	path="observe / logs"
	description="Recorded requests and provider attempts. Refresh for updates. Runtime logs remain in the API and worker process output."
	>{#snippet actions()}<Button
			variant="outline"
			size="sm"
			onclick={refresh}
			disabled={refreshing}
			aria-busy={refreshing}>{refreshing ? 'Refreshing...' : 'Refresh'}</Button
		>{/snippet}</PageHeader
>
{#if data.logs.length}<div class="panel table-wrap">
		<table class="data-table">
			<thead>
				<tr>
					<th>Started / UTC</th><th>Request ID</th><th>Attempt</th><th>Deployment</th><th>Status</th><th>Selection / error</th><th>First output</th><th>Duration</th>
				</tr>
			</thead>
			<tbody>
				{#each data.logs as entry}
					{#each entry.attempts.length ? entry.attempts : [null] as attempt}
						{@const status = attempt?.status ?? entry.request.status}
						{@const start = attempt?.started_at ?? entry.request.started_at}
						{@const finish = attempt ? attempt.finished_at : entry.request.finished_at}
						<tr>
							<td class="muted">{new Date(start).toISOString()}</td>
							<td><a class="table-link" href={`/activity?request=${encodeURIComponent(entry.request.id)}`}>{entry.request.id}</a></td>
							<td>{attempt?.attempt_number ?? '—'}</td>
							<td>{attempt ? (data.deployments.find((d) => d.id === attempt.deployment_id)?.name ?? `Deployment #${attempt.deployment_id}`) : '—'}</td>
							<td class={status === 'succeeded' ? 'positive' : status === 'failed' ? 'negative' : 'muted'}>{status}</td>
							<td>{attempt?.selection_reason ?? 'No deployment attempted'}<span class="muted"> / {attempt?.error_code ?? entry.request.error_code ?? '—'}</span></td>
							<td>{attempt?.first_output_at != null ? `${attempt.first_output_at - start}ms` : '—'}</td>
							<td>{finish !== null ? `${finish - start}ms` : '—'}</td>
						</tr>
					{/each}
				{/each}
			</tbody>
		</table>
	</div>{:else}<EmptyState
		title="No request logs"
		description="Send a model request with a router API key to record requests and provider attempts."
		href="/quickstart"
		action="Send your first request"
	/>{/if}
<div class="flex items-center justify-between mt-3">
	<span class="eyebrow">PAGE {Math.floor(data.offset / 50) + 1} / {data.logs.length} REQUESTS</span>
	<div class="actions">
		<Button
			href={`/logs?offset=${Math.max(0, data.offset - 50)}`}
			size="xs"
			variant="outline"
			disabled={data.offset === 0}>← Previous</Button
		><Button
			href={`/logs?offset=${data.offset + 50}`}
			size="xs"
			variant="outline"
			disabled={data.logs.length < 50}>Next →</Button
		>
	</div>
</div>
