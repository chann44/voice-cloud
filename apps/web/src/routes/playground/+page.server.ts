import { fail } from '@sveltejs/kit';
import { API_URL } from '$app/env/private';
import { testSpeechRoute } from '#lib/server/playground.js';
import type { Actions } from './$types';
export const actions: Actions = {
 default: async (event) => {
  const form=await event.request.formData();const file=form.get('file');
  const result=await testSpeechRoute(API_URL,String(form.get('router_key')??'').trim(),String(form.get('model')??''),String(form.get('task')??''),{
   text:String(form.get('text')??''),voice:String(form.get('voice')??''),targetLanguage:String(form.get('target_language')??''),file:file instanceof File?file:undefined,
  });
  return result.status===200?result:fail(result.status,result);
 },
};
