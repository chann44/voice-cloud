import { transcribe } from './client';

try {
  const [path] = process.argv.slice(2);
  if (!path || process.argv.length !== 3) throw new Error('Usage: bun run examples/typescript/transcribe.ts recording.wav');
  console.log(await transcribe(path));
} catch (error) {
  console.error(error instanceof Error ? error.message : error);
  process.exitCode = 1;
}
