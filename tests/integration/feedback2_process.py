"""Real Java + Go processes, real SQLite/files; only platform providers and transfer are fixtures."""
import argparse
import collections
import hashlib
import http.client
import http.server
import json
import os
from pathlib import Path
import socket
import subprocess
import threading
import time
import urllib.error
import urllib.request
import uuid

ROOT = Path(__file__).resolve().parents[2]
FLAGS = subprocess.CREATE_NO_WINDOW if os.name == "nt" else 0


def port():
    with socket.socket() as s:
        s.bind(("127.0.0.1", 0))
        return s.getsockname()[1]


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("--java", required=True)
    parser.add_argument("--runner", required=True)
    parser.add_argument("--ffmpeg-dir", required=True)
    args = parser.parse_args()
    run = ROOT / "backend/java/target/feedback2-e2e" / str(uuid.uuid4())
    run.mkdir(parents=True)
    downloads = run / "downloads"
    default, custom = downloads / "default", downloads / "custom"
    default.mkdir(parents=True)
    custom.mkdir()
    java_port = port()
    base = f"http://127.0.0.1:{java_port}"
    captures, operations = [], {}
    state = {"drop": True, "dropped": False}

    class Proxy(http.server.BaseHTTPRequestHandler):
        def log_message(self, *_):
            pass

        def do_POST(self):
            raw = self.rfile.read(int(self.headers.get("Content-Length", "0")))
            connection = http.client.HTTPConnection("127.0.0.1", java_port, timeout=20)
            connection.request("POST", self.path, raw, {"Content-Type": "application/json", "Authorization": self.headers.get("Authorization", "")})
            response = connection.getresponse()
            data = response.read()
            connection.close()
            if response.status == 200 and self.path.endswith("/leases/acquire"):
                operation = json.loads(data)["data"]["operation"]
                if operation:
                    operations[operation["operationId"]] = operation
                    captures.append({"schema": "Operation", "data": operation})
            if self.path.endswith("/result") and response.status == 200:
                request = json.loads(raw)
                operation = operations.get(self.path.split("/")[-2], {})
                kinds = {"SCAN_TEL_SAVED": "ScanResult", "LIST_QUARK_TREE": "TreeResult", "TRANSFER_QUARK_ITEMS": "TransferResult"}
                if request["status"] == "SUCCEEDED" and operation.get("operationType") in kinds:
                    captures.append({"schema": "part2:" + kinds[operation["operationType"]], "data": request["result"]})
                if state["drop"] and operation.get("operationType") == "DOWNLOAD_MEDIA" and request["status"] == "SUCCEEDED":
                    state.update(drop=False, dropped=True)
                    self.close_connection = True
                    return  # Java committed; simulate a lost acknowledgement, never log the private header.
            self.send_response(response.status)
            self.send_header("Content-Type", "application/json")
            self.send_header("Content-Length", str(len(data)))
            self.end_headers()
            self.wfile.write(data)

    proxy = http.server.ThreadingHTTPServer(("127.0.0.1", 0), Proxy)
    threading.Thread(target=proxy.serve_forever, daemon=True).start()

    def api(method, path, body=None, headers=None, expected=200):
        headers = dict(headers or {})
        headers["Content-Type"] = "application/json"
        if method == "POST":
            headers.setdefault("Idempotency-Key", str(uuid.uuid4()))
        request = urllib.request.Request(base + "/api/v1" + path, data=None if body is None else json.dumps(body).encode(), method=method, headers=headers)
        try:
            response = urllib.request.urlopen(request, timeout=20)
        except urllib.error.HTTPError as error:
            response = error
        data = json.load(response)
        assert response.status == expected, (path, response.status, data)
        return data.get("data", data)

    def run_command(command, env=None, allowed_failure=False):
        result = subprocess.run(command, env=env, capture_output=True, text=True, timeout=90, creationflags=FLAGS)
        if result.returncode and not allowed_failure:
            raise AssertionError((command[0], result.returncode, result.stdout[-2000:], result.stderr[-2000:]))
        return result

    ffmpeg = str(Path(args.ffmpeg_dir) / "ffmpeg.exe")
    ffprobe = str(Path(args.ffmpeg_dir) / "ffprobe.exe")
    audio, photo, document = run / "audio.wav", run / "photo.png", run / "notes.bin"
    run_command([ffmpeg, "-hide_banner", "-loglevel", "error", "-f", "lavfi", "-i", "sine=frequency=440:duration=0.15", str(audio)])
    run_command([ffmpeg, "-hide_banner", "-loglevel", "error", "-f", "lavfi", "-i", "color=c=red:s=16x16", "-frames:v", "1", str(photo)])
    document.write_bytes(b"quark-fixture")
    def media(key, name, kind, path=None):
        return {"ExternalID": key, "Name": name, kind: True, "MIMEType": {"Photo": "image/png", "Audio": "audio/wav", "Document": "application/octet-stream"}[kind], "SizeBytes": str(path.stat().st_size) if path else None}
    def message(mid, media_items=(), group=None):
        return {"ID": str(mid), "SentAt": f"2026-10-14T12:00:{mid:02d}Z", "Text": f"isolated message {mid}", "GroupID": group, "Media": list(media_items), "SourceLinks": [], "AuthorTags": []}
    messages = [message(1), message(2, [media("skip-photo", "photo.png", "Photo")]), message(3, [media("skip-pdf", "document.pdf", "Document")]), message(4, [media("skip-pic", "pic.png", "Photo"), media("skip-zip", "archive.zip", "Document")]), message(5, [media("audio", "audio.wav", "Audio", audio)], "album"), message(6, [media("photo", "photo.png", "Photo", photo)], "album"), message(7, [media("audio2", "audio.wav", "Audio", audio)])]
    def attachment_identity(mid, key):
        digest = hashlib.md5(f"tel-test\0{mid}\0{key}".encode()).digest()
        return str(uuid.UUID(bytes=digest, version=3))
    fail = max(((attachment_identity(5, "audio"), "audio"), (attachment_identity(6, "photo"), "photo")))[1]
    def folder(nid, parent, name):
        return {"ID": nid, "ParentID": parent, "Name": name, "Kind": "FOLDER", "Revision": "v1", "Availability": "AVAILABLE", "Writable": True}
    nodes = [folder("0", None, ""), folder("target", "0", "课程"), folder("managed", "target", "转存目录"), {"ID": "qfile", "ParentID": "managed", "Name": "notes.bin", "Kind": "FILE", "Revision": "q-v1", "Availability": "AVAILABLE", "SizeBytes": str(document.stat().st_size)}]
    fixture = {"ControlURL": f"http://127.0.0.1:{proxy.server_port}", "StateDir": str(run / "runner"), "Root": str(downloads), "FFmpeg": ffmpeg, "FFprobe": ffprobe, "FailOnce": fail, "Messages": messages, "Nodes": nodes, "Files": {"audio": str(audio), "audio2": str(audio), "photo": str(photo), "qfile": str(document)}}
    fixture_path = run / "fixture.json"
    fixture_path.write_text(json.dumps(fixture), encoding="utf-8")
    log = (run / "java.log").open("w", encoding="utf-8")
    command = [args.java, "-jar", str(ROOT / "backend/java/target/alldownloads-backend-0.1.0-SNAPSHOT.jar"), f"--server.port={java_port}", f"--alldownloads.database={run / 'business.db'}", f"--alldownloads.runner-token-file={run / 'runner.token'}", f"--alldownloads.download-root={default}", "--logging.level.root=ERROR", "--spring.main.banner-mode=off"]
    java = subprocess.Popen(command, stdout=log, stderr=subprocess.STDOUT, creationflags=FLAGS)
    try:
        for _ in range(100):
            try:
                api("GET", "/health")
                break
            except (OSError, AssertionError):
                if java.poll() is not None:
                    raise AssertionError("Java exited before health became available")
                time.sleep(0.1)
        else:
            raise AssertionError("Java startup timed out")
        env = dict(os.environ)
        env["ALLDOWNLOADS_TEST_TOKEN"] = (run / "runner.token").read_text().strip()
        def once():
            dropped_before = state["dropped"]
            result = run_command([args.runner, "-fixture", str(fixture_path)], env, allowed_failure=True)
            assert result.returncode == 0 or (state["dropped"] and not dropped_before), (result.returncode, result.stderr[-2000:])
        once()  # actual Go target validation
        assert api("GET", "/settings")["defaultTarget"]["status"] == "VALID"
        assert api("GET", "/telegram/messages")["items"] == []
        api("POST", "/telegram/saved-sync", {"accountId": "tel-test"}, expected=202)
        for _ in range(6):
            once()
        saved = api("GET", "/telegram/messages")["items"]
        assert len(saved) == 2 and sum(len(m["attachments"]) for m in saved) == 3
        issues = api("GET", "/issues")["items"]
        assert len(issues) == 1 and issues[0]["code"] == "NETWORK_TEMPORARY", issues
        acknowledged = api("POST", "/issues/" + issues[0]["id"] + "/acknowledge")
        assert acknowledged["read"] and not acknowledged["resolved"]
        captures.append({"schema": "Issue", "data": acknowledged})
        api("POST", "/telegram/retry-incomplete", {"accountId": "tel-test"}, expected=202)
        once()
        once()
        assert api("GET", "/issues")["items"] == []
        saved = api("GET", "/telegram/messages")["items"]
        assert all(m["downloadSummary"]["completed"] == len(m["attachments"]) for m in saved)
        for m in saved:
            captures.append({"schema": "SavedMessage", "data": m})
        watcher = api("GET", "/telegram/watcher?accountId=tel-test")
        assert watcher["scanState"]["cursorMessageId"] == "7"
        captures.append({"schema": "SavedWatcher", "data": watcher})
        before = (run / "runner/opens.log").read_text().splitlines()
        api("POST", "/telegram/saved-sync", {"accountId": "tel-test"}, expected=202)
        once()
        assert before == (run / "runner/opens.log").read_text().splitlines()
        assert collections.Counter(before) == {"audio": 1, "photo": 1, "audio2": 1}, before
        assert len(list(default.rglob("*.metadata.json"))) == 3
        assert state["dropped"], "ack-loss recovery was not exercised"
        loading = api("GET", "/quark/folders?accountId=quark-test")
        assert loading["status"] == "LOADING"
        once()
        folders = api("GET", "/quark/folders?accountId=quark-test")
        assert folders["parent"]["name"] == "我的云盘" and folders["parent"]["path"] == ""
        captures.append({"schema": "FolderListing", "data": folders})
        batch = api("POST", "/quark/shares", {"accountId": "quark-test", "entries": [{"url": "https://pan.quark.cn/s/abcd12345678", "targetFolderId": "target", "targetFolderRevision": "v1"}]}, expected=202)
        share = batch["items"][0]["shareId"]
        once()  # fixture transfer with real target validation; never a real cloud write
        once()  # real shared tree adapter, information only
        assert api("GET", "/download-orders")["items"] == []
        target = api("POST", "/download-targets/validate", {"path": str(custom)}, expected=202)
        once()
        preview_id = api("POST", "/download-previews", {"platform": "QUARK", "selection": {"mode": "EXPLICIT", "resource": "QUARK_NODES", "ids": ["qfile"]}, "target": {"id": target["targetId"], "revision": "1"}}, expected=202)["previewId"]
        preview = api("GET", "/download-previews/" + preview_id)
        assert preview["archivePolicy"] == "NONE" and preview["items"][0]["sizeBytes"] == str(document.stat().st_size)
        captures.append({"schema": "Preview", "data": preview})
        fixture["Nodes"].append({**nodes[-1], "ID": "new", "Name": "new.bin"})
        fixture["Files"]["new"] = str(document)
        fixture_path.write_text(json.dumps(fixture), encoding="utf-8")
        api("POST", "/quark/shares/" + share + "/refresh-tree", {}, expected=202)
        once()
        order_id = api("POST", "/download-orders", {"previewId": preview_id}, expected=202)["orderId"]
        once()
        assert (custom / "Quark/12345678/notes.bin").read_bytes() == document.read_bytes()
        assert not list(custom.rglob("new.bin"))
        order = api("GET", "/download-orders/" + order_id)
        assert order["status"] == "SUCCEEDED" and order["archivePolicy"] == "NONE"
        captures.append({"schema": "Order", "data": order})
        tasks = api("GET", "/tasks")["items"]
        assert not any(t["platform"] == "QUARK" and t["kind"] == "UPLOAD" for t in tasks)
        assert api("GET", "/quark/shares/" + share)["targetFolderId"] == "target"
        java.terminate()
        java.wait(timeout=10)
        java = subprocess.Popen(command, stdout=log, stderr=subprocess.STDOUT, creationflags=FLAGS)
        for _ in range(100):
            try:
                assert api("GET", "/download-orders/" + order_id)["status"] == "SUCCEEDED"
                break
            except OSError:
                time.sleep(0.1)
        once()
        assert not any(t["platform"] == "QUARK" and t["kind"] == "UPLOAD" for t in api("GET", "/tasks")["items"])
        capture_path = ROOT / "backend/java/target/feedback2-process-captures.json"
        capture_path.write_text(json.dumps(captures, ensure_ascii=False, indent=2), encoding="utf-8")
        print("PASS: Java-Go scan/group filtering, actual mixed files/sidecars, known partial retry, lost-ack replay, incremental dedup, folder/target mapping, Quark frozen NONE order and Java restart")
        print("Fixture transfer only; no real accounts/platform requests/deployment. Captures:", capture_path)
    finally:
        if java.poll() is None:
            java.terminate()
            java.wait(timeout=10)
        proxy.shutdown()
        proxy.server_close()
        log.close()


if __name__ == "__main__":
    main()
