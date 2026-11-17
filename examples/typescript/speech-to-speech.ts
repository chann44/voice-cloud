import {requireNewOutput,saveAudio,speechToSpeech} from './client';
try {
 const [input,output]=process.argv.slice(2);
 if(!input||!output)throw new Error('Usage: bun run examples/typescript/speech-to-speech.ts recording.wav converted.mp3');
 requireNewOutput(output);await saveAudio(output,await speechToSpeech(input));console.log(`Saved ${output}`);
} catch(error){console.error(error instanceof Error?error.message:error);process.exitCode=1;}
