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
            "S2S_MODEL": "policy:1", "STT_MODEL": "policy:2",
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

    def test_models_must_select_router_resources(self):
        with patch.dict(os.environ, {"STT_MODEL": "not-a-router-selector"}):
            with self.assertRaisesRegex(ValueError, "STT_MODEL"):
                client.transcribe("unused.wav")

    def test_native_speech_to_speech_preserves_audio_and_output(self):
        with tempfile.TemporaryDirectory() as directory:
            source = Path(directory, 'input.wav')
            source.write_bytes(b'native speech input')
            output = Path(directory, 'converted.mp3')
            with patch.dict(os.environ, {'TARGET_LANGUAGE': 'fr'}):
                with patch.object(client, 'post', return_value=b'ID3-native-output') as post:
                    client.speech_to_speech(source, output)
                    endpoint, body, content_type = post.call_args.args
                    self.assertEqual(endpoint, '/v1/audio/speech-to-speech')
                    message = BytesParser(policy=policy.default).parsebytes(
                        f'Content-Type: {content_type}\r\nMIME-Version: 1.0\r\n\r\n'.encode() + body)
                    fields = {part.get_param('name', header='content-disposition'): part.get_payload(decode=True) for part in message.iter_parts()}
                    self.assertEqual(fields['file'], b'native speech input')
                    self.assertEqual(fields['model'], b'policy:1')
                    self.assertEqual(fields['target_language'], b'fr')
                    self.assertEqual(output.read_bytes(), b'ID3-native-output')
                with patch.object(client, 'post') as post:
                    with self.assertRaises(FileExistsError):
                        client.speech_to_speech(source, output)
                    post.assert_not_called()


if __name__ == "__main__":
    unittest.main()
