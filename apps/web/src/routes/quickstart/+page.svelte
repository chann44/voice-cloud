<script lang="ts">
 import type {PageProps} from './$types';
 import PageHeader from '#lib/components/PageHeader.svelte';
 import CodeBlock from '#lib/components/CodeBlock.svelte';
 let {data}:PageProps=$props();
 const tts=`curl http://localhost:8080/v1/audio/speech \\\n  -H "Authorization: Bearer $VOICE_CLOUD_API_KEY" \\\n  -H 'Content-Type: application/json' \\\n  -d '{"model":"policy:1","input":"Hello from Voice Cloud","voice":"your-voice-id","response_format":"mp3"}' \\\n  --output speech.mp3`;
 const stt=`curl http://localhost:8080/v1/audio/transcriptions \\\n  -H "Authorization: Bearer $VOICE_CLOUD_API_KEY" \\\n  -F model=policy:2 -F file=@recording.wav -F response_format=json`;
 const s2s=`curl http://localhost:8080/v1/audio/speech-to-speech \\\n  -H "Authorization: Bearer $VOICE_CLOUD_API_KEY" \\\n  -F model=policy:3 -F file=@recording.wav -F voice=your-voice-id \\\n  -F response_format=mp3 --output converted.mp3`;
</script>
<PageHeader title="Quickstart" path="connect / quickstart" description="Route text-to-speech, speech-to-text, and native speech-to-speech models." />
<section class="panel panel-body mb-5"><h2 class="mb-3">Connect a speech model</h2>
 <ol class="list-decimal ml-5 space-y-3">
  <li><a class="table-link" href="/manage/connections">Connect a provider</a>. Choose an implemented speech adapter and save its API key. {data.connections.filter((connection)=>connection.enabled).length} providers enabled.</li>
  <li><a class="table-link" href="/models">Register a model</a>. Select STT, TTS, or S2S to match the actual audio endpoint. Declare supported voices, formats, input limits, and translation target languages.</li>
  <li><a class="table-link" href="/manage/policies">Create a speech routing policy</a>. Add compatible deployments and set fallback priority.</li>
  <li><a class="table-link" href="/api-keys">Create a router API key</a>. Use that key in your application's server. Provider keys remain encrypted in Voice Cloud.</li>
  <li><a class="table-link" href="/playground">Test a speech route</a>, then inspect its attempts and usage under Activity.</li>
 </ol>
 <p class="muted text-xs mt-4">Use your actual policy/deployment IDs and voice IDs in these examples. Native speech-to-speech means audio directly enters a speech model and audio comes back. Voice conversion preserves the spoken content; translation needs a model supporting the requested target language.</p>
</section>
<div class="space-y-5"><CodeBlock title="Text to speech" code={tts}/><CodeBlock title="Speech to text" code={stt}/><CodeBlock title="Native speech to speech" code={s2s}/></div>
