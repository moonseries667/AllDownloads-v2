import { fileURLToPath } from "node:url";
import { createServer } from "vite";
export default async function setup() {
  const server = await createServer({ mode: "demo", root: fileURLToPath(new URL("..", import.meta.url)), server: { host: "127.0.0.1", port: 5197, strictPort: true } });
  await server.listen(); return () => server.close();
}
