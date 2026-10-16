"""Exercise account assembly through the production Java and Go entrypoints.

Run with the project jsonschema environment after building the Java jar and
``go build -o <runner.exe> ./cmd/runner``::

    python tests/integration/accounts_process.py --java <java21.exe> --runner <runner.exe>

Only a loopback Bilibili HTTP fixture is contacted. All account files, process
logs and SQLite data are new, isolated files below backend/java/target; no real
credentials, platform requests, downloads, uploads or deployment are used.
"""

import argparse
import collections
import http.client
import json
import os
from pathlib import Path
import secrets
import subprocess
import threading
import time
import uuid
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from urllib.error import HTTPError, URLError
from urllib.parse import parse_qs, urlparse
from urllib.request import ProxyHandler, Request, build_opener

from jsonschema import Draft202012Validator, FormatChecker

from platform_clients_process import ROOT, free_port


ACCOUNTS = {"bilibili-main", "telegram-main", "quark-main", "drive-main"}
TERMINAL_TASKS = {"SUCCEEDED", "FAILED", "CANCELLED", "NEEDS_ACTION"}


def require(condition, message):
    if not condition:
        raise AssertionError(message)


class AccountFixture:
    def __init__(self):
        self.lock = threading.RLock()
        self.calls = collections.Counter()
        self.errors = []
        self.cookie = "synthetic-qr-session-" + secrets.token_hex(16)
        self.discarded_value = "discarded-upstream-value-" + secrets.token_hex(16)
        self.operations = {}
        self.registrations = 0
        self.block_session = None
        self.ready_result = threading.Event()
        self.release_result = threading.Event()
        self.blocked_operation = None

    def counts(self):
        with self.lock:
            return dict(self.calls)


def platform_handler(fixture):
    class Handler(BaseHTTPRequestHandler):
        def log_message(self, *_):
            pass

        def do_GET(self):
            parsed = urlparse(self.path)
            with fixture.lock:
                fixture.calls[parsed.path] += 1
                sequence = fixture.calls[parsed.path]
            cookie_headers = []
            if parsed.path == "/x/passport-login/web/qrcode/generate":
                if self.headers.get("Cookie") or self.headers.get("Authorization"):
                    fixture.errors.append("credential sent during QR generation")
                key = "fixture-key-" + str(sequence)
                data = {"code": 0, "data": {
                    "url": "https://passport.bilibili.com/h5-app/passport/login/scan"
                           "?navhide=1&qrcode_key=" + key,
                    "qrcode_key": key,
                }}
            elif parsed.path == "/x/passport-login/web/qrcode/poll":
                keys = parse_qs(parsed.query).get("qrcode_key", [])
                if self.headers.get("Cookie") or self.headers.get("Authorization"):
                    fixture.errors.append("credential sent during QR polling")
                if len(keys) != 1 or not keys[0].startswith("fixture-key-"):
                    fixture.errors.append("unexpected native QR poll parameters")
                cookie_headers = [
                    "DedeUserID=42; Domain=.bilibili.com; Path=/",
                    "SESSDATA=" + fixture.cookie + "; Domain=.bilibili.com; Path=/; HttpOnly; Secure",
                ]
                # The native client must discard callback/refresh data, never
                # follow this address, and prove the Set-Cookie UID through nav.
                data = {"code": 0, "data": {
                    "code": 0,
                    "url": "https://discarded.invalid/?SESSDATA=" + fixture.discarded_value,
                    "refresh_token": fixture.discarded_value,
                }}
            elif parsed.path == "/x/web-interface/nav":
                cookie = self.headers.get("Cookie", "")
                if "SESSDATA=" + fixture.cookie not in cookie or "DedeUserID=42" not in cookie:
                    fixture.errors.append("nav did not prove the newly confirmed fixture identity")
                data = {"code": 0, "data": {"isLogin": True, "mid": 42}}
            else:
                fixture.errors.append("unexpected platform endpoint")
                self.send_error(404)
                return
            raw = json.dumps(data).encode()
            self.send_response(200)
            self.send_header("Content-Type", "application/json")
            self.send_header("Content-Length", str(len(raw)))
            for cookie in cookie_headers:
                self.send_header("Set-Cookie", cookie)
            self.end_headers()
            self.wfile.write(raw)

    return Handler


def control_proxy(fixture, java_port, token):
    class Proxy(BaseHTTPRequestHandler):
        def log_message(self, *_):
            pass

        def do_POST(self):
            raw = self.rfile.read(int(self.headers.get("Content-Length", "0")))
            request = json.loads(raw)
            is_result = self.path.startswith("/internal/v1/operations/") and self.path.endswith("/result")
            with fixture.lock:
                operation = fixture.operations.get(self.path.split("/")[-2], {}) if is_result else {}
                payload = operation.get("payload", {})
                block = (is_result and fixture.block_session is not None
                         and payload.get("authSessionId") == fixture.block_session
                         and payload.get("action") == "POLL"
                         and request.get("status") == "SUCCEEDED"
                         and request.get("result", {}).get("session", {}).get("state") == "SUCCEEDED")
            if block:
                fixture.blocked_operation = operation["operationId"]
                fixture.ready_result.set()
                if not fixture.release_result.wait(8):
                    fixture.errors.append("test cancellation did not release the completed login result")
            connection = http.client.HTTPConnection("127.0.0.1", java_port, timeout=10)
            try:
                connection.request("POST", self.path, raw, {
                    "Content-Type": "application/json", "Authorization": "Bearer " + token,
                })
                response = connection.getresponse()
                reply, status = response.read(), response.status
            finally:
                connection.close()
            parsed = json.loads(reply)
            with fixture.lock:
                if self.path == "/internal/v1/runners/register" and status == 200:
                    fixture.registrations += 1
                    parsed["data"]["heartbeatSeconds"] = 1
                    reply = json.dumps(parsed).encode()
                if self.path == "/internal/v1/leases/acquire" and status == 200:
                    operation = parsed["data"].get("operation")
                    if operation is not None:
                        fixture.operations[operation["operationId"]] = operation
            self.send_response(status)
            self.send_header("Content-Type", "application/json")
            self.send_header("Content-Length", str(len(reply)))
            self.end_headers()
            self.wfile.write(reply)

    return Proxy


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--java", required=True)
    parser.add_argument("--runner", required=True)
    args = parser.parse_args()
    jar = ROOT / "backend/java/target/alldownloads-backend-0.1.0-SNAPSHOT.jar"
    require(jar.is_file(), "build the current Java production jar before this test")
    require(Path(args.java).is_file(), "--java must name the Java 21 executable")
    require(Path(args.runner).is_file(), "--runner must name the built production cmd/runner executable")
    java, runner = str(Path(args.java).resolve()), str(Path(args.runner).resolve())
    runtime = ROOT / "backend/java/target" / ("accounts-process-" + uuid.uuid4().hex[:12])
    runtime.mkdir(parents=True)
    downloads = runtime / "downloads"
    downloads.mkdir()
    token = "synthetic-internal-" + secrets.token_urlsafe(48)
    token_file = runtime / "internal.token"
    token_file.write_text(token, encoding="utf-8")
    cookie_file = runtime / "new-bilibili.session"
    drive_token, drive_client = runtime / "new-drive.token.json", runtime / "new-drive.client.json"
    fixture = AccountFixture()
    java_port = free_port()
    base = "http://127.0.0.1:" + str(java_port) + "/api/v1"
    platform = ThreadingHTTPServer(("127.0.0.1", 0), platform_handler(fixture))
    proxy = ThreadingHTTPServer(("127.0.0.1", 0), control_proxy(fixture, java_port, token))
    for server in (platform, proxy):
        threading.Thread(target=server.serve_forever, daemon=True).start()
    fixture_url = "http://127.0.0.1:" + str(platform.server_port)
    config = runtime / "runner.local.json"
    config.write_text(json.dumps({
        "runnerId": "accounts-production-runner",
        "controlUrl": "http://127.0.0.1:" + str(proxy.server_port),
        "tokenFile": str(token_file), "stateDir": str(runtime / "runner"),
        "allowedRoots": [str(runtime)], "roots": {"download-root": str(downloads)},
        "isolatedTestMode": True,
        "bilibili": {"cookieFile": str(cookie_file), "expectedUserId": "42",
                     "apiBase": fixture_url, "passportBase": fixture_url},
        "driveAccounts": {"drive-main": {"tokenFile": str(drive_token), "clientFile": str(drive_client)}},
    }, indent=2), encoding="utf-8")
    schema = json.loads((ROOT / "contracts/accounts.schema.json").read_text(encoding="utf-8"))
    Draft202012Validator.check_schema(schema)
    validators = {name: Draft202012Validator({
        "$schema": schema["$schema"], "$defs": schema["$defs"], "$ref": "#/$defs/" + name,
    }, format_checker=FormatChecker()) for name in ("Account", "AuthSession")}
    captures = {"accounts": [], "sessions": []}
    opener = build_opener(ProxyHandler({}))  # Every request is direct loopback HTTP.
    processes, logs = [], []
    flags = subprocess.CREATE_NO_WINDOW if hasattr(subprocess, "CREATE_NO_WINDOW") else 0
    stage = "start isolated Java"

    def safe_public(value):
        encoded = json.dumps(value, ensure_ascii=False)
        for private in (token, fixture.cookie, fixture.discarded_value, "SESSDATA", "refresh_token"):
            require(private not in encoded, "private fixture authentication data reached a public response")

    def validate_capture(definition, value):
        safe_public(value)
        require(not any(validators[definition].iter_errors(value)), "public response violates accounts.schema " + definition)
        captures["accounts" if definition == "Account" else "sessions"].append(value)

    def api(path, method="GET", body=None, revision=None, key=None):
        headers = {"Content-Type": "application/json"}
        if method == "POST":
            headers["Idempotency-Key"] = key or uuid.uuid4().hex
        if revision is not None:
            headers["If-Match"] = '"' + revision + '"'
        request = Request(base + path, None if body is None else json.dumps(body).encode(), headers, method=method)
        try:
            with opener.open(request, timeout=5) as response:
                require(response.status == (202 if method in ("POST", "DELETE") else 200), "unexpected public HTTP status")
                result = json.load(response)["data"]
        except HTTPError as error:
            raise AssertionError("public " + method + " " + path.split("?")[0] + " returned HTTP " + str(error.code)) from None
        safe_public(result)
        if path == "/accounts":
            require(result["nextCursor"] is None, "fixed account list unexpectedly paginated")
            for account in result["items"]:
                validate_capture("Account", account)
        elif path.startswith("/auth-sessions"):
            validate_capture("AuthSession", result)
        return result

    def wait_for(fetch, predicate=lambda value: bool(value), label="persisted state", timeout=45):
        deadline = time.monotonic() + timeout
        while time.monotonic() < deadline:
            for child in processes:
                require(child.poll() is None, "owned Java/Go process exited during " + label)
            try:
                value = fetch()
                if predicate(value):
                    return value
            except (URLError, ConnectionError, TimeoutError):
                pass
            time.sleep(.1)
        raise AssertionError("timed out waiting for " + label)

    def start_process(executable, arguments, name):
        output = open(runtime / (name + "-" + str(len(logs)) + ".log"), "wb")
        logs.append(output)
        child = subprocess.Popen([executable, *arguments], cwd=runtime,
                                 stdout=output, stderr=subprocess.STDOUT, creationflags=flags)
        processes.append(child)
        return child

    def stop_process(child):
        if child.poll() is None:
            child.terminate()
            try:
                child.wait(timeout=8)
            except subprocess.TimeoutExpired:
                child.kill()
                child.wait(timeout=5)
        processes.remove(child)

    def start_go():
        with fixture.lock:
            before = fixture.registrations
        child = start_process(runner, ["-config", str(config)], "go")
        wait_for(lambda: fixture.registrations, lambda value: value > before, "production Go registration")
        return child

    def task(identity, expected="SUCCEEDED"):
        require(isinstance(identity, str) and bool(identity), "account operation did not return taskId")
        value = wait_for(lambda: api("/tasks/" + identity),
                         lambda item: item["status"] in TERMINAL_TASKS, "account task " + identity)
        require(value["status"] == expected, "account task reached an unexpected terminal state")
        return value

    def session(identity):
        return api("/auth-sessions/" + identity)

    def begin_bilibili():
        begun = api("/auth-sessions", "POST", {"platform": "BILIBILI"})
        task(begun["taskId"])
        ready = session(begun["authSessionId"])
        require(ready["state"] == "PENDING" and ready["nextAction"] == "SCAN_QR", "native BEGIN did not provide scan action")
        scan = urlparse(ready["url"])
        require(scan.scheme == "https" and scan.netloc == "passport.bilibili.com"
                and scan.path == "/h5-app/passport/login/scan"
                and set(parse_qs(scan.query)) <= {"navhide", "from", "qrcode_key"}
                and parse_qs(scan.query).get("qrcode_key"), "native BEGIN did not return the official QR URL")
        require(ready["revision"] != begun["revision"], "BEGIN result did not advance Java revision")
        return ready

    def poll_session(current):
        polled = api("/auth-sessions/" + current["authSessionId"] + "/steps", "POST", {"action": "POLL"})
        require(polled["authSessionId"] == current["authSessionId"]
                and polled["taskId"] != current["taskId"]
                and polled["revision"] != current["revision"], "POLL did not preserve flow identity with a new task/revision")
        return polled

    try:
        start_process(java, ["-jar", str(jar), "--server.address=127.0.0.1", "--server.port=" + str(java_port),
                            "--alldownloads.database=" + str(runtime / "business.db"),
                            "--alldownloads.runner-token-file=" + str(token_file),
                            "--alldownloads.download-root=", "--alldownloads.frontend-root=",
                            "--alldownloads.allowed-origins=", "--spring.main.banner-mode=off",
                            "--logging.level.root=ERROR"], "java")
        wait_for(lambda: api("/health"), label="isolated Java HTTP readiness")
        initial = api("/accounts")
        require({item["id"] for item in initial["items"]} == ACCOUNTS, "empty database did not expose the four fixed accounts")
        require(all(item["state"] == "UNKNOWN" for item in initial["items"]), "empty database invented checked accounts")
        for _ in range(3):
            require(api("/accounts") == initial and api("/tasks")["total"] == 0, "ordinary account GET created a task or changed facts")

        stage = "unconfigured account checks"
        go = start_go()
        require(not fixture.counts(), "ordinary startup contacted the platform fixture")
        checks = {identity: api("/accounts/" + identity + "/check", "POST", {}) for identity in sorted(ACCOUNTS)}
        for identity, result in checks.items():
            require(result["accountId"] == identity, "account check changed logical identity")
            task(result["taskId"])
        checked = api("/accounts")["items"]
        require(all(item["state"] == "AUTH_REQUIRED" for item in checked), "missing new sessions did not require authentication")
        require(not fixture.counts(), "missing sessions caused platform network requests")

        stage = "Drive local setup without client"
        drive = api("/auth-sessions", "POST", {"platform": "GOOGLE_DRIVE"})
        task(drive["taskId"])
        drive = session(drive["authSessionId"])
        require((drive["state"], drive["nextAction"], drive["reasonCode"], drive["url"])
                == ("NEEDS_ACTION", "LOCAL_SETUP", "AUTH_CLIENT_REQUIRED", None), "missing Drive client did not produce explicit local setup")
        require(not drive_token.exists() and not drive_client.exists() and not fixture.counts(), "Drive local setup created credentials or contacted a platform")

        stage = "native Bilibili QR login and explicit identity check"
        first = begin_bilibili()
        task_count, request_counts = api("/tasks")["total"], fixture.counts()
        for _ in range(3):
            require(session(first["authSessionId"]) == first, "session GET changed the persisted flow")
        require(api("/tasks")["total"] == task_count and fixture.counts() == request_counts, "session GET enqueued or polled authorization")
        polled = poll_session(first)
        task(polled["taskId"])
        success = session(first["authSessionId"])
        require((success["state"], success["nextAction"], success["url"]) == ("SUCCEEDED", "NONE", None), "native confirmed QR did not complete")
        require(cookie_file.is_file(), "native login did not persist its new private session")
        require(next(item for item in api("/accounts")["items"] if item["id"] == "bilibili-main")["state"] == "AUTH_REQUIRED", "login implicitly replaced the explicit account-check fact")
        active_check = api("/accounts/bilibili-main/check", "POST", {})
        task(active_check["taskId"])
        active = next(item for item in api("/accounts")["items"] if item["id"] == "bilibili-main")
        require(active["state"] == "ACTIVE" and active["lastCheckedAt"] is not None
                and active["reasonCode"] is None, "explicit nav identity check did not activate the account")

        stage = "cancel after provider READY before Java completion"
        second = begin_bilibili()
        require(second["authSessionId"] != first["authSessionId"], "second login reused a terminal flow")
        fixture.block_session = second["authSessionId"]
        in_flight = poll_session(second)
        wait_for(fixture.ready_result.is_set, label="completed provider login held before Java result", timeout=20)
        current = session(second["authSessionId"])
        cancelled = api("/auth-sessions/" + second["authSessionId"], "DELETE", revision=current["revision"])
        require(cancelled["state"] == "CANCELLED" and cancelled["taskId"] != in_flight["taskId"], "cancel intent did not create its cleanup operation")
        fixture.release_result.set()
        task(cancelled["taskId"])
        after_cancel = session(second["authSessionId"])
        require(after_cancel == cancelled and fixture.blocked_operation is not None, "late READY result revived or rewrote cancelled Java session")

        stage = "restart and poll the old provider flow"
        third = begin_bilibili()
        stop_process(go)
        before_restart = fixture.counts()
        go = start_go()
        require(fixture.counts() == before_restart, "Go restart contacted an account provider")
        restarted_poll = poll_session(third)
        task(restarted_poll["taskId"])
        interrupted = session(third["authSessionId"])
        require((interrupted["state"], interrupted["nextAction"], interrupted["reasonCode"], interrupted["url"])
                == ("NEEDS_ACTION", "LOCAL_SETUP", "AUTH_SESSION_NOT_FOUND", None), "old POLL did not explain the missing process-owned flow")
        require(fixture.counts() == before_restart, "old POLL secretly started or queried another provider flow")
        cleanup = api("/auth-sessions/" + third["authSessionId"], "DELETE", revision=interrupted["revision"])
        task(cleanup["taskId"])
        require(session(third["authSessionId"])["state"] == "CANCELLED", "unknown old flow could not be cancelled")

        stage = "validate isolated captures and side-effect boundaries"
        require(api("/download-orders")["total"] == 0 and api("/archives")["total"] == 0, "account operations created downloads or archives")
        require(not api("/bilibili/sources")["items"], "account operations created a content source")
        require(all(item["phase"] in {"CHECK_ACCOUNT", "AUTH_STEP"} for item in api("/tasks?limit=100")["items"]), "account operations scheduled unrelated work")
        require(not fixture.errors, "native protocol fixture detected an unexpected request or auth boundary")
        counts = fixture.counts()
        require(counts.get("/x/passport-login/web/qrcode/generate") == 3
                and counts.get("/x/passport-login/web/qrcode/poll") == 2
                and counts.get("/x/web-interface/nav") == 3, "native login/check request counts changed")
        capture_path = runtime / "accounts-public.json"
        capture_path.write_text(json.dumps(captures, ensure_ascii=False, indent=2), encoding="utf-8")
        for output in logs:
            output.flush()
            logged = Path(output.name).read_bytes()
            require(all(private.encode() not in logged for private in (token, fixture.cookie, fixture.discarded_value)), "private fixture authentication data reached a process log")
        print(json.dumps({"result": "PASS", "productionEntrypoints": True, "unconfiguredAccountsChecked": 4,
                          "nativeQrGenerations": 3, "nativeQrPolls": 2, "nativeIdentityChecks": 3,
                          "driveLocalSetup": True, "cancelledReadyResultDidNotRevive": True,
                          "restartPollDidNotBegin": True, "publicCapturesSchemaValidated": True,
                          "captures": str(capture_path), "runtime": str(runtime)}))
        return 0
    except Exception as error:
        # Do not echo response bodies, subprocess output, private config or
        # synthetic cookie values when a fixture assertion fails.
        detail = str(error) if isinstance(error, AssertionError) else type(error).__name__
        print(json.dumps({"result": "FAIL", "stage": stage, "reason": detail, "runtime": str(runtime)}))
        return 1
    finally:
        fixture.release_result.set()
        for child in list(reversed(processes)):
            stop_process(child)
        for server in (proxy, platform):
            server.shutdown()
            server.server_close()
        for output in logs:
            output.close()


if __name__ == "__main__":
    raise SystemExit(main())
