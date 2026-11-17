import { existsSync } from 'node:fs';
import { readFile, writeFile } from 'node:fs/promises';
import { basename } from 'node:path';

function required(name: string): string {
  const value = process.env[name]?.trim();
  if (!value) throw new Error(`Set ${name}; see examples/.env.example`);
  return value;
}

async function post(path: string, body: Record<string, unknown> | FormData): Promise<Response> {
  const base = (process.env.VOICE_CLOUD_URL || 'http://localhost:8080').replace(/\/+$/, '');
  const headers: Record<string, string> = {
    Authorization: `Bearer ${required('VOICE_CLOUD_API_KEY')}`,
  };
  const multipart = body instanceof FormData;
  if (!multipart) headers['Content-Type'] = 'application/json';
  const response = await fetch(`${base}/v1/${path}`, {
    method: 'POST',
    headers,
    body: multipart ? body : JSON.stringify(body),
    redirect: 'error',
    signal: AbortSignal.timeout(330_000),
  });
  if (!response.ok) {
    throw new Error(`HTTP ${response.status} (request ${response.headers.get('x-request-id') ?? 'unavailable'}): ${await response.text()}`);
  }
  return response;
}

export async function transcribe(path: string): Promise<string> {
  const form = new FormData();
  form.set('model', required('STT_MODEL'));
  form.set('file', new Blob([await readFile(path)]), basename(path));
  const language = process.env.AUDIO_LANGUAGE ?? 'en';
  if (language) form.set('language', language);
  form.set('response_format', 'json');
  const result = await (await post('audio/transcriptions', form)).json() as { text?: unknown };
  if (typeof result.text !== 'string' || !result.text.trim()) throw new Error('Transcription response did not contain text');
  return result.text;
}

export async function speech(text: string): Promise<ArrayBuffer> {
  const response = await post('audio/speech', {
    model: required('TTS_MODEL'),
    input: text,
    voice: process.env.VOICE || 'alloy',
    response_format: 'mp3',
  });
  const contentType = response.headers.get('content-type')?.split(';')[0].trim().toLowerCase();
  if (!contentType?.startsWith('audio/') && contentType !== 'application/octet-stream') {
    throw new Error(`Expected audio, received ${contentType ?? 'no Content-Type'}`);
  }
  const audio = await response.arrayBuffer();
  if (!audio.byteLength) throw new Error('Speech response was empty');
  return audio;
}

export function requireNewOutput(path: string): void {
  if (existsSync(path)) throw new Error(`Output already exists: ${path}; choose a new filename`);
}

export async function saveAudio(path: string, audio: ArrayBuffer): Promise<void> {
  await writeFile(path, new Uint8Array(audio), { flag: 'wx' });
}

export async function speechToSpeech(path: string): Promise<ArrayBuffer> {
  const form = new FormData();
  form.set('model', required('S2S_MODEL'));
  form.set('file', new Blob([await readFile(path)]), basename(path));
  form.set('response_format', 'mp3');
  if (process.env.VOICE) form.set('voice', process.env.VOICE);
  if (process.env.TARGET_LANGUAGE) form.set('target_language', process.env.TARGET_LANGUAGE);
  const response = await post('audio/speech-to-speech', form);
  if (!(response.headers.get('content-type') ?? '').startsWith('audio/')) throw new Error('Expected audio from the speech-to-speech model');
  const audio = await response.arrayBuffer();
  if (!audio.byteLength) throw new Error('Speech response was empty');
  return audio;
}
