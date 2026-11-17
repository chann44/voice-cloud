<script lang="ts">
 import { enhance } from '$app/forms';
 import { page } from '$app/state';
 import { untrack } from 'svelte';
 import type { PageProps } from './$types';
 import PageHeader from '#lib/components/PageHeader.svelte';
 import { Button } from '#lib/components/ui/button/index.js';
 import { Input } from '#lib/components/ui/input/index.js';
 let {data,form}:PageProps=$props();
 let pending=$state(false),routerKey=$state('');
 let model=$state(untrack(()=>page.url.searchParams.get('model')??''));
 let task=$state(untrack(()=>data.models.find((item)=>item.id===data.deployments.find((deployment)=>`deployment:${deployment.id}`===model)?.model_id)?.task??'tts'));
 const policies=$derived(data.policies.filter((policy)=>policy.task===task));
 const deployments=$derived(data.deployments.filter((deployment)=>deployment.enabled&&data.models.some((item)=>item.id===deployment.model_id&&item.task===task)));
</script>
<svelte:head><title>Test a speech route · Voice Cloud</title></svelte:head>
<PageHeader title="Test a speech route" path="connect / playground" description="Transcribe audio, generate speech, or send audio directly to a speech-to-speech model." />
<div class="two-column">
 <section class="panel panel-body">
  <form method="POST" enctype="multipart/form-data" class="flex flex-col gap-3" use:enhance={()=>{
   pending=true;return async({update})=>{routerKey='';try{await update({reset:false});}finally{pending=false;}};
  }}>
   <label for="router-key">Router API key</label><Input id="router-key" name="router_key" type="password" autocomplete="off" bind:value={routerKey} required />
   <label for="speech-task">Task</label><select id="speech-task" name="task" class="native-select" bind:value={task} onchange={()=>model=''}><option value="tts">Text to speech</option><option value="stt">Speech to text</option><option value="s2s">Speech to speech</option></select>
   <label for="speech-route">Route</label><select id="speech-route" name="model" class="native-select" bind:value={model} required><option value="" disabled>Choose a speech route</option>
    <optgroup label="Policies">{#each policies as policy}<option value={`policy:${policy.id}`}>{policy.name} / policy:{policy.id}</option>{/each}</optgroup>
    <optgroup label="Deployments">{#each deployments as deployment}<option value={`deployment:${deployment.id}`}>{deployment.name} / deployment:{deployment.id}</option>{/each}</optgroup>
   </select>
   {#if task==='tts'}<label for="speech-text">Text</label><textarea id="speech-text" name="text" class="native-textarea" rows="4" maxlength={500} required>Hello from Voice Cloud.</textarea>
   {:else}<label for="speech-file">Audio file</label><input id="speech-file" name="file" type="file" accept="audio/*" required /><p class="muted text-xs">Up to 256 KiB for this test. Larger uploads use the API.</p>{/if}
   {#if task!=='stt'}<label for="speech-voice">Voice ID {task==='s2s'?'(if required by the model)':''}</label><Input id="speech-voice" name="voice" maxlength={256} required={task==='tts'} placeholder="A voice supported by this deployment" />{/if}
   {#if task==='s2s'}<label for="target-language">Target language (translation models only)</label><Input id="target-language" name="target_language" placeholder="For example: fr" /><p class="muted text-xs">Voice conversion preserves the spoken language. Translation needs a native translation model declaring the target language.</p>{/if}
   <p class="muted text-xs">This makes a real provider request and may incur charges. Audio output uses MP3. Your router key is cleared afterward.</p>
   <Button type="submit" disabled={pending||(!policies.length&&!deployments.length)}>{pending?'Testing speech...':'Send speech request →'}</Button>
   {#if !policies.length&&!deployments.length}<p>No route configured for this speech task. <a href="/models" class="table-link">Register a speech model →</a></p>{/if}
  </form>
 </section>
 <section class="panel panel-body" aria-live="polite"><h2 class="mb-3">Result</h2>
  {#if form?.error}<p role="alert" class="negative">{form.error}</p>{:else if form?.audio}<audio controls src={form.audio} aria-label="Generated speech"></audio>{:else if form?.text!==undefined}<p class="whitespace-pre-wrap">{form.text||'(No speech detected)'}</p>{:else}<p class="muted">Your transcript or generated audio will appear here.</p>{/if}
  {#if form?.request_id}<a class="table-link block mt-4" href={`/activity?request=${form.request_id}`}>Inspect request {form.request_id} →</a>{/if}
 </section>
</div>
