<script lang="ts">
	import type { PageProps } from './$types';
	import PageHeader from '#lib/components/PageHeader.svelte';
	import EmptyState from '#lib/components/EmptyState.svelte';
	import InfoTip from '#lib/components/InfoTip.svelte';
	import { Button } from '#lib/components/ui/button/index.js';
	let { data }: PageProps = $props();
	const buckets = $derived(data.analytics.hourly.map((hour) => hour.requests));
	const max = $derived(Math.max(1, ...buckets));
</script>

<PageHeader title="Overview" path="overview"
	>{#snippet actions()}<Button variant="outline" size="sm" href="/quickstart">Quickstart ↗</Button
		><Button size="sm" href="/playground">Test a route →</Button>{/snippet}</PageHeader
>
<div class="metric-grid">
	{#each [{ label: 'MODELS', value: data.models.length }, { label: 'ENABLED PROVIDERS', value: data.connections.filter((c) => c.enabled).length }, { label: 'ENABLED DEPLOYMENTS', value: data.deployments.filter((d) => d.enabled).length }, { label: 'REQUESTS / 24H', value: data.analytics.requests }] as metric}<div
			class="metric"
		>
			<div class="metric-label flex items-center gap-1">
				{metric.label}{#if metric.label === 'REQUESTS / 24H'}<InfoTip
						text="All recorded requests started in the rolling 24-hour window."
					/>{/if}
			</div>
			<div class="metric-value">{metric.value}</div>
		</div>{/each}
</div>
<div class="metric-grid section">
 {#each [
  { label: 'FAILED / 24H', value: data.analytics.failed },
  { label: 'FALLBACK REQUESTS / 24H', value: data.analytics.fallback_requests },
  { label: 'MEDIAN DURATION', value: data.analytics.p50_duration_ms === null ? '—' : `${data.analytics.p50_duration_ms}ms` },
  { label: 'P95 DURATION', value: data.analytics.p95_duration_ms === null ? '—' : `${data.analytics.p95_duration_ms}ms` },
 ] as metric}<div class="metric"><div class="metric-label">{metric.label}</div><div class="metric-value">{metric.value}</div></div>{/each}
</div>
<p class="muted text-xs mt-3">Duration includes routing and all attempts for completed requests. P95 is the duration at or below which 95% of completed requests finished.</p>
<div class="panel panel-body section">
 <h2 class="mb-2">Recorded cost / 24h</h2>
 <p>${(data.analytics.known_cost_nanos / 1e9).toFixed(6)} known or estimated · {data.analytics.unknown_cost_attempts} provider attempts with incomplete cost.</p>
 <p class="muted text-xs mt-2">This is a partial estimate, not an invoice. Unknown costs are not zero.</p>
</div>
<div class="two-column section">
	<section class="panel">
		<div class="panel-heading">
			<h2>Recent traffic / 24h</h2>
			<InfoTip
				text="All recorded requests in the last 24 hours, grouped into one-hour buckets."
			/>
		</div>
		{#if data.analytics.requests}<div
				class="traffic-chart"
				role="img"
				aria-label="All hourly request counts over the last 24 hours"
			>
				{#each buckets as count}<div class="traffic-track" title={`${count} requests`}>
						<div style={`height:${(count / max) * 100}%`}></div>
					</div>{/each}
			</div>
			<div class="table-footer"><span>24 hours ago</span><span>Now</span></div>{:else}<div
				class="p-3"
			>
				<EmptyState
					title="No traffic"
					description="Send a model request with a router API key to see traffic here."
					href="/quickstart"
					action="Send your first request"
				/>
			</div>{/if}
	</section>
	<section class="panel">
		<div class="panel-heading">
			<h2>Deployment health</h2>
			<InfoTip text="Worker reachability probes, not model performance or inference readiness." />
		</div>
		<div class="panel-body health-list">
			{#each ['healthy', 'unhealthy', 'unknown'] as status}<div>
					<span class="muted">{status}</span><span
						>{Object.values(data.health).filter((h) => h.status === status).length}</span
					>
				</div>{/each}
			<div><a href="/manage/deployments" class="table-link">Inspect deployments →</a></div>
		</div>
	</section>
</div>
<section class="section">
	<div class="section-heading">
		<h2>Recent requests</h2>
		<Button href="/activity" variant="ghost" size="xs">View all →</Button>
	</div>
	{#if data.requests.length}<div class="panel table-wrap">
			<table class="data-table">
				<thead
					><tr
						><th>Request ID</th><th>Task</th><th>Status</th><th>Duration</th><th>Started / UTC</th
						></tr
					></thead
				><tbody
					>{#each data.requests.slice(0, 5) as request}<tr
							><td
								><a class="table-link" href={`/activity?request=${encodeURIComponent(request.id)}`}
									>{request.id}</a
								></td
							><td>{request.task.toUpperCase()}</td><td>{request.status}</td><td
								>{request.finished_at !== null
									? `${request.finished_at - request.started_at}ms`
									: '—'}</td
							><td class="muted">{new Date(request.started_at).toISOString()}</td></tr
						>{/each}</tbody
				>
			</table>
		</div>{:else}<EmptyState
			title="No requests"
			description="Model requests appear here with their status and duration."
			href="/quickstart"
			action="Send your first request"
		/>{/if}
</section>

<style>
	.traffic-chart {
		height: 210px;
		padding: 20px 12px 0;
		display: flex;
		align-items: end;
		gap: 6px;
	}
	.traffic-track {
		height: 100%;
		flex: 1;
		display: flex;
		align-items: end;
		border-bottom: 1px solid var(--border);
	}
	.traffic-track > div {
		width: 100%;
		min-height: 1px;
		background: var(--success);
		opacity: 0.7;
	}
</style>
