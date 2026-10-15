// Run from the repository root: node frontend/src/api/generate-feedback-contract.mjs
import { execFileSync } from "node:child_process";
import { writeFileSync } from "node:fs";
const revision = process.argv[2] || "d508c7a";
const spec = JSON.parse(execFileSync("git", ["show", `${revision}:contracts/openapi.yaml`], { encoding: "utf8" }));
function type(schema) {
  if (schema.$ref) return schema.$ref.split("/").at(-1);
  if (schema.enum) return schema.enum.map((value) => JSON.stringify(value)).join(" | ");
  if (schema.anyOf) return schema.anyOf.map(type).join(" | ");
  if (Array.isArray(schema.type)) return schema.type.map((value) => type({ ...schema, type: value })).join(" | ");
  if (schema.type === "array") return `Array<${type(schema.items)}>`;
  if (schema.type === "integer" || schema.type === "number") return "number";
  if (schema.type === "string" || schema.type === "boolean" || schema.type === "null") return schema.type;
  if (schema.type === "object") {
    if (!schema.properties) return `Record<string, ${typeof schema.additionalProperties === "object" ? type(schema.additionalProperties) : "unknown"}>`;
    const fields = Object.entries(schema.properties).map(([key, value]) =>
      `  ${key}${schema.required?.includes(key) ? "" : "?"}: ${type(value)};`);
    if (schema.additionalProperties === true) fields.push("  [key: string]: unknown;");
    return `{\n${fields.join("\n")}\n}`;
  }
  throw new Error("Unsupported schema: " + JSON.stringify(schema));
}
const output = `// Generated from contracts/openapi.yaml at ${revision}. Do not edit by hand.\n` +
  `// Regenerate: node frontend/src/api/generate-feedback-contract.mjs ${revision}\n\n` +
  Object.entries(spec.components.schemas).map(([name, schema]) => `export type ${name} = ${type(schema)};\n`).join("\n");
writeFileSync(new URL("./feedback.generated.ts", import.meta.url), output, "utf8");
