"""Exercise the real API and production console against loopback-only fake providers.

Run from the repository root: python3 scripts/test_e2e.py
Requires Go, Goose, Bun, and already-installed console dependencies. No paid calls.
"""

from email import policy as email_policy
from email.parser import BytesParser
import http.cookiejar
import importlib.util
import json
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

    def do_POST(self):
        if self.headers.get("Authorization") != "Bearer " + PROVIDER_KEY:
            self.reply(401, {"error": "wrong provider credential"})
            return
        if self.headers.get("Transfer-Encoding", "").lower() == "chunked":
            chunks = []
            while True:
                size = int(self.rfile.readline(128).split(b";", 1)[0], 16)
                if size == 0:
                    while self.rfile.readline(8192).strip():
                        pass
                    break
                chunks.append(self.rfile.read(size))
                require(self.rfile.read(2) == b"\r\n", "Invalid chunk framing")
            raw = b"".join(chunks)
        else:
            raw = self.rfile.read(int(self.headers.get("Content-Length", "0")))
        if self.path == "/v1/audio/transcriptions":
            message = BytesParser(policy=email_policy.default).parsebytes(
                ("Content-Type: " + self.headers.get("Content-Type", "") + "\r\nMIME-Version: 1.0\r\n\r\n").encode() + raw
            )
            fields = {part.get_param("name", header="content-disposition"): part.get_payload(decode=True) for part in message.iter_parts()}
            if fields.get("file") != b"audio-bytes" or fields.get("model") != b"local-stt":
                self.reply(400, {"error": "missing multipart audio or upstream model"})
                return
            self.reply(200, {"text": "Hello from recorded audio", "duration": 1.25})
            return
        body = json.loads(raw)
        if self.path == "/v1/audio/speech":
            if body.get("voice") != "alloy" or body.get("model") != "local-tts":
                self.reply(400, {"error": "invalid speech selection"})
                return
            self.reply(200, AUDIO, "audio/mpeg")
            return
        if self.path != "/v1/chat/completions":
            self.reply(404, {"error": "unsupported provider path"})
            return
        if body.get("model") == "local-failing":
            self.reply(503, {"error": "simulated unavailable provider"})
            return
        if body.get("model") != "local-chat":
            self.reply(400, {"error": "wrong upstream model"})
            return
        usage = {"prompt_tokens": 3, "completion_tokens": 2, "total_tokens": 5}
        if body.get("stream"):
            chunks = [
                {"choices": [{"index": 0, "delta": {"role": "assistant", "content": "E2E routed reply"}, "finish_reason": None}]},
                {"choices": [{"index": 0, "delta": {}, "finish_reason": "stop"}]},
                {"choices": [], "usage": usage},
            ]
            events = b"".join(b"data: " + json.dumps(chunk).encode() + b"\n\n" for chunk in chunks)
            self.reply(200, events + b"data: [DONE]\n\n", "text/event-stream")
            return
        self.reply(200, {"id": "local-completion", "choices": [{"message": {"role": "assistant", "content": "E2E routed reply"}, "finish_reason": "stop"}], "usage": usage})


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
        return self.request(self.web + path, body=urllib.parse.urlencode(values).encode(), media="application/x-www-form-urlencoded", browser=True, headers={"Origin": self.web})

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
        connection = self.json_api("connections", session, {"name": "E2E provider", "adapter": "custom", "location_kind": "local", "base_url": self.provider_url, "credential_ref": "E2E_PROVIDER_KEY"})
        model_ids = {}
        for task in ("llm", "stt", "tts"):
            model_ids[task] = self.json_api("models", session, {"name": "E2E " + task, "publisher": "local", "task": task})["id"]
        capabilities = {"streaming": True, "max_output_tokens": 512, "max_input_bytes": 8192, "voices": ["alloy"], "response_formats": ["json", "mp3"], "routing_description": "Local test deployment"}
        deployments = {}
        for name, task, upstream in (("failing", "llm", "local-failing"), ("chat", "llm", "local-chat"), ("stt", "stt", "local-stt"), ("tts", "tts", "local-tts")):
            deployments[name] = self.json_api("deployments", session, {"name": "E2E " + name, "model_id": model_ids[task], "connection_id": connection["id"], "upstream_model_id": upstream, "capabilities_json": capabilities, "pricing_json": {"request_nanos": 10}})["id"]
        policy = self.json_api("policies", session, {"name": "E2E chat policy", "task": "llm", "max_attempts": 2, "fallback_enabled": True})
        for priority, name in enumerate(("failing", "chat")):
            self.json_api(f"policies/{policy['id']}/targets/{deployments[name]}", session, {"priority": priority}, "PUT")
        key = self.json_api("api-keys", session, {"name": "E2E app", "default_policy_id": policy["id"]})["key"]
        print("Checking real console playground and secret handling...", flush=True)
        page, _ = self.form("/playground", {"router_key": key, "model": f"policy:{policy['id']}", "prompt": "Say hello"})
        require(b"E2E routed reply" in page, "Playground did not show the model reply")
        require(key.encode() not in page and session.encode() not in page and PROVIDER_KEY.encode() not in page, "Credential leaked in playground HTML")
        print("Checking fallback, SSE, transcription, speech, usage and analytics...", flush=True)
        response, headers = self.request(self.api + "/v1/chat/completions", key, {"model": f"policy:{policy['id']}", "messages": [{"role": "user", "content": "Hello"}]})
        require(json.loads(response)["choices"][0]["message"]["content"] == "E2E routed reply", "Unexpected chat reply")
        request_id = headers["X-Request-ID"]
        attempts = self.json_api(f"requests/{request_id}/attempts", session)
        require(len(attempts) == 2 and attempts[0]["status"] == "failed" and attempts[1]["status"] == "succeeded", "Fallback attempts were not recorded")
        stream, _ = self.request(self.api + "/v1/chat/completions", key, {"model": f"deployment:{deployments['chat']}", "messages": [{"role": "user", "content": "Hello"}], "stream": True})
        require(b"E2E routed reply" in stream and b"data: [DONE]" in stream, "Streaming response was incomplete")
        boundary = uuid4().hex
        multipart = (f'--{boundary}\r\nContent-Disposition: form-data; name="model"\r\n\r\ndeployment:{deployments["stt"]}\r\n--{boundary}\r\nContent-Disposition: form-data; name="file"; filename="test.wav"\r\nContent-Type: audio/wav\r\n\r\n'.encode() + b'audio-bytes' + f'\r\n--{boundary}--\r\n'.encode())
        transcript, _ = self.request(self.api + "/v1/audio/transcriptions", key, multipart, media=f"multipart/form-data; boundary={boundary}")
        require(json.loads(transcript)["text"] == "Hello from recorded audio", "Unexpected transcript")
        audio, _ = self.request(self.api + "/v1/audio/speech", key, {"model": f"deployment:{deployments['tts']}", "input": "Hello", "voice": "alloy", "response_format": "mp3"})
        require(audio == AUDIO, "Speech bytes changed in transit")
        usage = self.json_api(f"requests/{request_id}/usage", session)
        require(any(row["cost_nanos"] == 10 for row in usage), "Applied request price was not recorded")
        analytics = self.json_api("analytics", session)
        require(analytics["requests"] >= 5 and analytics["fallback_requests"] >= 2 and analytics["known_cost_nanos"] >= 40, "Analytics did not include routed requests")
        print("Checking API restart and persisted activity...", flush=True)
        self.stop(self.api_process)
        self.api_process = self.spawn("api", [str(self.directory / "api")], self.api_environment)
        self.wait_ready(self.api + "/healthz", self.api_process)
        persisted = self.json_api(f"requests/{request_id}", session)
        require(persisted["status"] == "succeeded", "Request history did not survive restart")
        self.request(self.web + "/", browser=True)
        print("PASS: real console + API + SQLite + loopback provider work end to end", flush=True)


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
