import {afterEach,expect,test} from 'bun:test';
import {testSpeechRoute} from '../src/lib/server/playground';
const original=globalThis.fetch;const key=`vc_${'a'.repeat(64)}`;
afterEach(()=>{globalThis.fetch=original;});
test('speech tests validate inputs before dispatch',async()=>{
 globalThis.fetch=(async()=>{throw Error('must not call');}) as typeof fetch;
 expect((await testSpeechRoute('http://api','bad','policy:1','tts',{text:'hello',voice:'voice'})).status).toBe(400);
 expect((await testSpeechRoute('http://api',key,'policy:1','llm',{text:'hello'})).status).toBe(400);
 expect((await testSpeechRoute('http://api',key,'policy:1','stt',{})).status).toBe(400);
});
test('TTS sends speech only and returns bounded playable audio',async()=>{
 globalThis.fetch=(async(url,init)=>{
  expect(String(url)).toBe('http://api/v1/audio/speech');expect(JSON.parse(String(init?.body))).toEqual({model:'policy:1',input:'hello',voice:'voice',response_format:'mp3'});
  expect(new Headers(init?.headers).get('authorization')).toBe(`Bearer ${key}`);
  return new Response('ID3audio',{headers:{'content-type':'audio/mpeg'}});
 }) as typeof fetch;
 const result=await testSpeechRoute('http://api',key,'policy:1','tts',{text:'hello',voice:'voice'});
 expect(result.audio).toStartWith('data:audio/mpeg;base64,');expect(JSON.stringify(result)).not.toContain(key);
});
test('STT and native S2S upload audio and keep secrets out of results',async()=>{
 for(const task of ['stt','s2s']){
  globalThis.fetch=(async(url,init)=>{
   expect(String(url)).toBe(`http://api/v1/audio/${task==='stt'?'transcriptions':'speech-to-speech'}`);
   const form=init?.body as FormData;expect(await(form.get('file') as File).text()).toBe('audio');
   return task==='stt'?Response.json({text:`hello ${key}`}):new Response('ID3audio',{headers:{'content-type':'audio/mpeg'}});
  }) as typeof fetch;
  const result=await testSpeechRoute('http://api',key,'deployment:1',task,{file:new File(['audio'],'recording.wav'),voice:'voice'});
  expect(result.status).toBe(200);expect(JSON.stringify(result)).not.toContain(key);
 }
});
test('speech tests bound responses and redact errors',async()=>{
 globalThis.fetch=(async()=>Response.json({error:`bad ${key}`},{status:502})) as typeof fetch;
 expect((await testSpeechRoute('http://api',key,'policy:1','tts',{text:'hi',voice:'voice'})).error).toBe('bad [redacted]');
 globalThis.fetch=(async()=>new Response(new Uint8Array(2*1024*1024+1),{headers:{'content-type':'audio/mpeg'}})) as typeof fetch;
 expect((await testSpeechRoute('http://api',key,'policy:1','tts',{text:'hi',voice:'voice'})).status).toBe(502);
});
