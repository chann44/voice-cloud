import { afterEach, expect, test } from 'bun:test';
import { mkdtemp, readFile, rm, writeFile } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { chat, requireNewOutput, saveAudio, speech, transcribe } from './client';

const originalFetch = globalThis.fetch;
const names = ['VOICE_CLOUD_URL', 'VOICE_CLOUD_API_KEY', 'LLM_MODEL', 'STT_MODEL', 'TTS_MODEL', 'VOICE', 'AUDIO_LANGUAGE'];
const originalEnv = Object.fromEntries(names.map((name) => [name, process.env[name]]));
const directories: string[] = [];

afterEach(async () => {
  globalThis.fetch = originalFetch;
  for (const name of names) {
    if (originalEnv[name] === undefined) delete process.env[name];
    else process.env[name] = originalEnv[name];
  }
  for (const directory of directories.splice(0)) await rm(directory, { recursive: true });
});

function configure() {
  Object.assign(process.env, {
    VOICE_CLOUD_URL: 'http://router.test/', VOICE_CLOUD_API_KEY: 'vc_example',
    LLM_MODEL: 'policy:1', STT_MODEL: 'policy:2', TTS_MODEL: 'policy:3',
    VOICE: 'supported-voice', AUDIO_LANGUAGE: 'en',
  });
}

test('chat sends a router selector and reports HTTP errors with a request ID', async () => {
  configure();
  globalThis.fetch = (async (url, options) => {
    expect(String(url)).toBe('http://router.test/v1/chat/completions');
    expect(new Headers(options?.headers).get('authorization')).toBe('Bearer vc_example');
    expect(JSON.parse(String(options?.body))).toEqual({ model: 'policy:1', messages: [{ role: 'user', content: 'Hello' }] });
    expect(options?.redirect).toBe('error');
    return Response.json({ choices: [{ message: { content: 'Hi' } }] });
  }) as typeof fetch;
  expect(await chat('Hello')).toBe('Hi');
  globalThis.fetch = (async () => new Response('{"error":"no route"}', { status: 503, headers: { 'x-request-id': 'request-1' } })) as typeof fetch;
  await expect(chat('Hello')).rejects.toThrow('HTTP 503 (request request-1)');
  delete process.env.VOICE_CLOUD_API_KEY;
  await expect(chat('Hello')).rejects.toThrow('Set VOICE_CLOUD_API_KEY');
});

test('transcription preserves file bytes and lets fetch set the multipart boundary', async () => {
  configure();
  const directory = await mkdtemp(join(tmpdir(), 'voice-cloud-example-'));
  directories.push(directory);
  const input = join(directory, 'speech.wav');
  await writeFile(input, 'audio-bytes');
  globalThis.fetch = (async (url, options) => {
    expect(String(url)).toBe('http://router.test/v1/audio/transcriptions');
    expect(new Headers(options?.headers).has('content-type')).toBe(false);
    const form = options?.body as FormData;
    expect(form.get('model')).toBe('policy:2');
    expect(form.get('language')).toBe('en');
    const file = form.get('file') as File;
    expect(file.name).toBe('speech.wav');
    expect(await file.text()).toBe('audio-bytes');
    return Response.json({ text: 'Hello' });
  }) as typeof fetch;
  expect(await transcribe(input)).toBe('Hello');
});

test('speech requires audio and refuses to overwrite an existing file', async () => {
  configure();
  globalThis.fetch = (async (_url, options) => {
    const body = JSON.parse(String(options?.body));
    expect(body.voice).toBe('supported-voice');
    expect(body.response_format).toBe('mp3');
    return new Response(new Uint8Array([1, 2, 3]), { headers: { 'content-type': 'audio/mpeg' } });
  }) as typeof fetch;
  const audio = await speech('Hello');
  const directory = await mkdtemp(join(tmpdir(), 'voice-cloud-example-'));
  directories.push(directory);
  const output = join(directory, 'reply.mp3');
  requireNewOutput(output);
  await saveAudio(output, audio);
  expect(() => requireNewOutput(output)).toThrow('Output already exists');
  await expect(saveAudio(output, new ArrayBuffer(0))).rejects.toThrow();
  expect([...await readFile(output)]).toEqual([1, 2, 3]);
  globalThis.fetch = (async () => Response.json({ error: 'not audio' })) as typeof fetch;
  await expect(speech('Hello')).rejects.toThrow('Expected audio');
});
