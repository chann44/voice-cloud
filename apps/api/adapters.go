package main

import (
	"slices"

	"voice-cloud/db"
)

type adapterInfo struct {
	ID        string   `json:"id"`
	Tasks     []string `json:"tasks"`
	Protocol  string   `json:"protocol"`
	Discovery bool     `json:"discovery"`
	Notes     string   `json:"notes"`
}

var supportedAdapters = []adapterInfo{
	{"openai", []string{"stt", "tts"}, "openai-compatible", false, "Speech endpoints only. Register an actual audio model ID manually; chat and realtime APIs are unavailable."},
	{"openai-compatible", []string{"stt", "tts", "s2s"}, "openai-compatible", true, "Compatible speech server required. Discovery includes only entries explicitly tagged task: stt, tts or s2s; otherwise register manually."},
	{"local", []string{"stt", "tts", "s2s"}, "openai-compatible", true, "Run your Hugging Face speech server first. Native S2S uses our /audio/speech-to-speech upload contract. Discovery needs explicit speech task metadata; weights alone are not an API."},
	{"custom", []string{"stt", "tts", "s2s"}, "openai-compatible", true, "Compatible speech endpoints only. Register manually if the server omits speech task metadata."},
	{"whisper", []string{"stt"}, "openai-compatible", true, "OpenAI-compatible transcription server. Register the served STT model manually if discovery has no task metadata."},
	{"azure", []string{"stt", "tts"}, "openai-compatible", false, "Azure OpenAI v1 audio endpoints. Use your resource URL and deployed audio model's deployment name; no chat or Entra authentication."},
	{"deepinfra", []string{"stt", "tts"}, "openai-compatible", false, "Use /v1/openai audio endpoints and register an actual speech model ID manually."},
	{"together", []string{"stt", "tts"}, "openai-compatible", false, "Use compatible speech endpoints and register the audio model ID manually."},
	{"deepgram", []string{"stt", "tts"}, "native-http", true, "Recorded-audio STT (JSON/text), TTS (MP3/WAV). TTS voice must equal the Aura model ID. No WebSockets."},
	{"elevenlabs", []string{"stt", "tts", "s2s"}, "native-http", true, "Recorded-audio STT (JSON/text), TTS (MP3) with a real voice ID. Catalog lists TTS and voice conversion; enter Scribe IDs manually. Voice conversion does not translate speech. No WebSockets."},
}

func adapterSupport(id string) *adapterInfo {
	for i := range supportedAdapters {
		if supportedAdapters[i].ID == id {
			return &supportedAdapters[i]
		}
	}
	return nil
}

func supportsInferenceAdapter(id string) bool { return adapterSupport(id) != nil }

func adapterRequestReason(adapter string, input *inferenceInput, deployment db.Deployment) string {
	info := adapterSupport(adapter)
	if info == nil {
		return "unsupported_adapter"
	}
	if input == nil {
		return ""
	}
	if !slices.Contains(info.Tasks, input.task) {
		return "adapter_task"
	}
	if adapter == "deepgram" || adapter == "elevenlabs" {
		return speechAdapterReason(adapter, input, deployment)
	}
	return ""
}
