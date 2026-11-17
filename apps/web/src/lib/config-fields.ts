export type ConfigField = {
	key: string;
	label: string;
	kind?: 'json' | 'boolean' | 'number' | 'select' | 'password';
	options?: string[];
	immutable?: boolean;
	optional?: boolean;
	initial?: string;
	help?: string;
};
export const configFields: Record<string, ConfigField[]> = {
	connections: [
		{ key: 'name', label: 'Name' },
		{
			key: 'adapter',
			label: 'Adapter',
			kind: 'select',
			help: 'Choose an implemented adapter. Tasks and protocol limits are listed below the provider table. Model capabilities remain deployment-specific.',
		},
		{ key: 'location_kind', label: 'Location', kind: 'select', options: ['cloud', 'local'] },
		{ key: 'base_url', label: 'Base URL' },
		{ key: 'api_key', label: 'Provider API key', kind: 'password', optional: true,
			help: 'Encrypted on the server and never shown again. Leave blank to keep the saved key. Enter a new key to replace it. Saving a key clears the environment credential reference.' },
		{ key: 'remove_api_key', label: 'Remove saved API key and environment credential reference', kind: 'boolean', initial: 'false' },
		{
			key: 'credential_ref',
			label: 'Environment credential reference (optional)',
			optional: true,
			help: 'Advanced alternative: API environment variable name, used when no saved key exists. Put the actual key in Provider API key instead.',
		},
		{ key: 'enabled', label: 'Enabled', kind: 'boolean', initial: 'true' },
	],
	models: [
		{ key: 'name', label: 'Name' },
		{ key: 'publisher', label: 'Publisher' },
		{ key: 'task', label: 'Task', kind: 'select', options: ['stt', 'tts', 's2s'], immutable: true },
		{ key: 'version', label: 'Version', optional: true },
	],
	deployments: [
		{ key: 'name', label: 'Name' },
		{ key: 'model_id', label: 'Model', kind: 'select', immutable: true },
		{ key: 'connection_id', label: 'Provider connection', kind: 'select', immutable: true },
		{ key: 'upstream_model_id', label: 'Upstream model ID', immutable: true },
		{ key: 'enabled', label: 'Enabled', kind: 'boolean', initial: 'true' },
		{
			key: 'capabilities_json',
			label: 'Capabilities',
			kind: 'json',
			help: 'Declare verified audio input limits, supported voices, output formats, and streaming support. Do not infer capabilities from a model name.',
		},
		{ key: 'pricing_json', label: 'Pricing', kind: 'json' },
		{
			key: 'settings_json',
			label: 'Settings',
			kind: 'json',
			help: 'For custom/local probes, use {"healthcheck":{"url":"http://your-host/health"}}. The URL must match the connection origin.',
		},
	],
	policies: [
		{ key: 'name', label: 'Name' },
		{ key: 'task', label: 'Task', kind: 'select', options: ['stt', 'tts', 's2s'], immutable: true },
		{
			key: 'objective',
			label: 'Objective',
			kind: 'select',
			options: ['balanced', 'latency', 'cost', 'quality'],
		},
		{ key: 'fallback_enabled', label: 'Enable fallback', kind: 'boolean', initial: 'true' },
		{ key: 'max_attempts', label: 'Maximum attempts', kind: 'number', initial: '2' },
		{ key: 'constraints_json', label: 'Constraints', kind: 'json' },
	],
};
