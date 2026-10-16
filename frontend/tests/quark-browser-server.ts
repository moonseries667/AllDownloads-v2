import { createServer } from "vite";
import { fileURLToPath } from "node:url";

// Keep Vite in this test process so Windows teardown does not rely on a
// shell/taskkill subprocess. The server only serves this isolated worktree.
export default async function setup() {
  const server = await createServer({
    mode: "demo",
    root: fileURLToPath(new URL("..", import.meta.url)),
    server: { host: "127.0.0.1", port: 5184, strictPort: true },
  });
  await server.listen();
  return async () => { await server.close(); };
}
