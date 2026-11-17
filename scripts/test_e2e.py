"""Exercise the real API and production console against loopback-only fake providers.

Run from the repository root: python3 scripts/test_e2e.py
Requires Go, Goose, Bun, and already-installed console dependencies. No paid calls.
"""

from email import policy as email_policy
from email.parser import BytesParser
import http.cookiejar
import importlib.util
import json
import base64
import os
from pathlib import Path
import socket
import subprocess
import tempfile
import threading
import time
import unittest
import urllib.error
import urllib.parse
import urllib.request
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from uuid import uuid4

ROOT = Path(__file__).resolve().parent.parent
AUDIO = b"ID3\x04\x00\x00e2e-audio"
PROVIDER_KEY = "local-e2e-provider-secret"


def require(condition, message):
    if not condition:
        raise AssertionError(message)


def free_port():
    with socket.socket() as listener:
        listener.bind(("127.0.0.1", 0))
        return listener.getsockname()[1]


class Provider(BaseHTTPRequestHandler):
    def log_message(self, *_):
        pass

    def reply(self, status, value, media="application/json"):
        body = json.dumps(value).encode() if isinstance(value, dict) else value
        self.send_response(status)
        self.send_header("Content-Type", media)
        self.send_header("Content-Length", str(len(body)))
        self.send_header("X-Request-ID", "local-provider-request")
        self.end_headers()
        self.wfile.write(body)

    def do_GET(self):
        if urllib.parse.urlsplit(self.path).path != "/v1/models" or self.headers.get("Authorization") != "Bearer " + PROVIDER_KEY:
            self.reply(401, {"error": "invalid catalog request"})
            return
        self.reply(200, {"data": [{"id": "local-stt", "task": "stt"}, {"id": "local-tts", "task": "tts"}, {"id": "local-s2s", "task": "s2s"}, {"id": "hidden-text-model", "task": "llm"}]})

    def do_POST(self):
        if self.headers.get("Authorization") != "Bearer " + PROVIDER_KEY:
            self.reply(401, {"error": "wrong provider credential"})
            return
        if self.headers.get("Transfer-Encoding", "").lower() == "chunked":
            chunks = []
            while True:
                size = int(self.rfile.readline(128).split(b";", 1)[0], 16)
                if size == 0:
                    while self.rfile.readline(8192).strip(): pass
                    break
                chunks.append(self.rfile.read(size))
                require(self.rfile.read(2) == b"\r\n", "Invalid chunk framing")
            raw = b"".join(chunks)
        else:
            raw = self.rfile.read(int(self.headers.get("Content-Length", "0")))
        if self.path == "/v1/audio/speech":
            body = json.loads(raw)
            require(body.get("voice") == "alloy" and body.get("model") == "local-tts", "Wrong synthesis selection")
            self.reply(200, AUDIO, "audio/mpeg")
            return
        if self.path not in ("/v1/audio/transcriptions", "/v1/audio/speech-to-speech"):
            self.reply(404, {"error": "unsupported speech path"})
            return
        message = BytesParser(policy=email_policy.default).parsebytes(
            ("Content-Type: " + self.headers.get("Content-Type", "") + "\r\nMIME-Version: 1.0\r\n\r\n").encode() + raw)
        fields = {part.get_param("name", header="content-disposition"): part.get_payload(decode=True) for part in message.iter_parts()}
        require(fields.get("file") == b"audio-bytes", "Audio upload changed")
        if fields.get("model") == b"local-failing":
            self.reply(503, {"error": "simulated provider failure"})
        elif self.path.endswith("speech-to-speech"):
            require(fields.get("model") == b"local-s2s", "Wrong S2S model")
            self.reply(200, AUDIO, "audio/mpeg")
        elif fields.get("stream") == b"true":
            self.reply(200, b'data: {"type":"transcript.text.delta","delta":"Hello"}\n\ndata: {"type":"transcript.text.done","text":"Hello from recorded audio"}\n\n', "text/event-stream")
        else:
            self.reply(200, {"text": "Hello from recorded audio", "duration": 1.25})


class LocalStack:
    def __init__(self, directory):
        self.directory = Path(directory)
        self.provider = ThreadingHTTPServer(("127.0.0.1", 0), Provider)
        self.api = "http://127.0.0.1:" + str(free_port())
        self.web = self.api
        while self.web == self.api:
            self.web = "http://127.0.0.1:" + str(free_port())
        self.processes = []
        self.logs = []
        self.provider.daemon_threads = True
        self.provider_url = "http://127.0.0.1:" + str(self.provider.server_address[1])
        self.thread = threading.Thread(target=self.provider.serve_forever, daemon=True)
        self.cookies = http.cookiejar.CookieJar()
        self.browser = urllib.request.build_opener(urllib.request.HTTPCookieProcessor(self.cookies))
        self.api_environment = dict(os.environ, DB_PATH=str(self.directory / "router.db"), API_ADDR=self.api.removeprefix("http://"), ADMIN_TOKEN="", E2E_PROVIDER_KEY=PROVIDER_KEY)
        self.api_environment['SECRETS_MASTER_KEY'] = base64.b64encode(os.urandom(32)).decode()

    def spawn(self, name, command, environment):
        log = (self.directory / (name + ".log")).open("a+")
        self.logs.append(log)
        process = subprocess.Popen(command, cwd=ROOT, env=environment, stdout=log, stderr=subprocess.STDOUT)
        self.processes.append(process)
        return process

    def start(self):
        print("Building API and production console...", flush=True)
        build_env = dict(os.environ, GOPROXY="off", GOTOOLCHAIN="local")
        subprocess.run(["go", "-C", "apps/api", "build", "-o", str(self.directory / "api"), "."], cwd=ROOT, env=build_env, check=True, timeout=180, capture_output=True)
        subprocess.run(["bun", "run", "build"], cwd=ROOT / "apps/web", check=True, timeout=120, capture_output=True)
        subprocess.run(["goose", "-env=none", "-dir", "db/migrations", "sqlite3", str(self.directory / "router.db"), "up"], cwd=ROOT, check=True, timeout=30, capture_output=True)
        self.thread.start()
        self.api_process = self.spawn("api", [str(self.directory / "api")], self.api_environment)
        self.wait_ready(self.api + "/healthz", self.api_process)
        web_env = dict(os.environ, API_URL=self.api, ORIGIN=self.web, HOST="127.0.0.1", PORT=self.web.rsplit(":", 1)[1], BODY_SIZE_LIMIT="1M")
        self.web_process = self.spawn("web", ["bun", "apps/web/build/index.js"], web_env)
        self.wait_ready(self.web + "/healthz", self.web_process)

    def wait_ready(self, url, process):
        deadline = time.monotonic() + 30
        while time.monotonic() < deadline:
            if process.poll() is not None:
                raise RuntimeError(f"Service exited with {process.returncode}: {url}")
            try:
                with urllib.request.urlopen(url, timeout=1) as response:
                    if response.status == 200:
                        return
            except (OSError, urllib.error.URLError):
                pass
            time.sleep(0.1)
        raise RuntimeError("Service did not become ready: " + url)

    def request(self, url, token=None, body=None, method=None, media="application/json", browser=False, headers=None, status=200):
        values = dict(headers or {})
        if token:
            values["Authorization"] = "Bearer " + token
        if body is not None:
            values["Content-Type"] = media
        data = json.dumps(body).encode() if isinstance(body, dict) else body
        request = urllib.request.Request(url, data=data, headers=values, method=method)
        opener = self.browser.open if browser else urllib.request.urlopen
        try:
            response = opener(request, timeout=15)
        except urllib.error.HTTPError as error:
            response = error
        with response:
            data = response.read()
            require(response.status == status, f"{request.get_method()} {url}: {response.status}, expected {status}: {data[:1000]!r}")
            return data, response.headers

    def json_api(self, path, token=None, body=None, method=None, status=None):
        if status is None:
            status = 201 if body is not None and method is None else 200
        data, _ = self.request(self.api + "/v1/" + path, token, body, method, status=status)
        return json.loads(data) if data else None

    def form(self, path, values):
        return self.request(self.web + path, body=urllib.parse.urlencode(values).encode(), media="application/x-www-form-urlencoded", browser=True, headers={"Origin": self.web, "Accept": "text/html"})

    def stop(self, process):
        if process.poll() is None:
            process.terminate()
            try:
                process.wait(timeout=12)
            except subprocess.TimeoutExpired:
                process.kill()
                process.wait(timeout=5)

    def close(self, failed):
        for process in reversed(self.processes):
            self.stop(process)
        if self.thread.is_alive():
            self.provider.shutdown()
            self.thread.join(timeout=2)
        self.provider.server_close()
        if failed:
            for log in self.logs:
                log.flush()
                log.seek(0)
                print(f"\n--- {Path(log.name).name} ---\n{log.read()[-6000:]}")
        for log in self.logs:
            log.close()

    def exercise(self):
        print("Checking console signup, sessions, configuration, roles, CSRF and logout...", flush=True)
        spec = importlib.util.spec_from_file_location("console_e2e", ROOT / "docker/test_console.py")
        module = importlib.util.module_from_spec(spec)
        spec.loader.exec_module(module)
        old_env = {name: os.environ.get(name) for name in ("WEB_URL", "API_URL", "ORIGIN")}
        os.environ.update(WEB_URL=self.web, API_URL=self.api, ORIGIN=self.web)
        try:
            result = unittest.TextTestRunner(verbosity=1).run(unittest.defaultTestLoader.loadTestsFromTestCase(module.ConsoleTest))
            require(result.wasSuccessful(), "Console lifecycle regression failed")
        finally:
            for name, value in old_env.items():
                if value is None:
                    os.environ.pop(name, None)
                else:
                    os.environ[name] = value
        self.form("/login", {"email": "admin@example.test", "password": "console-test-password"})
        session = next(cookie.value for cookie in self.cookies if cookie.name == "voicecloud_session")
        connection = self.json_api("connections", session, {"name": "E2E speech provider", "adapter": "local", "location_kind": "local", "base_url": self.provider_url})
        saved, _ = self.form("/manage/connections?/save", {"id": connection["id"], "name": connection["name"], "adapter": "local", "location_kind": "local", "base_url": self.provider_url, "enabled": "on", "api_key": PROVIDER_KEY})
        require(PROVIDER_KEY.encode() not in saved, "Provider key echoed in configuration HTML")
        print("Checking speech-only discovery, setup and encrypted credentials...", flush=True)
        catalog, _ = self.form("/models?/discover", {"connection_id": connection["id"]})
        require(b"local-stt" in catalog and b"local-s2s" in catalog and b"hidden-text-model" not in catalog, "Speech catalog was not filtered")
        caps = {"streaming": True, "max_input_bytes": 8192, "voices": ["alloy"], "response_formats": ["json", "mp3"], "target_languages": ["fr"]}
        deployments = {}
        for task in ("stt", "tts", "s2s"):
            page, _ = self.form("/models?/import", {"connection_id": connection["id"], "upstream_model_id": "local-" + task, "task": task, "capabilities_json": json.dumps(caps)})
            require(b"Registered deployment:" in page and PROVIDER_KEY.encode() not in page, "Speech model registration failed")
            deployments[task] = next(row["id"] for row in self.json_api("deployments", session) if row["upstream_model_id"] == "local-" + task)
        models = self.json_api("models", session)
        stt_model = next(row["id"] for row in models if row["task"] == "stt")
        primary = self.json_api("deployments", session, {"name": "Failing speech", "model_id": stt_model, "connection_id": connection["id"], "upstream_model_id": "local-failing", "pricing_json": {"request_nanos": 10}})
        policy = self.json_api("policies", session, {"name": "E2E speech fallback", "task": "stt", "max_attempts": 2, "fallback_enabled": True})
        for priority, deployment in enumerate((primary['id'], deployments['stt'])):
            self.json_api(f"policies/{policy['id']}/targets/{deployment}", session, {"priority": priority}, "PUT")
        key = self.json_api("api-keys", session, {"name": "E2E app", "default_policy_id": policy["id"]})["key"]
        print("Checking speech playground, native audio requests and fallback...", flush=True)
        page, _ = self.form("/playground", {"router_key": key, "model": f"deployment:{deployments['tts']}", "task": "tts", "text": "Hello", "voice": "alloy"})
        require(b"data:audio/mpeg;base64," in page and key.encode() not in page and PROVIDER_KEY.encode() not in page, "TTS playground failed or leaked a key")
        def upload(task, route, browser=False, extra=None):
            boundary = uuid4().hex
            values = {"model": route, "response_format": "json" if task == 'stt' else 'mp3'}
            if extra: values.update(extra)
            if browser: values.update({"task": task, "router_key": key})
            parts = [f'--{boundary}\r\nContent-Disposition: form-data; name="{name}"\r\n\r\n{value}\r\n'.encode() for name, value in values.items()]
            parts += [f'--{boundary}\r\nContent-Disposition: form-data; name="file"; filename="test.wav"\r\nContent-Type: audio/wav\r\n\r\n'.encode(), b'audio-bytes', f'\r\n--{boundary}--\r\n'.encode()]
            url = self.web + "/playground" if browser else self.api + "/v1/audio/" + ('transcriptions' if task == 'stt' else 'speech-to-speech')
            return self.request(url, None if browser else key, b''.join(parts), media=f"multipart/form-data; boundary={boundary}", browser=browser, headers={"Origin": self.web, "Accept": "text/html"} if browser else None)
        transcript, headers = upload('stt', f"policy:{policy['id']}")
        require(json.loads(transcript)['text'] == 'Hello from recorded audio', "Transcription failed")
        request_id = headers['X-Request-ID']
        attempts = self.json_api(f"requests/{request_id}/attempts", session)
        require(len(attempts) == 2 and attempts[0]['status'] == 'failed' and attempts[1]['status'] == 'succeeded', "Speech fallback not recorded")
        for task in ('stt','s2s'):
            page, _ = upload(task, f"deployment:{deployments[task]}", browser=True, extra={'voice':'alloy'} if task == 's2s' else None)
            expected = b'Hello from recorded audio' if task == 'stt' else b'data:audio/mpeg;base64,'
            require(expected in page and key.encode() not in page and PROVIDER_KEY.encode() not in page, task + ' playground failed')
        stream, _ = upload('stt', f"deployment:{deployments['stt']}", extra={'stream':'true'})
        require(b'transcript.text.done' in stream, "Transcription stream failed")
        self.request(self.api + '/v1/chat/completions', key, {'messages': []}, status=410)
        self.json_api('models', session, {'name':'Rejected chat','publisher':'test','task':'llm'}, status=400)
        print("Checking restart, stored credentials and speech activity...", flush=True)
        self.stop(self.api_process)
        self.api_process = self.spawn("api", [str(self.directory / "api")], self.api_environment)
        self.wait_ready(self.api + "/healthz", self.api_process)
        require(self.json_api(f"requests/{request_id}", session)['status'] == 'succeeded', "Speech history lost")
        self.request(self.web + "/", browser=True)
        print("PASS: speech-only console + API + SQLite + STT/TTS/S2S provider work end to end", flush=True)



def main():
    # Keep provider calls local even when the developer's shell has a proxy configured.
    for name in ("HTTP_PROXY", "HTTPS_PROXY", "ALL_PROXY", "http_proxy", "https_proxy", "all_proxy"):
        os.environ.pop(name, None)
    os.environ["NO_PROXY"] = "127.0.0.1,localhost"
    os.environ["no_proxy"] = "127.0.0.1,localhost"
    with tempfile.TemporaryDirectory(prefix="voice-cloud-e2e-") as directory:
        stack = LocalStack(directory)
        failed = True
        try:
            stack.start()
            stack.exercise()
            failed = False
        except (subprocess.CalledProcessError, subprocess.TimeoutExpired) as error:
            for output in (error.stdout, error.stderr):
                if output:
                    print(output.decode(errors="replace") if isinstance(output, bytes) else output)
            raise
        finally:
            stack.close(failed)


if __name__ == "__main__":
    main()
