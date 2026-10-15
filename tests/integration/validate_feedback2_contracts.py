"""Validate part-two schemas, shared fixtures and optional captured HTTP/runner data offline."""
import json
import sys
from pathlib import Path
from jsonschema import Draft202012Validator
from referencing import Registry, Resource

ROOT = Path(__file__).resolve().parents[2]
BASE = "https://alldownloads.invalid/contracts/"
documents = {name: json.loads((ROOT / "contracts" / name).read_text(encoding="utf-8"))
             for name in ("runner.schema.json", "feedback2.schema.json", "openapi.yaml")}
registry = Registry()
for name, document in documents.items():
    registry = registry.with_resource(BASE + name, Resource.from_contents(document, default_specification=__import__("referencing.jsonschema", fromlist=["DRAFT202012"]).DRAFT202012))


def validate(schema, data):
    prefix = "openapi.yaml#/components/schemas/"
    if schema.startswith("part2:"):
        prefix, schema = "feedback2.schema.json#/$defs/", schema[6:]
    elif schema == "Operation":
        prefix = "runner.schema.json#/$defs/"
    Draft202012Validator({"$ref": BASE + prefix + schema}, registry=registry).validate(data)


def main():
    for name in ("runner.schema.json", "feedback2.schema.json"):
        Draft202012Validator.check_schema(documents[name])
    for schema in documents["openapi.yaml"]["components"]["schemas"].values():
        Draft202012Validator.check_schema(schema)
    examples = json.loads((ROOT / "contracts/feedback2.examples.json").read_text(encoding="utf-8"))
    for page in examples["telegramPages"]:
        validate("part2:ScanResult", page)
    validate("part2:TreeResult", examples["quarkRoot"])
    validate("part2:ShareRequest", examples["quarkSharedTarget"])
    validate("Issue", examples["issue"])
    count = 0
    for path in sys.argv[1:]:
        for capture in json.loads(Path(path).read_text(encoding="utf-8")):
            validate(capture["schema"], capture["data"])
            count += 1
    print(f"Part-two schemas and shared examples validated; {count} captures validated")


if __name__ == "__main__":
    main()
