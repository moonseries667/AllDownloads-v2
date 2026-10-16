import { spawn } from "node:child_process";
import { createWriteStream } from "node:fs";
import { access, mkdir, mkdtemp } from "node:fs/promises";
import { createServer } from "node:net";
import { resolve } from "node:path";
import { fileURLToPath } from "node:url";
import { build, preview } from "vite";

// Real Java + fresh SQLite, with no platform runner or account sessions.
// Only a newly generated internal token is owned by Java; this harness never reads it.
export default async function setup() {
  const root = fileURLToPath(new URL("..", import.meta.url));
  const java = process.env.ALLDOWNLOADS_TEST_JAVA;
  const jar = process.env.ALLDOWNLOADS_TEST_JAVA_JAR;
  if (!java || !jar) throw Error("Set ALLDOWNLOADS_TEST_JAVA and ALLDOWNLOADS_TEST_JAVA_JAR to an existing Java 21 executable and backend jar.");
  await Promise.all([access(java), access(jar)]);
  await mkdir(resolve(root, "node_modules/.cache"), { recursive: true });
  const runtime = await mkdtemp(resolve(root, "node_modules/.cache/java-browser-"));
  const reserved = createServer();
  await new Promise<void>(resolve => reserved.listen(0, "127.0.0.1", resolve));
  const port = (reserved.address() as { port: number }).port;
  await new Promise<void>(resolve => reserved.close(() => resolve()));
  const log = createWriteStream(resolve(runtime, "java.log"));
  const child = spawn(java, ["-Xms32m", "-Xmx256m", "-XX:ActiveProcessorCount=2", "-jar", jar, "--server.address=127.0.0.1", `--server.port=${port}`,
    `--alldownloads.database=${resolve(runtime, "business.db")}`,
    `--alldownloads.runner-token-file=${resolve(runtime, "runner.token")}`,
    "--alldownloads.download-root=", "--alldownloads.allowed-origins=http://127.0.0.1:5193"],
  { cwd: runtime, windowsHide: true, stdio: ["ignore", "pipe", "pipe"] });
  child.stdout.pipe(log); child.stderr.pipe(log);
  let startError: Error | undefined;
  child.on("error", error => { startError = error; });
  process.env.ALLDOWNLOADS_TEST_JAVA_PID = String(child.pid);
  process.env.ALLDOWNLOADS_TEST_JAVA_RUNTIME = runtime;
  let server: Awaited<ReturnType<typeof preview>> | undefined;
  try {
    const deadline = Date.now() + 90000;
    while (true) {
      if (startError) throw startError;
      if (child.exitCode !== null) throw Error(`Java exited (${child.exitCode}); inspect ${resolve(runtime, "java.log")}`);
      try {
        const result = await fetch(`http://127.0.0.1:${port}/api/v1/overview`, { signal: AbortSignal.timeout(1000) });
        if (result.ok) break;
      } catch { /* Wait only for this newly started isolated process. */ }
      if (Date.now() >= deadline) throw Error(`Java startup timed out; inspect ${resolve(runtime, "java.log")}`);
      await new Promise(resolve => setTimeout(resolve, 250));
    }
    process.env.VITE_API_MODE = "http";
    process.env.VITE_API_BASE = "/api/v1";
    process.env.DEV_JAVA_PROXY = `http://127.0.0.1:${port}`;
    await build({ root, mode: "production", logLevel: "warn" });
    server = await preview({ root, mode: "production", preview: { host: "127.0.0.1", port: 5193, strictPort: true } });
  } catch (error) {
    child.kill(); log.end(); throw error;
  }
  return async () => {
    await new Promise<void>(resolve => server!.httpServer.close(() => resolve()));
    if (child.exitCode === null) child.kill();
    log.end();
  };
}
