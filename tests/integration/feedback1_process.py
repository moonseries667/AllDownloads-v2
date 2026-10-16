"""Real Java + Go processes; synthetic loopback media only, never a platform account.

The harness generates its own private runner identity and passes it only to its child
environment. It never logs or returns credentials. Results/SQLite/media stay under
backend/java/target. Run after building both executables; see --help.
"""
import argparse
import collections
import http.client
import http.server
import json
import os
from pathlib import Path
import socket
import sqlite3
import subprocess
import tempfile
import threading
import time
import urllib.error
import urllib.request
import uuid

from validate_contracts import validate

ROOT = Path(__file__).resolve().parents[2]
NO_WINDOW = getattr(subprocess, "CREATE_NO_WINDOW", 0)


def free_port():
    with socket.socket() as sock:
        sock.bind(("127.0.0.1", 0))
        return sock.getsockname()[1]


def run(command, env=None, expected=0):
    process = subprocess.run([str(v) for v in command], env=env, capture_output=True,
                             timeout=60, creationflags=NO_WINDOW)
    if expected is not None and process.returncode != expected:
        raise AssertionError(f"Child exit {process.returncode}: {process.stderr.decode('utf-8', 'replace')[-1800:]}")
    return process


class MediaServer(http.server.BaseHTTPRequestHandler):
    files = {}
    counts = collections.Counter()

    def log_message(self, *args):
        pass

    def do_GET(self):
        type(self).counts[self.path] += 1
        value = self.files.get(self.path)
        if value is None:
            self.send_error(404)
            return
        self.send_response(200)
        self.send_header("Content-Length", str(len(value)))
        self.send_header("ETag", '"fixture-v1"')
        self.end_headers()
        self.wfile.write(value)


class LossProxy(http.server.BaseHTTPRequestHandler):
    java_port = 0
    drop_download_result = False
    types = {}
    dropped = 0
    captured_operation = None

    def log_message(self, *args):
        pass

    def do_POST(self):
        body = self.rfile.read(int(self.headers.get("Content-Length", "0")))
        client = http.client.HTTPConnection("127.0.0.1", type(self).java_port, timeout=15)
        headers = {"Content-Type": "application/json", "Authorization": self.headers.get("Authorization", "")}
        client.request("POST", self.path, body, headers)
        response = client.getresponse()
        raw = response.read()
        status = response.status
        client.close()
        if self.path.endswith("/leases/acquire") and status == 200:
            operation = json.loads(raw)["data"]["operation"]
            if operation:
                type(self).types[operation["operationId"]] = operation["operationType"]
                if operation["operationType"] == "DOWNLOAD_MEDIA":
                    type(self).captured_operation = operation
        if self.path.endswith("/result"):
            operation_id = self.path.split("/")[-2]
            if status == 200 and type(self).drop_download_result and self.types.get(operation_id) == "DOWNLOAD_MEDIA":
                # Java has durably committed; discard only the response to reproduce an acknowledgement loss.
                type(self).drop_download_result = False
                type(self).dropped += 1
                self.close_connection = True
                self.connection.shutdown(socket.SHUT_RDWR)
                self.connection.close()
                return
        self.send_response(status)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(raw)))
        self.end_headers()
        self.wfile.write(raw)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--java", required=True, type=Path)
    parser.add_argument("--runner", required=True, type=Path)
    parser.add_argument("--ffmpeg", required=True, type=Path)
    parser.add_argument("--ffprobe", required=True, type=Path)
    args = parser.parse_args()
    jar = ROOT / "backend/java/target/alldownloads-backend-0.1.0-SNAPSHOT.jar"
    work = Path(tempfile.mkdtemp(prefix="feedback1-", dir=ROOT / "backend/java/target"))
    default = work / "default"
    custom = work / "custom"
    default.mkdir()
    custom.mkdir()
    video, audio = work / "input-video.mp4", work / "input-audio.m4a"
    run([args.ffmpeg, "-hide_banner", "-loglevel", "error", "-f", "lavfi", "-i", "color=c=blue:s=64x64:r=10", "-t", "1", "-an", "-c:v", "mpeg4", video])
    run([args.ffmpeg, "-hide_banner", "-loglevel", "error", "-f", "lavfi", "-i", "sine=frequency=440:sample_rate=44100", "-t", "1", "-vn", "-c:a", "aac", audio])
    MediaServer.files = {"/video": video.read_bytes(), "/audio": audio.read_bytes(),
                         "/subtitle": json.dumps({"body": [{"from": 0, "to": 1, "content": "隔离验证"}]}).encode(),
                         "/danmaku": '<i><d p="0,1,25,16777215,0,0,0,1">fixture</d></i>'.encode()}
    media = http.server.ThreadingHTTPServer(("127.0.0.1", 0), MediaServer)
    proxy = http.server.ThreadingHTTPServer(("127.0.0.1", 0), LossProxy)
    for server in [media, proxy]:
        threading.Thread(target=server.serve_forever, daemon=True).start()
    java_port = free_port()
    LossProxy.java_port = java_port
    base = f"http://127.0.0.1:{java_port}"
    java_process = None
    java_log = open(work / "java.log", "ab")
    captures = []

    def api(method, path, data=None, headers=None, expected=200):
        request = urllib.request.Request(base + "/api/v1" + path,
                                         None if data is None else json.dumps(data).encode(), method=method,
                                         headers={"Content-Type": "application/json", **(headers or {})})
        try:
            response = urllib.request.urlopen(request, timeout=10)
        except urllib.error.HTTPError as error:
            response = error
        with response:
            body = json.loads(response.read())
            assert response.status == expected, (response.status, body)
        return body.get("data", body)

    def post(path, data, expected=202, key=None):
        return api("POST", path, data, {"Idempotency-Key": key or str(uuid.uuid4())}, expected)

    def start_java():
        nonlocal java_process
        java_process = subprocess.Popen([str(args.java), "-jar", str(jar), f"--server.port={java_port}",
                                         f"--alldownloads.database={work / 'business.db'}",
                                         f"--alldownloads.runner-token-file={work / 'runner.token'}",
                                         f"--alldownloads.download-root={default}", "--logging.level.root=ERROR"],
                                        stdout=java_log, stderr=java_log, creationflags=NO_WINDOW)
        deadline = time.monotonic() + 30
        while time.monotonic() < deadline:
            if java_process.poll() is not None:
                raise AssertionError("Java exited before becoming ready; inspect isolated java.log")
            try:
                api("GET", "/health")
                return
            except (urllib.error.URLError, ConnectionError):
                time.sleep(.15)
        raise AssertionError("Java did not become ready within 30 seconds")

    def stop_java():
        nonlocal java_process
        if java_process and java_process.poll() is None:
            java_process.terminate()
            java_process.wait(timeout=15)

    def once(expected=0):
        env = dict(os.environ)
        env["ALLDOWNLOADS_INTERNAL_TOKEN"] = (work / "runner.token").read_text().strip()
        return run([args.runner, "-config", work / "runner.json", "-once"], env=env, expected=expected)

    def capture(schema, value):
        validate(schema, value)
        captures.append({"schema": schema, "data": value})
        return value

    try:
        def metadata(external, suffix="", bad=False):
            tracks = []
            kinds = [("video", "VIDEO"), ("audio", "AUDIO"), ("subtitle", "SUBTITLE"), ("danmaku", "DANMAKU")]
            if bad:
                kinds = [("missing", "VIDEO")]
            for name, kind in kinds:
                tracks.append({"ID": name + suffix, "Kind": kind, "Availability": "AVAILABLE", "Duration": 1 if kind in ["VIDEO", "AUDIO"] else None,
                               "SubtitleType": "HUMAN" if kind == "SUBTITLE" else "NOT_APPLICABLE", "Language": "zh-CN" if kind == "SUBTITLE" else "",
                               "Source": {"URL": f"http://127.0.0.1:{media.server_port}/{name}", "Version": "fixture-v1", "Size": len(MediaServer.files['/' + name]) if '/' + name in MediaServer.files else None}})
            return {"ID": "content" + suffix, "ExternalID": external, "IdentifierType": "BVID", "Kind": "SINGLE", "Title": "隔离媒体" + suffix,
                    "OriginalURL": "https://www.bilibili.com/video/" + external,
                    "Units": [{"ID": "unit" + suffix, "ExternalID": "cid" + suffix, "Kind": "MAIN", "Title": "", "Index": 1, "Duration": 1, "Tracks": tracks}]}
        catalog = {"BVFixture": metadata("BVFixture"), "BVFailure": metadata("BVFailure", "-bad", True)}
        (work / "catalog.json").write_text(json.dumps(catalog), encoding="utf-8")
        (work / "runner.json").write_text(json.dumps({"runnerId": "integration-runner", "controlUrl": f"http://127.0.0.1:{proxy.server_port}",
                                                       "stateDir": str(work / "runner-state"), "allowedRoots": [str(work)], "roots": {"download-root": str(default)},
                                                       "ffmpeg": str(args.ffmpeg), "ffprobe": str(args.ffprobe), "isolatedTestMode": True, "fixtureCatalog": str(work / "catalog.json")}), encoding="utf-8")
        start_java()
        once()  # Default local target validation.
        assert api("GET", "/settings")["defaultTarget"]["status"] == "VALID"
        for external in catalog:
            post("/bilibili/sources", {"links": ["https://www.bilibili.com/video/" + external]})
            once()  # Metadata only; no media download.
        assert not MediaServer.counts
        assert api("GET", "/download-orders")["total"] == 0
        detail = capture("Content", api("GET", "/bilibili/contents/content"))
        target_request = post("/download-targets/validate", {"path": str(custom)})
        once()
        target = capture("Target", api("GET", "/download-targets/" + target_request["targetId"]))
        selection = {"mode": "EXPLICIT", "resource": "BILIBILI_CONTENTS", "ids": [detail["id"]],
                     "mediaSelections": [{"contentId": detail["id"], "contentRevision": detail["revision"], "trackIds": ["video", "audio", "subtitle", "danmaku"]}]}
        preview_request = {"platform": "BILIBILI", "selection": selection, "target": {"id": target["id"], "revision": target["revision"]}}
        preview_ref = post("/download-previews", preview_request)
        preview = capture("Preview", api("GET", "/download-previews/" + preview_ref["previewId"]))
        assert preview["archivePolicy"] == "NONE"
        assert len(preview["items"][0]["outputs"]) == 3
        order = post("/download-orders", {"previewId": preview["id"]}, key="order-stable")
        assert post("/download-orders", {"previewId": preview["id"]}, key="order-stable") == order
        LossProxy.drop_download_result = True
        failed_delivery = once(expected=None)
        assert failed_delivery.returncode != 0 and LossProxy.dropped == 1
        assert api("GET", "/download-orders/" + order["orderId"])["status"] == "SUCCEEDED"
        downloaded = dict(MediaServer.counts)
        outputs = preview["items"][0]["outputs"]
        for output in outputs:
            assert (custom / output["relativePath"]).is_file()
            assert not (default / output["relativePath"]).exists()
        muxed = custom / next(o["relativePath"] for o in outputs if o["kind"] == "MUXED")
        probe = json.loads(run([args.ffprobe, "-v", "error", "-show_streams", "-of", "json", muxed]).stdout)
        assert {s["codec_type"] for s in probe["streams"]} == {"video", "audio"}
        subtitle = custom / next(o["relativePath"] for o in outputs if o["kind"] == "SUBTITLE")
        assert json.loads(subtitle.read_text())["body"][0]["content"] == "隔离验证"
        stop_java()
        start_java()
        once()  # Replay Go receipt after both processes restart; no new media requests.
        assert dict(MediaServer.counts) == downloaded
        replayed = capture("Order", api("GET", "/download-orders/" + order["orderId"]))
        assert replayed["status"] == "SUCCEEDED" and replayed["archivePolicy"] == "NONE"
        with sqlite3.connect(work / "business.db") as db:
            assert db.execute("SELECT count(*) FROM operations WHERE type='UPLOAD_ARTIFACT'").fetchone()[0] == 0
        again = post("/download-previews", preview_request)
        reused = api("GET", "/download-previews/" + again["previewId"])
        assert reused["items"][0]["reasonCode"] == "LOCAL_VERIFIED"
        assert reused["items"][0]["archiveState"] != "VERIFIED"
        default_preview = post("/download-previews", {"platform": "BILIBILI", "selection": selection})
        assert api("GET", "/download-previews/" + default_preview["previewId"])["items"][0]["decision"] == "DOWNLOAD"
        bad = api("GET", "/bilibili/contents/content-bad")
        bad_preview = post("/download-previews", {"platform": "BILIBILI", "selection": {"mode": "EXPLICIT", "resource": "BILIBILI_CONTENTS", "ids": [bad["id"]]}, "target": {"id": target["id"], "revision": target["revision"]}})
        bad_order = post("/download-orders", {"previewId": bad_preview["previewId"]})
        once()
        task = api("GET", "/tasks/" + bad_order["taskIds"][0])
        assert task["status"] == "NEEDS_ACTION" and task["error"]["code"] != "RESOURCE_DELETED"
        assert api("GET", "/download-orders/" + bad_order["orderId"])["status"] == "NEEDS_ACTION"
        capture("Overview", api("GET", "/overview"))
        (work / "captures.json").write_text(json.dumps(captures, ensure_ascii=False), encoding="utf-8")
        print("PASS: real Java/Go processes, metadata-only sync, exact mux/subtitle/danmaku outputs, target isolation, lost-ack replay across Java/Go restart, NONE archive exclusion, and media failure.")
        print("Isolated evidence directory:", work)
    finally:
        stop_java()
        java_log.close()
        for server in [proxy, media]:
            server.shutdown()
            server.server_close()


if __name__ == "__main__":
    main()
