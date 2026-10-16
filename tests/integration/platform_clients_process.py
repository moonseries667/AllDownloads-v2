"""Production Java/Go entrypoints against isolated native HTTP and tdl protocols.

No live accounts, business media, deployment, or external writes are used.
The tdl protocol process is deliberately a test substitute, not a Telegram login.
"""
import argparse
import collections
import copy
import hashlib
import http.client
import json
import os
from pathlib import Path
import socket
import subprocess
import threading
import time
import uuid
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from urllib.error import HTTPError, URLError
from urllib.parse import parse_qs, urlparse
from urllib.request import Request, urlopen

ROOT = Path(__file__).resolve().parents[2]

def wait_for(fn, check=lambda value: bool(value), timeout=60):
    end = time.monotonic() + timeout
    last = None
    while time.monotonic() < end:
        try:
            last = fn()
            if check(last): return last
        except (URLError, ConnectionError, TimeoutError): pass
        time.sleep(.1)
    raise AssertionError(f"expected persisted state did not appear: {last}")

def free_port():
    with socket.socket() as sock:
        sock.bind(("127.0.0.1", 0)); return sock.getsockname()[1]

class Fixture:
    def __init__(self, audio, photo, slow):
        self.lock = threading.RLock()
        self.audio, self.photo, self.slow = audio, photo, slow
        self.messages = [self.message(1), self.message(2, "PHOTO"), self.message(3, "DOCUMENT"),
                         self.message(4, "PHOTO", 77), self.message(5, "AUDIO", 77)]
        self.tel_calls, self.exports = collections.Counter(), []
        self.slow_started, self.slow_stopped = threading.Event(), threading.Event()
        self.slow_bytes = 0
        self.source = [self.entry("source-1", "0", "lesson.bin", b"isolated-quark-v1")]
        self.objects = {"target": dict(fid="target", pdir_fid="0", file_name="Selected", dir=True, file=False, updated_at=1700000000000)}
        self.bytes = {"source-1": b"isolated-quark-v1"}
        self.tasks, self.saves, self.quark_downloads = {}, [], collections.Counter()
        self.hide_copy, self.lose_save, self.fail_download = True, True, True
        self.drop_ack, self.ack_dropped = False, False
        self.captures, self.operations = [], {}
        self.fact_sequences=collections.Counter()
        self.protocol_errors=[]

    def entry(self, identity, parent, name, data):
        return dict(fid=identity, pdir_fid=parent, file_name=name, dir=False, file=True,
                    size=len(data), updated_at=1700000000000, sha1=hashlib.sha1(data).hexdigest(),
                    md5=hashlib.md5(data).hexdigest(), share_fid_token="isolated-source-token")

    def message(self, identity, kind=None, group=0):
        sent = 1700000000 + identity
        body = dict(ID=identity, Date=sent, Message=f"synthetic message {identity}", GroupedID=group, Media=None)
        file = ""
        if kind == "PHOTO":
            file = f"photo-{identity}.png"
            body["Media"] = dict(Photo=dict(ID=4000 + identity, Sizes=[dict(Type="w", Size=len(self.photo), W=32, H=32)]))
        if kind in ("AUDIO", "DOCUMENT"):
            file = "cancel.wav" if identity == 6 else f"audio-{identity}.wav" if kind == "AUDIO" else "looks-like-video.mp4"
            data = self.slow if identity == 6 else self.audio if kind == "AUDIO" else b"synthetic ordinary document"
            body["Media"] = dict(Document=dict(ID=5000 + identity, Size=len(data), MimeType="audio/wav" if kind == "AUDIO" else "application/pdf", Attributes=[dict(FileName=file)]))
        return dict(id=identity, file=file, date=sent, text=body["Message"], raw=body)

def platform_handler(f):
    class Handler(BaseHTTPRequestHandler):
        def log_message(self, *_): pass
        def reply(self, data, status=200, headers=None):
            raw = json.dumps(data).encode()
            self.send_response(status); self.send_header("Content-Type", "application/json")
            self.send_header("Content-Length", str(len(raw)))
            for key, value in (headers or {}).items(): self.send_header(key, value)
            self.end_headers(); self.wfile.write(raw)
        def envelope(self, data, total=None):
            value = dict(status=200, code=0, data=data)
            if total is not None: value["metadata"] = {"_total": total}
            self.reply(value)
        def do_GET(self):
            parsed = urlparse(self.path); query = parse_qs(parsed.query)
            path = parsed.path
            if path == "/telegram/export":
                low, high = map(int, query["range"][0].split(","))
                with f.lock:
                    f.exports.append(low)
                    messages = [copy.deepcopy(m) for m in f.messages if low <= m["id"] <= high]
                self.reply(dict(id=1001, messages=messages)); return
            if path.startswith("/telegram/file/"):
                identity = int(path.rsplit("/", 1)[1])
                with f.lock: f.tel_calls[identity] += 1
                data = f.slow if identity == 6 else f.photo if identity in (2, 4, 7) else f.audio
                self.send_response(200); self.send_header("Content-Length", str(len(data))); self.end_headers()
                if identity == 6:
                    f.slow_started.set()
                    try:
                        for offset in range(0, len(data), 2048):
                            self.wfile.write(data[offset:offset+2048]); self.wfile.flush()
                            with f.lock: f.slow_bytes += len(data[offset:offset+2048])
                            time.sleep(.05)
                    except (BrokenPipeError, ConnectionResetError, ConnectionAbortedError): pass
                    finally: f.slow_stopped.set()
                else: self.wfile.write(data)
                return
            if path.startswith("/quark/file/"):
                identity = path.rsplit("/", 1)[1]
                with f.lock:
                    f.quark_downloads[identity] += 1
                    if f.fail_download:
                        f.fail_download = False; self.reply({}, 503); return
                    data = f.bytes[identity]
                self.send_response(200); self.send_header("Content-Length", str(len(data))); self.end_headers(); self.wfile.write(data); return
            if self.headers.get("Cookie") != "sid=isolated-cookie": self.reply({}, 401); return
            with f.lock:
                if path == "/account/info": self.envelope(dict(uid="quark-user")); return
                if path.endswith("/file/info"):
                    value = f.objects.get(query["fid"][0])
                    if value is None: self.reply({}, 404)
                    else: self.envelope(value)
                    return
                if path.endswith("/file/sort"):
                    parent = query["pdir_fid"][0]
                    nodes = [v for k, v in f.objects.items() if v["pdir_fid"] == parent and not (f.hide_copy and k.startswith("copy-"))]
                    start = (int(query["_page"][0]) - 1) * 50
                    self.envelope(dict(list=nodes[start:start+50]), len(nodes)); return
                if path.endswith("/sharepage/detail"):
                    self.envelope(dict(list=f.source if query["pdir_fid"][0] == "0" else []), len(f.source) if query["pdir_fid"][0] == "0" else 0); return
                if path.endswith("/task"):
                    self.envelope(dict(status=2, save_as=dict(save_as_top_fids=f.tasks[query["task_id"][0]]))); return
            self.reply({}, 404)
        def do_POST(self):
            path = urlparse(self.path).path
            if self.headers.get("Cookie") != "sid=isolated-cookie": self.reply({}, 401); return
            body = json.loads(self.rfile.read(int(self.headers.get("Content-Length", "0"))))
            with f.lock:
                if path.endswith("/sharepage/token"): self.envelope(dict(stoken="isolated-share-token")); return
                if path.endswith("/sharepage/save"):
                    assert body["to_pdir_fid"] == "target" and len(body["fid_list"]) == 1
                    source = next(s for s in f.source if s["fid"] == body["fid_list"][0])
                    identity = "copy-" + str(len(f.saves) + 1); copied = copy.deepcopy(source)
                    copied.update(fid=identity, pdir_fid=body["to_pdir_fid"])
                    f.objects[identity] = copied; f.bytes[identity] = f.bytes[source["fid"]]
                    f.saves.append(source["fid"]); task = "task-" + identity; f.tasks[task] = [identity]
                    if f.lose_save:
                        f.lose_save = False; self.close_connection = True; return
                    self.envelope(dict(task_id=task)); return
                if path.endswith("/file/download"):
                    identity = body["fids"][0]
                    self.envelope([dict(fid=identity, download_url=f"http://127.0.0.1:{self.server.server_port}/quark/file/{identity}")]); return
            self.reply({}, 404)
    return Handler

def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("--java", required=True); parser.add_argument("--runner", required=True)
    parser.add_argument("--tdl-protocol", required=True); parser.add_argument("--ffmpeg-dir", required=True)
    args = parser.parse_args()
    runtime = ROOT / "backend/java/target" / ("platform-clients-" + uuid.uuid4().hex[:10]); runtime.mkdir(parents=True)
    root, custom, session = runtime / "downloads", runtime / "custom", runtime / "new-session"
    for directory in (root, custom, session): directory.mkdir()
    audio, photo, slow = runtime / "audio.wav", runtime / "photo.png", runtime / "slow.wav"
    ffmpeg = str(Path(args.ffmpeg_dir) / "ffmpeg.exe")
    for target, options in [(audio, ["-f", "lavfi", "-i", "sine=frequency=440:duration=1", "-ar", "8000"]),
                            (slow, ["-f", "lavfi", "-i", "sine=frequency=440:duration=120", "-ar", "8000"]),
                            (photo, ["-f", "lavfi", "-i", "color=c=red:s=32x32", "-frames:v", "1"])]:
        subprocess.run([ffmpeg, "-v", "error", *options, str(target)], check=True, capture_output=True)
    f = Fixture(audio.read_bytes(), photo.read_bytes(), slow.read_bytes())
    server = ThreadingHTTPServer(("127.0.0.1", 0), platform_handler(f)); threading.Thread(target=server.serve_forever, daemon=True).start()
    fixture_url = f"http://127.0.0.1:{server.server_port}"
    java_port = free_port(); base = f"http://127.0.0.1:{java_port}"
    token = "synthetic-internal-token-" * 3; token_file = runtime / "internal.token"; token_file.write_text(token)
    cookie = runtime / "quark-cookie.local"; cookie.write_text("sid=isolated-cookie")

    class Proxy(BaseHTTPRequestHandler):
        def log_message(self, *_): pass
        def do_POST(self):
            body = self.rfile.read(int(self.headers.get("Content-Length", "0")))
            conn = http.client.HTTPConnection("127.0.0.1", java_port, timeout=15)
            conn.request("POST", self.path, body, {"Content-Type":"application/json", "Authorization":"Bearer " + token})
            response = conn.getresponse(); raw = response.read(); conn.close()
            parsed, request = json.loads(raw), json.loads(body)
            path = self.path.replace("/internal/v1", "")
            for name in ("heartbeat",):
                if path.startswith("/leases/") and path.endswith("/"+name): path = "/leases/{id}/heartbeat"
            if path.startswith("/operations/"): path = "/operations/{id}/" + path.rsplit("/", 1)[1]
            f.captures.append(dict(path=path, request=request, response=parsed, status=response.status))
            if path == "/leases/acquire" and response.status == 200 and parsed["data"]["operation"]:
                operation = parsed["data"]["operation"]; f.operations[operation["operationId"]] = operation
                # Exercise actual facts HTTP with the issued proof; no effects are
                # committed by facts. Production Go still commits through result.
                f.fact_sequences[operation["operationId"]]+=1
                facts = dict(eventId="facts-"+operation["leaseId"], runnerId=operation["runnerId"], leaseId=operation["leaseId"], attempt=operation["attempt"], fencingToken=operation["fencingToken"], sequence=f.fact_sequences[operation["operationId"]], facts=dict(phase="acquired"))
                try:
                    with urlopen(Request(base+"/internal/v1/operations/"+operation["operationId"]+"/facts", json.dumps(facts).encode(), {"Content-Type":"application/json", "Authorization":"Bearer "+token}), timeout=10) as reply:
                        f.captures.append(dict(path="/operations/{id}/facts", request=facts, response=json.load(reply), status=reply.status))
                except HTTPError as error:
                    f.protocol_errors.append(dict(status=error.code,error=json.loads(error.read())))
            if path == "/runners/register" and response.status == 200:
                parsed["data"]["heartbeatSeconds"] = 1; raw = json.dumps(parsed).encode()
            operation = f.operations.get(self.path.split("/")[-2], {}) if path.endswith("/result") else {}
            if response.status == 200 and f.drop_ack and operation.get("operationType") == "DOWNLOAD_MEDIA" and operation.get("platform") == "QUARK" and request.get("status") == "SUCCEEDED":
                f.drop_ack = False; f.ack_dropped = True; self.close_connection = True; return
            self.send_response(response.status); self.send_header("Content-Type", "application/json"); self.send_header("Content-Length", str(len(raw))); self.end_headers(); self.wfile.write(raw)

    proxy = ThreadingHTTPServer(("127.0.0.1", 0), Proxy); threading.Thread(target=proxy.serve_forever, daemon=True).start()
    config = runtime / "runner.local.json"
    config.write_text(json.dumps(dict(runnerId="production-runner", controlUrl=f"http://127.0.0.1:{proxy.server_port}", tokenEnv="ALLDOWNLOADS_INTERNAL_TOKEN", stateDir=str(runtime/"runner"), allowedRoots=[str(runtime)], roots={"download-root":str(root)}, ffmpeg=ffmpeg, ffprobe=str(Path(args.ffmpeg_dir)/"ffprobe.exe"), isolatedTestMode=True,
        telegram=dict(executable=args.tdl_protocol, accounts={"tel-test":dict(namespace="isolated",storageDir=str(session),peerId="1001")}),
        quarkAccounts={"quark-test":dict(cookieFile=str(cookie),expectedUserId="quark-user",baseUrl=fixture_url,accountUrl=fixture_url+"/account/info")})), encoding="utf-8")
    processes, logs = [], []
    flags = subprocess.CREATE_NO_WINDOW if hasattr(subprocess, "CREATE_NO_WINDOW") else 0
    def api(path, method="GET", body=None, revision=None):
        headers = {"Content-Type":"application/json"}
        if method == "POST": headers["Idempotency-Key"] = uuid.uuid4().hex
        if revision is not None: headers["If-Match"] = '"'+revision+'"'
        try:
            with urlopen(Request(base+"/api/v1"+path, None if body is None else json.dumps(body).encode(), headers, method=method), timeout=10) as response: return json.load(response)["data"]
        except HTTPError as error: raise AssertionError(f"{method} {path}: {error.code} {error.read().decode()}") from None
    def start_go():
        output = open(runtime/f"go-{len(logs)}.log", "wb"); logs.append(output)
        environment = dict(os.environ, ALLDOWNLOADS_INTERNAL_TOKEN=token, ALLDOWNLOADS_TEST_TDL_URL=fixture_url)
        child = subprocess.Popen([args.runner,"-config",str(config)], cwd=runtime, env=environment, stdout=output, stderr=subprocess.STDOUT, creationflags=flags)
        processes.append(child); return child
    def start():
        output = open(runtime/f"java-{len(logs)}.log", "wb"); logs.append(output)
        processes.append(subprocess.Popen([args.java,"-jar",str(ROOT/"backend/java/target/alldownloads-backend-0.1.0-SNAPSHOT.jar"),"--server.port="+str(java_port),"--alldownloads.database="+str(runtime/"business.db"),"--alldownloads.runner-token-file="+str(token_file),"--alldownloads.download-root="+str(root),"--logging.level.root=ERROR"],cwd=runtime,stdout=output,stderr=subprocess.STDOUT,creationflags=flags))
        wait_for(lambda: api("/health")); child=start_go()
        wait_for(lambda: api("/settings"), lambda s:s["defaultTarget"]["status"]=="VALID")
        return child
    def stop():
        for child in reversed(processes):
            if child.poll() is None: child.terminate(); child.wait(timeout=10)
        processes.clear()
        for output in logs: output.flush()
    def project(identity): return api("/download-projects/"+identity)
    try:
        go=start(); time.sleep(.3)
        assert not f.exports and not f.tel_calls, "ordinary startup accessed Telegram"
        api("/telegram/saved-sync","POST",dict(accountId="tel-test"))
        first=wait_for(lambda: api("/download-projects?platform=TELEGRAM"),lambda p:len(p["items"])==1 and p["items"][0]["status"]=="SUCCEEDED")["items"][0]
        assert first["summary"]["total"]==2 and api("/telegram/messages?accountId=tel-test")["total"]==1
        assert f.tel_calls=={4:1,5:1}, f.tel_calls
        assert len([p for p in root.rglob("*.metadata.json")])==2
        watcher=api("/telegram/watcher?accountId=tel-test"); api("/telegram/watcher","PATCH",dict(accountId="tel-test",enabled=True),watcher["revision"])
        with f.lock: f.messages += [f.message(6,"AUDIO",88),f.message(7,"PHOTO",88)]
        api("/telegram/saved-sync","POST",dict(accountId="tel-test"))
        assert f.slow_started.wait(30), "frozen attachment did not open its real stream"
        second=wait_for(lambda: api("/download-projects?platform=TELEGRAM"),lambda p:len(p["items"])==2)["items"]
        second=next(p for p in second if p["id"]!=first["id"])
        item=next(c for c in second["items"] if "cancel.wav" in c["title"])
        api(f"/download-projects/{second['id']}/items/{item['id']}/actions","POST",dict(action="CANCEL"),item["revision"])
        stopped=wait_for(lambda: project(second["id"]),lambda p:p["summary"]["cancelled"]==1 and p["summary"]["completed"]==1)
        assert f.slow_stopped.wait(5) and f.slow_bytes<len(f.slow) and stopped["status"]=="PARTIAL"
        orders_before_transfer=api("/download-orders")["total"]
        folders=wait_for(lambda: api("/quark/folders?accountId=quark-test"),lambda p:p["status"]=="READY")
        target=next(n for n in folders["items"] if n["id"]=="target")
        added=api("/quark/shares","POST",dict(accountId="quark-test",entries=[dict(url="https://pan.quark.cn/s/abcd12345678",targetFolderId=target["id"],targetFolderRevision=target["revision"])]))
        share=added["items"][0]; task=wait_for(lambda: api("/tasks/"+share["taskId"]),lambda t:t["status"]=="NEEDS_ACTION")
        assert task["error"]["code"]=="RESULT_UNKNOWN" and "RETRY" not in task["allowedActions"] and len(f.saves)==1
        assert api("/download-orders")["total"]==orders_before_transfer and api("/download-projects?platform=QUARK")["total"]==0
        api("/tasks/"+task["id"]+"/actions","POST",dict(action="RECONCILE"),task["revision"])
        checked=wait_for(lambda: api("/tasks"),lambda p:any(t["phase"]=="RECONCILE_QUARK_TRANSFER" and t["status"]=="NEEDS_ACTION" for t in p["items"]))
        assert len(f.saves)==1 and api("/tasks/"+task["id"])["status"]=="NEEDS_ACTION"
        f.hide_copy=False
        task=api("/tasks/"+task["id"])
        api("/tasks/"+task["id"]+"/actions","POST",dict(action="RECONCILE"),task["revision"])
        wait_for(lambda: api("/tasks/"+task["id"]),lambda t:t["status"]=="SUCCEEDED")
        nodes=wait_for(lambda: api("/quark/nodes?shareId="+share["shareId"]),lambda p:len(p["items"])==1)["items"]
        assert len(f.saves)==1 and api("/download-orders")["total"]==orders_before_transfer
        local=api("/download-targets/validate","POST",dict(path=str(custom)))
        local=wait_for(lambda: api("/download-targets/"+local["targetId"]),lambda t:t["status"]=="VALID")
        preview=api("/download-previews","POST",dict(platform="QUARK",selection=dict(mode="EXPLICIT",resource="QUARK_NODES",ids=[nodes[0]["id"]]),target=dict(id=local["id"],revision=local["revision"])))
        shown=api("/download-previews/"+preview["previewId"]); assert shown["archivePolicy"]=="NONE"
        order=api("/download-orders","POST",dict(previewId=preview["previewId"]))
        failed=wait_for(lambda: api("/tasks/"+order["taskIds"][0]),lambda t:t["status"]=="NEEDS_ACTION")
        assert "RETRY" in failed["allowedActions"]
        f.drop_ack=True
        api("/tasks/"+failed["id"]+"/actions","POST",dict(action="RETRY"),failed["revision"])
        wait_for(lambda: go.poll() is not None); assert f.ack_dropped
        go=start_go()
        complete=wait_for(lambda: project(order["projectIds"][0]),lambda p:p["status"]=="SUCCEEDED")
        assert api("/archives")["total"]==0 and f.quark_downloads["copy-1"]==2
        assert len(list(custom.rglob("lesson.bin")))==1
        with f.lock:
            data=b"isolated-quark-new-file"; f.bytes["source-2"]=data; f.source.append(f.entry("source-2","0","new.bin",data))
        api("/quark/shares/"+share["shareId"]+"/refresh","POST",{})
        wait_for(lambda: api("/quark/nodes?shareId="+share["shareId"]),lambda p:len(p["items"])==2)
        assert f.saves==["source-1","source-2"] and api("/download-orders?platform=QUARK")["total"]==1
        assert len(complete["items"])==1 and len(list(custom.rglob("new.bin")))==0
        with f.lock: f.messages.append(f.message(8,"AUDIO"))
        wait_for(lambda: api("/download-projects?platform=TELEGRAM"),lambda p:len(p["items"])==3 and all(x["status"] in ("SUCCEEDED","PARTIAL") for x in p["items"]),timeout=90)
        watcher=api("/telegram/watcher?accountId=tel-test"); api("/telegram/watcher","PATCH",dict(accountId="tel-test",enabled=False),watcher["revision"])
        assert f.tel_calls[6]==1 and project(second["id"])["status"]=="PARTIAL" and any(low==8 for low in f.exports)
        counts=(dict(f.tel_calls),dict(f.quark_downloads),list(f.saves)); stop(); time.sleep(.2); start(); time.sleep(2.5)
        assert counts==(dict(f.tel_calls),dict(f.quark_downloads),list(f.saves)) and project(second["id"])["status"]=="PARTIAL"
        assert not f.protocol_errors, f.protocol_errors
        captures=runtime/"runner-http.json";captures.write_text(json.dumps(f.captures,ensure_ascii=False,indent=2),encoding="utf-8")
        print(json.dumps(dict(result="PASS",productionEntrypoints=True,telegramQualifiedProjects=3,telegramCancelledChildren=1,quarkSaveCalls=len(f.saves),quarkDownloadOrders=1,unknownTransferReconciledWithoutResend=True,lostAckReplayed=True,restartDidNotRepeatIO=True,captures=str(captures),runtime=str(runtime))))
    finally:
        stop(); server.shutdown(); proxy.shutdown()
        for output in logs: output.close()

if __name__=="__main__": main()
