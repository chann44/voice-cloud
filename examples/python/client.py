"""Small server-side HTTP client shared by the examples; no third-party packages."""

import json
import mimetypes
import os
from pathlib import Path
import re
import sys
from urllib.error import HTTPError, URLError
from urllib.parse import quote
from urllib.request import HTTPRedirectHandler, Request, build_opener
from uuid import uuid4


class NoRedirect(HTTPRedirectHandler):
    def redirect_request(self, request, file, code, message, headers, new_url):
        return None


def model(name):
    value = os.environ.get(name, "")
    if not re.fullmatch(r"(policy|deployment):[1-9][0-9]*", value):
        raise ValueError(f"Set {name} to policy:<id> or deployment:<id>")
    return value


def post(path, body, content_type="application/json"):
    key = os.environ.get("VOICE_CLOUD_API_KEY", "")
    if not key.startswith("vc_") or any(char.isspace() for char in key):
        raise ValueError("Set VOICE_CLOUD_API_KEY to a router key beginning with vc_")
    if isinstance(body, dict):
        body = json.dumps(body).encode()
    url = os.environ.get("VOICE_CLOUD_URL", "http://localhost:8080").rstrip("/")
    request = Request(url + path, body, headers={
        "Authorization": f"Bearer {key}", "Content-Type": content_type,
    }, method="POST")
    try:
        with build_opener(NoRedirect()).open(request, timeout=330) as response:
            if path in ("/v1/audio/speech", "/v1/audio/speech-to-speech"):
                content_type = response.headers.get("Content-Type", "").split(";")[0].strip().lower()
                if not content_type.startswith("audio/") and content_type != "application/octet-stream":
                    raise RuntimeError(f"Expected audio, received {content_type or 'no Content-Type'}")
            request_id = response.headers.get("X-Request-ID")
            if request_id:
                print(f"Request ID: {request_id}", file=sys.stderr)
            return response.read()
    except HTTPError as error:
        request_id = error.headers.get("X-Request-ID", "unavailable")
        with error:
            detail = error.read(4096).decode("utf-8", errors="replace")
        raise RuntimeError(f"HTTP {error.code} (request {request_id}): {detail}") from error
    except URLError as error:
        raise RuntimeError(f"Cannot reach Voice Cloud: {error.reason}") from error


def json_response(data):
    try:
        result = json.loads(data)
    except (ValueError, UnicodeDecodeError) as error:
        raise RuntimeError("Voice Cloud returned invalid JSON") from error
    if not isinstance(result, dict):
        raise RuntimeError("Expected a JSON response object")
    return result


def transcribe(filename):
    selected_model = model("STT_MODEL")
    language = os.environ.get("AUDIO_LANGUAGE", "en")
    if language and not re.fullmatch(r"[A-Za-z]{1,8}(-[A-Za-z0-9]{1,8})*", language):
        raise ValueError("AUDIO_LANGUAGE must be a language tag")
    fields = {"model": selected_model, "response_format": "json"}
    if language: fields["language"] = language
    body, content_type = multipart_upload(filename, fields)
    result = json_response(post("/v1/audio/transcriptions", body, content_type))
    text = result.get("text")
    if not isinstance(text, str) or not text.strip():
        raise RuntimeError("Transcription response has no text")
    return text


def speech(text, output):
    path = Path(output)
    if path.exists():
        raise FileExistsError(f"Output already exists: {path}")
    voice = os.environ.get("VOICE", "alloy")
    if not voice.strip():
        raise ValueError("VOICE must name a voice supported by your TTS deployments")
    audio = post("/v1/audio/speech", {
        "model": model("TTS_MODEL"), "input": text,
        "voice": voice, "response_format": "mp3",
    })
    if not audio:
        raise RuntimeError("Speech response contains no audio")
    with path.open("xb") as destination:
        destination.write(audio)


def multipart_upload(filename, fields):
    path = Path(filename)
    if path.stat().st_size > 32 * 1024 * 1024:
        raise ValueError("The audio exceeds the router's 32 MiB request limit")
    audio = path.read_bytes()
    if not audio:
        raise ValueError("The audio file is empty")
    boundary = uuid4().hex
    parts = []
    for name, value in fields.items():
        parts.append(f'--{boundary}\r\nContent-Disposition: form-data; name="{name}"\r\n\r\n{value}\r\n'.encode())
    mime = mimetypes.guess_type(str(path))[0] or "application/octet-stream"
    parts.append((f'--{boundary}\r\nContent-Disposition: form-data; name="file"; filename="{quote(path.name, safe="")}"\r\n'
                  f'Content-Type: {mime}\r\n\r\n').encode())
    parts.extend([audio, f"\r\n--{boundary}--\r\n".encode()])
    body = b"".join(parts)
    if len(body) > 32 * 1024 * 1024:
        raise ValueError("The audio and multipart fields exceed the router's 32 MiB limit")
    return body, f"multipart/form-data; boundary={boundary}"


def speech_to_speech(filename, output):
    path = Path(output)
    if path.exists(): raise FileExistsError(f"Output already exists: {path}")
    fields = {"model": model("S2S_MODEL"), "response_format": "mp3"}
    if os.environ.get("VOICE"): fields["voice"] = os.environ["VOICE"]
    if os.environ.get("TARGET_LANGUAGE"): fields["target_language"] = os.environ["TARGET_LANGUAGE"]
    body, content_type = multipart_upload(filename, fields)
    audio = post("/v1/audio/speech-to-speech", body, content_type)
    if not audio: raise RuntimeError("Speech response contains no audio")
    with path.open("xb") as destination: destination.write(audio)
