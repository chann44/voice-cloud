import { requireNewOutput, saveAudio, speech } from './client';

try {
  const [text, output] = process.argv.slice(2);
  if (!text?.trim() || !output || process.argv.length !== 4) throw new Error('Usage: bun run examples/typescript/speech.ts "Hello world" speech.mp3');
  requireNewOutput(output);
  await saveAudio(output, await speech(text));
  console.log(`Saved ${output}`);
} catch (error) {
  console.error(error instanceof Error ? error.message : error);
  process.exitCode = 1;
}
