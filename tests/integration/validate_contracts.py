"""Validate frozen examples and captured live responses without contacting any platform."""
import json
import sys
from pathlib import Path
from jsonschema import Draft202012Validator

ROOT = Path(__file__).resolve().parents[2]
OPENAPI = json.loads((ROOT / "contracts/openapi.yaml").read_text(encoding="utf-8"))
RUNNER = json.loads((ROOT / "contracts/runner.schema.json").read_text(encoding="utf-8"))


def public_schema(name):
    return {"$ref": f"#/components/schemas/{name}", "components": OPENAPI["components"]}


def validate(name, value):
    Draft202012Validator(public_schema(name)).validate(value)


def main():
    for schema in OPENAPI["components"]["schemas"].values():
        Draft202012Validator.check_schema(schema)
    Draft202012Validator.check_schema(RUNNER)
    examples = json.loads((ROOT / "contracts/feedback1.examples.json").read_text(encoding="utf-8"))
    for name, key in [("Source", "source"), ("Content", "content"), ("PreviewRequest", "previewRequest")]:
        validate(name, examples[key])
    for key in ["operation", "result"]:
        Draft202012Validator(RUNNER).validate(examples[key])
    Draft202012Validator({"$ref": "#/$defs/DownloadPayload", "$defs": RUNNER["$defs"]}).validate(examples["operation"]["payload"])
    if len(sys.argv) > 1:
        captures = json.loads(Path(sys.argv[1]).read_text(encoding="utf-8"))
        for capture in captures:
            validate(capture["schema"], capture["data"])
    if len(sys.argv) > 2:
        operation = json.loads(Path(sys.argv[2]).read_text(encoding="utf-8"))
        Draft202012Validator(RUNNER).validate(operation)
        Draft202012Validator({"$ref": "#/$defs/DownloadPayload", "$defs": RUNNER["$defs"]}).validate(operation["payload"])
    print("Contract schemas, examples and supplied captures validated")


if __name__ == "__main__":
    main()
