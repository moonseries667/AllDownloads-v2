// Run from the repository root. Reads only an explicit committed contract.
import { execFileSync } from "node:child_process";
import { writeFileSync } from "node:fs";
const revision = process.argv[2] || "baa03f5";
const schema = JSON.parse(execFileSync("git", ["-c", `safe.directory=${process.cwd().replaceAll("\\", "/")}`, "show", `${revision}:contracts/management.schema.json`], { encoding: "utf8" }));
function type(value) {
  if (value.$ref) return value.$ref.split("/").at(-1);
  if ("const" in value) return JSON.stringify(value.const);
  if (value.enum) return value.enum.map(JSON.stringify).join(" | ");
  if (value.anyOf) return value.anyOf.map(type).join(" | ");
  if (Array.isArray(value.type)) return value.type.map(t => type({ ...value, type: t })).join(" | ");
  if (value.type === "array") return `Array<${type(value.items)}>`;
  if (["integer", "number"].includes(value.type)) return "number";
  if (["string", "boolean", "null"].includes(value.type)) return value.type;
  if (value.type === "object") {
    if (!value.properties) return "Record<string, unknown>";
    return `{\n${Object.entries(value.properties).map(([key, field]) => `  ${key}${value.required?.includes(key) ? "" : "?"}: ${type(field)};`).join("\n")}\n}`;
  }
  throw Error("Unsupported schema " + JSON.stringify(value));
}
writeFileSync(new URL("./management.generated.ts", import.meta.url), `// Generated from contracts/management.schema.json at ${revision}. Do not edit by hand.\n// Regenerate: node frontend/src/api/generate-management-contract.mjs ${revision}\n\n` + Object.entries(schema.$defs).map(([name, value]) => `export type ${name} = ${type(value)};\n`).join("\n"));
