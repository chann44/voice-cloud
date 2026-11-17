export type PlaygroundResult = {
 status: number; text?: string; audio?: string; error?: string; request_id?: string;
};

export async function testSpeechRoute(apiUrl: string, key: string, model: string, task: string,
 input: {text?: string; voice?: string; file?: File; targetLanguage?: string}): Promise<PlaygroundResult> {
 if (!/^vc_[a-f0-9]{64}$/.test(key)) return {status:400,error:'Enter a router API key from API keys.'};
 if (!/^(policy|deployment):[1-9]\d*$/.test(model)) return {status:400,error:'Choose a speech policy or deployment.'};
 if (!['stt','tts','s2s'].includes(task)) return {status:400,error:'Choose a speech task.'};
 const voice=input.voice?.trim() ?? '';
 if (voice.length>256) return {status:400,error:'Voice ID must be at most 256 characters.'};
 const headers: Record<string,string>={Authorization:`Bearer ${key}`};
 let body: FormData | string;
 if (task==='tts') {
  if (!input.text?.trim() || input.text.length>500 || !voice) return {status:400,error:'Enter 1–500 characters and a supported voice ID.'};
  headers['Content-Type']='application/json';
  body=JSON.stringify({model,input:input.text,voice,response_format:'mp3'});
 } else {
  if (!input.file || input.file.size===0 || input.file.size>256*1024) return {status:400,error:'Choose an audio file up to 256 KiB. Use the API directly for larger files.'};
  if (input.targetLanguage && !/^[A-Za-z]{2,8}(-[A-Za-z0-9]{1,8})*$/.test(input.targetLanguage)) return {status:400,error:'Target language must be a language tag.'};
  body=new FormData();body.set('model',model);body.set('file',input.file);body.set('response_format',task==='stt'?'json':'mp3');
  if (task==='s2s' && voice) body.set('voice',voice);
  if (task==='s2s' && input.targetLanguage) body.set('target_language',input.targetLanguage);
 }
 const path=task==='stt'?'transcriptions':task==='tts'?'speech':'speech-to-speech';
 try {
  const response=await fetch(`${apiUrl}/v1/audio/${path}`,{method:'POST',headers,body,redirect:'error',signal:AbortSignal.timeout(60_000)});
  const id=response.headers.get('x-request-id') ?? '';
  const request_id=/^[a-f0-9]{32}$/.test(id)?id:undefined;
  const reader=response.body?.getReader();
  if (!reader) return {status:502,error:'The API returned an empty response.',request_id};
  const limit=task==='stt' || !response.ok?65536:2*1024*1024;
  const chunks:Uint8Array[]=[];let size=0;
  while (true) {
   const {done,value}=await reader.read();if(done)break;
   size+=value.length;if(size>limit){await reader.cancel();return {status:502,error:'The test response exceeded its size limit. Use the API directly for larger responses.',request_id};}
   chunks.push(value);
  }
  const bytes=new Uint8Array(size);let offset=0;for(const chunk of chunks){bytes.set(chunk,offset);offset+=chunk.length;}
  if (!response.ok || task==='stt') {
   let result;try{result=JSON.parse(new TextDecoder().decode(bytes));}catch{return {status:502,error:'The API did not return valid JSON.',request_id};}
   if(!response.ok)return {status:response.status,error:(typeof result?.error==='string'?result.error:'The route could not complete this request.').replaceAll(key,'[redacted]'),request_id};
   if(typeof result?.text!=='string')return {status:502,error:'The model returned no transcript.',request_id};
   return {status:200,text:result.text.replaceAll(key,'[redacted]'),request_id};
  }
  const mime=(response.headers.get('content-type') ?? '').split(';')[0];
  if(!/^audio\/[a-zA-Z0-9.+-]+$/.test(mime) || !bytes.length)return {status:502,error:'The model did not return audio.',request_id};
  let binary='';for(let start=0;start<bytes.length;start+=8192)binary+=String.fromCharCode(...bytes.subarray(start,start+8192));
  return {status:200,audio:`data:${mime};base64,${btoa(binary)}`,request_id};
 } catch {return {status:503,error:'The speech test failed or took longer than one minute. Check Activity before retrying.'};}
}
