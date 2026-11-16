import { chat, requireNewOutput, saveAudio, speech, transcribe } from './client';

try {
  const [input, output] = process.argv.slice(2);
  if (!input || !output || process.argv.length !== 4) throw new Error('Usage: bun run examples/typescript/voice-assistant.ts recording.wav reply.mp3');
  requireNewOutput(output);
  const transcript = await transcribe(input);
  console.log(`You: ${transcript}`);
  const reply = await chat(transcript);
  console.log(`Assistant: ${reply}`);
  await saveAudio(output, await speech(reply));
  console.log(`Saved ${output}`);
} catch (error) {
  console.error(error instanceof Error ? error.message : error);
  process.exitCode = 1;
}
