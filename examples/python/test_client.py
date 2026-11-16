"""Offline client checks. Run later: python3 -m unittest discover -s examples/python."""

from email import policy
from email.parser import BytesParser
import json
import os
from pathlib import Path
import tempfile
import unittest
from unittest.mock import MagicMock, patch

import client


class ClientTest(unittest.TestCase):
    def setUp(self):
        environment = patch.dict(os.environ, {
            "VOICE_CLOUD_URL": "http://localhost:8080/",
            "VOICE_CLOUD_API_KEY": "vc_example",
            "LLM_MODEL": "policy:1", "STT_MODEL": "policy:2",
            "TTS_MODEL": "deployment:3", "AUDIO_LANGUAGE": "en", "VOICE": "alloy",
        })
        environment.start()
        self.addCleanup(environment.stop)

    def test_post_authenticates_and_returns_response(self):
        response = MagicMock()
        response.headers = {"X-Request-ID": "request-1"}
        response.read.return_value = b'{"text":"hello"}'
        with patch.object(client, "build_opener") as build:
            build.return_value.open.return_value.__enter__.return_value = response
            self.assertEqual(client.post("/v1/example", {"hello": "world"}), b'{"text":"hello"}')
            request = build.return_value.open.call_args.args[0]
            self.assertEqual(request.full_url, "http://localhost:8080/v1/example")
            self.assertEqual(request.get_header("Authorization"), "Bearer vc_example")
            self.assertEqual(json.loads(request.data), {"hello": "world"})
            self.assertEqual(build.return_value.open.call_args.kwargs["timeout"], 330)

    def test_transcription_builds_valid_multipart(self):
        with tempfile.TemporaryDirectory() as directory:
            source = Path(directory, 'recording".wav')
            source.write_bytes(b"audio-bytes")
            with patch.object(client, "post", return_value=b'{"text":"Hello"}') as post:
                self.assertEqual(client.transcribe(source), "Hello")
                endpoint, body, content_type = post.call_args.args
                message = BytesParser(policy=policy.default).parsebytes(
                    f"Content-Type: {content_type}\r\nMIME-Version: 1.0\r\n\r\n".encode() + body
                )
                fields = {part.get_param("name", header="content-disposition"): part
                          for part in message.iter_parts()}
                self.assertEqual(endpoint, "/v1/audio/transcriptions")
                self.assertEqual(fields["model"].get_payload(decode=True), b"policy:2")
                self.assertEqual(fields["language"].get_payload(decode=True), b"en")
                self.assertEqual(fields["file"].get_payload(decode=True), b"audio-bytes")
                self.assertEqual(fields["file"].get_filename(), "recording%22.wav")

    def test_speech_never_overwrites_existing_output(self):
        with tempfile.TemporaryDirectory() as directory:
            output = Path(directory, "speech.mp3")
            output.write_bytes(b"original")
            with patch.object(client, "post") as post:
                with self.assertRaises(FileExistsError):
                    client.speech("Hello", output)
                post.assert_not_called()
            self.assertEqual(output.read_bytes(), b"original")

    def test_speech_rejects_json_instead_of_audio(self):
        response = MagicMock()
        response.headers = {"Content-Type": "application/json"}
        with patch.object(client, "build_opener") as build:
            build.return_value.open.return_value.__enter__.return_value = response
            with self.assertRaisesRegex(RuntimeError, "Expected audio"):
                client.post("/v1/audio/speech", {"input": "Hello"})

    def test_chat_rejects_missing_text(self):
        with patch.object(client, "post", return_value=b'{"choices":[]}'):
            with self.assertRaisesRegex(RuntimeError, "assistant message"):
                client.chat("Hello")
        with self.assertRaisesRegex(RuntimeError, "JSON response object"):
            client.json_response(b"[]")

    def test_models_must_select_router_resources(self):
        with patch.dict(os.environ, {"LLM_MODEL": "gpt-4o"}):
            with self.assertRaisesRegex(ValueError, "LLM_MODEL"):
                client.chat("Hello")


if __name__ == "__main__":
    unittest.main()
