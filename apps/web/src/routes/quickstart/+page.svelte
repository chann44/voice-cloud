<script lang="ts">
	import type { PageProps } from './$types';
	let { data }: PageProps = $props();
	import InfoTip from '#lib/components/InfoTip.svelte';
	import PageHeader from '#lib/components/PageHeader.svelte';
	import CodeBlock from '#lib/components/CodeBlock.svelte';
	import { Button } from '#lib/components/ui/button/index.js';
	import * as Tabs from '#lib/components/ui/tabs/index.js';
	let language = $state('curl');
	const examples: Record<string, string> = {
		curl: '# Set VOICE_CLOUD_API_KEY to a router key from API keys\n# Replace policy:1 with your LLM policy ID\ncurl http://localhost:8080/v1/chat/completions \\\n  -H "Authorization: Bearer $VOICE_CLOUD_API_KEY" \\\n  -H "Content-Type: application/json" \\\n  -d \'{"model":"policy:1","messages":[{"role":"user","content":"Hello!"}]}\'',
		python:
			'import json\nimport os\nfrom urllib.request import Request, urlopen\n\nrequest = Request(\n    "http://localhost:8080/v1/chat/completions",\n    data=json.dumps({\n        "model": "policy:1",  # Your LLM policy ID\n        "messages": [{"role": "user", "content": "Hello!"}],\n    }).encode(),\n    headers={\n        "Authorization": f"Bearer {os.environ[\'VOICE_CLOUD_API_KEY\']}",\n        "Content-Type": "application/json",\n    },\n)\nwith urlopen(request) as response:\n    print(json.load(response))',
		typescript:
			'// Run on the server; keep the router key private\nconst response = await fetch(\n  "http://localhost:8080/v1/chat/completions",\n  {\n    method: "POST",\n    headers: {\n      Authorization: `Bearer ${process.env.VOICE_CLOUD_API_KEY}`,\n      "Content-Type": "application/json",\n    },\n    body: JSON.stringify({\n      model: "policy:1", // Your LLM policy ID\n      messages: [{ role: "user", content: "Hello!" }],\n    }),\n  }\n);\nif (!response.ok) throw new Error(`HTTP ${response.status}`);\nconsole.log(await response.json());',
	};
	const transcription = '# Replace policy:2 with your STT policy ID\ncurl http://localhost:8080/v1/audio/transcriptions \\\n  -H "Authorization: Bearer $VOICE_CLOUD_API_KEY" \\\n  -F "model=policy:2" \\\n  -F "file=@speech.wav"';
	const speech = '# Replace policy:3 and the voice with values for your TTS provider\ncurl --fail-with-body http://localhost:8080/v1/audio/speech \\\n  -H "Authorization: Bearer $VOICE_CLOUD_API_KEY" \\\n  -H "Content-Type: application/json" \\\n  -d \'{"model":"policy:3","input":"Hello from Voice Cloud","voice":"alloy"}\' \\\n  --output speech.mp3';
	const prompt =
		'# Voice Cloud — context for your coding agent\n\nVoice Cloud is a self-hosted voice model router.\nThe backend is Go, with SQLite and a maintenance worker.\nThe console uses Svelte, TypeScript, and shadcn-svelte.\n\nUse http://localhost:8080/v1. Management calls use a user session token.\nInference calls use a router key from API keys. Keep keys on the server.\n\nConfigure an OpenAI-compatible connection, model, deployment, and routing policy.\nProvider credential_ref names an environment variable in the API process.\nPOST /v1/chat/completions accepts JSON messages.\nPOST /v1/audio/speech accepts JSON input and voice.\nPOST /v1/audio/transcriptions accepts multipart file and model fields.\nUse model "policy:<id>" or "deployment:<id>".\nOmit model to use the router key\'s default policy.\nPolicies must match the endpoint task. Provider-specific protocols require adapters.\nRequest outcomes, attempts, and usage appear under Activity.';
</script>

<PageHeader
	title="Quickstart"
	path="connect / quickstart"
	description="Configure a route and send your first model request."
	>{#snippet actions()}<Button href="/api-keys" size="sm">Create API key +</Button
		>{/snippet}</PageHeader
>
<section class="panel panel-body mb-4" aria-labelledby="setup-checklist">
 <h2 id="setup-checklist" class="mb-3">Your installation</h2>
 <ol class="flex flex-col gap-3">
  <li><a class="table-link" href="/manage/connections">1. Connect a provider →</a><p class="muted text-xs">{data.connections.filter((connection) => connection.enabled).length} enabled connections. Use an OpenAI-compatible endpoint and set its provider-key environment variable in the API process.</p></li>
  <li><a class="table-link" href="/models">2. Register a model →</a><p class="muted text-xs">{data.models.length} models. Choose STT, TTS, or LLM to match the provider endpoint.</p></li>
  <li><a class="table-link" href="/manage/deployments">3. Add a deployment →</a><p class="muted text-xs">{data.deployments.filter((deployment) => deployment.enabled).length} enabled deployments. Set the upstream model ID and declare the capabilities your app requires.</p></li>
  <li><a class="table-link" href="/manage/policies">4. Configure a policy and targets →</a><p class="muted text-xs">{data.policies.length} policies. Add compatible deployments, set their priority, and enable fallback if needed.</p></li>
  <li><a class="table-link" href="/api-keys">5. Create a router key →</a><p class="muted text-xs">Keep the key on your app's backend. Management session tokens cannot run model requests.</p></li>
  <li><a class="table-link" href="/playground">6. Send a real test request →</a><p class="muted text-xs">Verify chat in the console, or use the speech commands below. Inspect the resulting request and attempts in Activity.</p></li>
 </ol>
 <p class="muted text-xs mt-4">Counts show saved configuration, not proof of a working route. Provider credentials, model access, voices, and formats are verified only by a real request. Seeded native-provider entries remain disabled until a supported adapter is configured.</p>
</section>
<div class="two-column">
	<section>
		<h2 class="mb-4">Setup</h2>
		{#each [{ title: 'Start the backend', text: 'Run the API and maintenance worker with your SQLite database.' }, { title: 'Configure your providers', text: 'Add an OpenAI-compatible connection and its model deployments. Set credential_ref to the environment variable holding the provider key in the API process.' }, { title: 'Define your routes', text: 'Add deployments to a policy for each task, then create a router API key. Each policy and deployment has an ID in its configuration page.' }] as step, i}<div
				class="step"
			>
				<span class="step-number">0{i + 1}</span>
				<div class="flex items-center gap-1">
					<h3>{step.title}</h3>
					<InfoTip text={step.text} label={`About ${step.title.toLowerCase()}`} />
				</div>
			</div>{/each}
	</section>
	<CodeBlock
		title="terminal / start backend"
		code={'# From the repository root\n# Open the web console to create your first account\n# Development: API and worker in separate terminals\nmake api-run\nmake worker-run'}
	/>
</div>
<details class="section panel">
	<summary class="panel-heading">Coding agent context <span class="muted">+</span></summary
	><CodeBlock title="AGENT_CONTEXT.md" code={prompt} />
</details>
<section class="section">
	<div class="section-heading">
		<h2>Send a chat request</h2>
		<InfoTip
			text="Set VOICE_CLOUD_API_KEY to your router key. Use policy:<id> for routing, deployment:<id> for a specific deployment, or omit model to use the key's default policy. The route must support the endpoint's task."
			label="About these examples"
		/>
	</div>
	<Tabs.Root bind:value={language}
		><Tabs.List class="mb-2"
			><Tabs.Trigger value="curl">cURL</Tabs.Trigger><Tabs.Trigger value="python"
				>Python</Tabs.Trigger
			><Tabs.Trigger value="typescript">TypeScript</Tabs.Trigger></Tabs.List
		>{#each ['curl', 'python', 'typescript'] as lang}<Tabs.Content value={lang}
				><CodeBlock
					title={lang === 'curl'
						? 'terminal / chat completion'
						: lang === 'python'
							? 'connect.py'
							: 'connect.ts'}
					code={examples[lang]}
				/></Tabs.Content
			>{/each}</Tabs.Root
	>
</section>
<section class="section">
	<div class="section-heading"><h2>Transcribe audio</h2></div>
	<CodeBlock title="terminal / transcription" code={transcription} />
</section>
<section class="section">
	<div class="section-heading"><h2>Generate speech</h2></div>
	<CodeBlock title="terminal / speech" code={speech} />
</section>
