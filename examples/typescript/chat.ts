import { chat } from './client';

try {
  const prompt = process.argv.slice(2).join(' ');
  if (!prompt.trim()) throw new Error('Usage: bun run examples/typescript/chat.ts "Your question"');
  console.log(await chat(prompt));
} catch (error) {
  console.error(error instanceof Error ? error.message : error);
  process.exitCode = 1;
}
