"""Real Java + Go processes, real local files, and a loopback Drive/media substitute.

No live account, remote platform, native dialog, deployment, or system configuration is used.
Runtime evidence is retained under Java target/ for diagnosis; only this process's children stop.
"""
import argparse
import hashlib
import json
import re
import socket
import subprocess
import threading
import time
import uuid
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from pathlib import Path
from urllib.error import HTTPError, URLError
from urllib.parse import parse_qs, unquote, urlparse
from urllib.request import Request, urlopen

ROOT = Path(__file__).resolve().parents[2]
FOLDER = "application/vnd.google-apps.folder"


def folder(identity, name, parent=None):
    return dict(id=identity, name=name, mimeType=FOLDER, parents=[] if parent is None else [parent],
                trashed=False, version="1", capabilities=dict(canListChildren=True, canAddChildren=True))


class Fixture:
    def __init__(self):
        self.objects = {"root-real": folder("root-real", "My Drive"), "archive-target": folder("archive-target", "Archive", "root-real")}
        self.sessions, self.uploads, self.downloads = {}, [], {}
        self.slow_started, self.slow_stopped = threading.Event(), threading.Event()
        self.slow_bytes, self.slow_total = 0, 8 * 1024 * 1024
        self.lock = threading.Lock()

    def paths(self, outputs):
        with self.lock:
            for output in outputs:
                parent = "archive-target"
                for name in output["relativePath"].split("/")[:-1]:
                    identity = "dir-" + hashlib.sha256((parent + "/" + name).encode()).hexdigest()[:20]
                    self.objects[identity] = folder(identity, name, parent)
                    parent = identity


def handler(fixture):
    class Handler(BaseHTTPRequestHandler):
        def log_message(self, *_):
            pass

        def reply(self, status, body, headers=None):
            raw = json.dumps(body).encode()
            self.send_response(status)
            self.send_header("Content-Type", "application/json")
            self.send_header("Content-Length", str(len(raw)))
            for key, value in (headers or {}).items():
                self.send_header(key, value)
            self.end_headers()
            self.wfile.write(raw)

        def do_GET(self):
            parsed = urlparse(self.path)
            if parsed.path.endswith(".xml"):
                with fixture.lock:
                    fixture.downloads[parsed.path] = fixture.downloads.get(parsed.path, 0) + 1
                slow = parsed.path == "/slow.xml"
                raw = (b"<i>" + b" " * (fixture.slow_total - 7) + b"</i>") if slow else b'<i><d p="0,1,25,1">fixture</d></i>'
                self.send_response(200)
                self.send_header("Content-Length", str(len(raw)))
                self.send_header("ETag", '"fixture-v1"')
                self.end_headers()
                if slow:
                    fixture.slow_started.set()
                try:
                    for offset in range(0, len(raw), 32768):
                        self.wfile.write(raw[offset:offset + 32768]); self.wfile.flush()
                        if slow:
                            fixture.slow_bytes += min(32768, len(raw) - offset)
                            time.sleep(.04)
                except (BrokenPipeError, ConnectionResetError, ConnectionAbortedError, OSError):
                    if slow:
                        fixture.slow_stopped.set()
                return
            if parsed.path == "/drive/v3/files/generateIds":
                return self.reply(200, {"ids": ["remote-" + uuid.uuid4().hex]})
            if parsed.path == "/drive/v3/files":
                query = parse_qs(parsed.query).get("q", [""])[0]
                parent = re.search(r"'([^']+)' in parents", query)
                name = re.search(r"name\s*=\s*'([^']+)'", query)
                with fixture.lock:
                    files = [obj for obj in fixture.objects.values() if obj.get("mimeType") == FOLDER
                             and (not parent or parent.group(1) in obj["parents"])
                             and (not name or obj["name"] == name.group(1))]
                return self.reply(200, {"files": files})
            if parsed.path.startswith("/drive/v3/files/"):
                identity = unquote(parsed.path.rsplit("/", 1)[1])
                identity = "root-real" if identity == "root" else identity
                with fixture.lock:
                    obj = fixture.objects.get(identity)
                return self.reply(200 if obj else 404, obj or {})
            self.reply(404, {})

        def do_POST(self):
            if urlparse(self.path).path != "/upload/drive/v3/files":
                return self.reply(404, {})
            data = json.loads(self.rfile.read(int(self.headers["Content-Length"])))
            identity = data["id"]
            with fixture.lock:
                fixture.sessions[identity] = data
            location = f"http://127.0.0.1:{self.server.server_port}/upload/drive/v3/files/{identity}"
            self.reply(200, {}, {"Location": location})

        def do_PUT(self):
            identity = urlparse(self.path).path.rsplit("/", 1)[1]
            raw = self.rfile.read(int(self.headers["Content-Length"]))
            with fixture.lock:
                obj = fixture.sessions[identity].copy()
                obj.update(mimeType="application/octet-stream", size=str(len(raw)), sha256Checksum=hashlib.sha256(raw).hexdigest(), trashed=False)
                fixture.objects[identity] = obj
                fixture.uploads.append(identity)
            self.reply(200, obj)
    return Handler


def wait_until(operation, predicate=lambda x: bool(x), seconds=40):
    deadline, last = time.monotonic() + seconds, None
    while time.monotonic() < deadline:
        try:
            last = operation()
            if predicate(last):
                return last
        except (URLError, ConnectionError, TimeoutError):
            pass
        time.sleep(.1)
    raise AssertionError(f"Timed out waiting for expected persisted state; last={last}")


def main():
    args = argparse.ArgumentParser()
    args.add_argument("--java", required=True)
    args.add_argument("--runner", required=True)
    args.add_argument("--ffmpeg-dir", required=True)
    config = args.parse_args()
    runtime = ROOT / "backend/java/target" / ("feedback3-chain-" + uuid.uuid4().hex[:10])
    runtime.mkdir(parents=True)
    download_root = runtime / "downloads"; download_root.mkdir()
    token_file = runtime / "fixture.token"
    token_file.write_text("integration-only-synthetic-token-" * 2)
    fixture = Fixture(); server = ThreadingHTTPServer(("127.0.0.1", 0), handler(fixture))
    threading.Thread(target=server.serve_forever, daemon=True).start()
    fixture_url = f"http://127.0.0.1:{server.server_port}"
    with socket.socket() as sock:
        sock.bind(("127.0.0.1", 0)); port = sock.getsockname()[1]
    base = f"http://127.0.0.1:{port}"

    def api(path, method="GET", body=None, revision=None):
        headers = {"Content-Type": "application/json"}
        if method == "POST": headers["Idempotency-Key"] = uuid.uuid4().hex
        if revision is not None: headers["If-Match"] = '"' + revision + '"'
        request = Request(base + "/api/v1" + path, None if body is None else json.dumps(body).encode(), headers, method=method)
        try:
            with urlopen(request, timeout=8) as response: return json.load(response)["data"]
        except HTTPError as error:
            raise AssertionError(f"{method} {path}: {error.code} {error.read().decode()}") from None

    catalog = {}
    for external, prefix, slow in [("BV1Cancel", "cancel", True), ("BV1Complete", "complete", False)]:
        catalog[external] = dict(ID=prefix + "-content", ExternalID=external, IdentifierType="BVID", Kind="MULTIPART", Title=prefix,
            OriginalURL="https://www.bilibili.com/video/" + external,
            Units=[dict(ID=prefix + "-unit-" + str(i), ExternalID=prefix + "-cid-" + str(i), Kind="PART", Index=i + 1, Title="Part " + str(i + 1),
                Tracks=[dict(ID=prefix + "-track-" + str(i), Kind="DANMAKU", Availability="AVAILABLE", Source=dict(URL=fixture_url + ("/slow.xml" if slow and i == 0 else "/" + prefix + str(i) + ".xml"), Version="fixture-v1"))]) for i in range(2)])
    catalog_file = runtime / "catalog.json"; catalog_file.write_text(json.dumps(catalog))
    runner_config = runtime / "runner.json"
    runner_config.write_text(json.dumps(dict(ControlURL=base, TokenFile=str(token_file), Root=str(download_root), StateDir=str(runtime / "runner"), Catalog=str(catalog_file), DriveURL=fixture_url, FFmpegDir=config.ffmpeg_dir)))
    flags = subprocess.CREATE_NO_WINDOW if hasattr(subprocess, "CREATE_NO_WINDOW") else 0
    children, logs = [], []

    def launch():
        java_log = open(runtime / ("java-" + str(len(logs)) + ".log"), "wb"); logs.append(java_log)
        children.append(subprocess.Popen([config.java, "-jar", str(ROOT / "backend/java/target/alldownloads-backend-0.1.0-SNAPSHOT.jar"),
            "--server.port=" + str(port), "--alldownloads.database=" + str(runtime / "business.db"), "--alldownloads.runner-token-file=" + str(token_file),
            "--alldownloads.download-root=" + str(download_root), "--logging.level.root=ERROR"], cwd=runtime, stdout=java_log, stderr=subprocess.STDOUT, creationflags=flags))
        wait_until(lambda: api("/health"))
        go_log = open(runtime / ("go-" + str(len(logs)) + ".log"), "wb"); logs.append(go_log)
        children.append(subprocess.Popen([config.runner, str(runner_config)], cwd=runtime, stdout=go_log, stderr=subprocess.STDOUT, creationflags=flags))
        return wait_until(lambda: api("/settings"), lambda s: s["defaultTarget"]["status"] == "VALID")

    def stop():
        for child in reversed(children):
            if child.poll() is None:
                child.terminate(); child.wait(timeout=10)
        children.clear()
        for log in logs: log.flush()

    def submit(external):
        api("/bilibili/sources", "POST", {"links": ["https://www.bilibili.com/video/" + external]})
        contents = wait_until(lambda: api("/bilibili/contents"), lambda p: any(c["externalId"] == external for c in p["items"]))
        content = next(c for c in contents["items"] if c["externalId"] == external)
        preview = api("/download-previews", "POST", dict(platform="BILIBILI", selection=dict(mode="EXPLICIT", resource="BILIBILI_CONTENTS", ids=[content["id"]])))
        manifest = api("/download-previews/" + preview["previewId"])
        fixture.paths([output for item in manifest["items"] for output in item["outputs"]])
        order = api("/download-orders", "POST", {"previewId": preview["previewId"]})
        return order["projectIds"][0]

    try:
        settings = launch()
        listing = wait_until(lambda: api("/drive/folders?accountId=fixture-drive"), lambda p: p["status"] == "READY")
        selected = next(f for f in listing["items"] if f["id"] == "archive-target")
        validation = api("/settings/validate", "POST", dict(downloadTarget=dict(id="default", revision=settings["defaultTarget"]["revision"]), archiveDestination=dict(accountId="fixture-drive", folderId=selected["id"], revision=selected["revision"])))
        wait_until(lambda: api("/settings/validations/" + validation["validationId"]), lambda v: v["status"] == "VALID")
        api("/settings", "PATCH", {"validationId": validation["validationId"]}, settings["revision"])
        cancelled = submit("BV1Cancel")
        assert fixture.slow_started.wait(20), "Go never started the slow HTTP body"
        current = api("/download-projects/" + cancelled)
        item = next(c for c in current["items"] if c["partId"] == "cancel-unit-0")
        accepted = api(f"/download-projects/{cancelled}/items/{item['id']}/actions", "POST", {"action": "CANCEL"}, item["revision"])
        assert accepted["desiredAction"] == "CANCEL" and accepted["status"] == "RUNNING"
        finished = wait_until(lambda: api("/download-projects/" + cancelled), lambda p: p["summary"]["cancelled"] == 1 and p["summary"]["completed"] == 1)
        assert fixture.slow_stopped.wait(5) and fixture.slow_bytes < fixture.slow_total, "Cancelled transport did not stop"
        assert finished["status"] == "PARTIAL" and finished["summary"]["total"] == 2
        assert api("/archives")["total"] == 0
        completed = submit("BV1Complete")
        archive = wait_until(lambda: api("/archives"), lambda p: len(p["items"]) == 1 and p["items"][0]["status"] == "SUCCEEDED", seconds=50)["items"][0]
        assert archive["projectId"] == completed and len(archive["files"]) == 2
        for file in archive["files"]:
            local = download_root / file["relativePath"]
            assert local.is_file() and hashlib.sha256(local.read_bytes()).hexdigest() == file["sha256"]
        assert len(fixture.uploads) == 2 and len(set(fixture.uploads)) == 2
        downloads_before = fixture.downloads.copy(); stop(); time.sleep(.2); launch(); time.sleep(.5)
        assert api("/download-projects/" + cancelled)["status"] == "PARTIAL"
        assert api("/archives")["total"] == 1 and len(fixture.uploads) == 2 and fixture.downloads == downloads_before
        print(json.dumps(dict(result="PASS", projects=2, cancelledChildren=1, verifiedUploads=1, uploadedFiles=2,
                              cancelledHttpBytes=fixture.slow_bytes, restartDidNotReplayIO=True, runtime=str(runtime))))
    finally:
        stop(); server.shutdown()
        for log in logs: log.close()


if __name__ == "__main__":
    main()
