"""Validate shared lifecycle examples and optional captured real HTTP exchanges."""
import json
import re
import sys
from urllib.parse import urljoin
from pathlib import Path
from jsonschema import Draft202012Validator
from referencing import Registry, Resource

ROOT = Path(__file__).resolve().parents[2]
CONTRACTS = ROOT / "contracts"

def load(name):
    return json.loads((CONTRACTS / name).read_text(encoding="utf-8"))

def main():
    runner=load("runner.schema.json")
    public=load("openapi.yaml")
    fixture=load("fixture.schema.json")
    api=load("runner-lifecycle.openapi.json")
    examples=load("runner-lifecycle.examples.json")
    Draft202012Validator.check_schema(runner)
    Draft202012Validator(fixture).validate(load("fixture.examples.json"))
    registry=Registry()
    for name in ("runner.schema.json","feedback2.schema.json","management.schema.json","fixture.schema.json","accounts.schema.json"):
        document=load(name);resource=Resource.from_contents(document)
        for uri in {(CONTRACTS/name).as_uri(),document.get("$id"),urljoin(runner["$id"],name)}:
            if uri: registry=registry.with_resource(uri,resource)
    accounts=load("accounts.schema.json")
    Draft202012Validator.check_schema(accounts)
    for key,definition in (("account","Account"),("session","AuthSession"),("checkPayload","CheckPayload"),("authPayload","AuthPayload")):
        value=load("accounts.examples.json")[key]
        validator=Draft202012Validator({"$id":accounts["$id"],"$defs":accounts["$defs"],"$ref":"#/$defs/"+definition},registry=registry)
        validator.validate(value)
        rejected=dict(value,cookie="must-never-appear")
        assert not validator.is_valid(rejected), "Private credentials must not enter account contracts"
    def validate(ref,value):
        if ref.startswith("runner.schema.json#"):
            schema={"$id":runner["$id"],"$ref":ref.split("runner.schema.json",1)[1],"$defs":runner["$defs"]}
            Draft202012Validator(schema,registry=registry).validate(value)
        else:
            raise AssertionError("Unknown local schema ref")
    for key,ref in examples["schemaByExample"].items():
        validate(ref,examples[key]["value"])
    # Validate response *data* too: a generic envelope alone cannot prove a
    # nullable acquire result or a persisted result acknowledgement is correct.
    response_types={"registerResponse":"RegisterResponse","acquireEmptyResponse":"AcquireResponse","acquireOperationResponse":"AcquireResponse","transferReconcileOperationResponse":"AcquireResponse","heartbeatResponse":"HeartbeatResponse","factsResponse":"FactsAck","resultSucceededResponse":"ResultAck","resultUnknownResponse":"ResultAck","sameEventReplayResponse":"ResultAck","transferReconcileResultResponse":"ResultAck"}
    for key,name in response_types.items():
        validate("runner.schema.json#/$defs/"+name,examples[key]["value"]["data"])
    for path,item in api["paths"].items():
        for operation in item.values():
            for response in operation.get("responses",{}).values():
                schema=response.get("content",{}).get("application/json",{}).get("schema")
                if schema: Draft202012Validator.check_schema(schema)
    for supplied in sys.argv[1:]:
        for exchange in json.loads(Path(supplied).read_text(encoding="utf-8")):
            if "schema" in exchange:
                validate("runner.schema.json#/$defs/"+exchange["schema"],exchange["data"])
            else:
                shape=lambda path:re.sub(r"\{[^}]+\}","{}",path)
                route=next(path for path in api["paths"] if shape(path)==shape(exchange["path"]))
                operation=api["paths"][route]["post"]
                if exchange.get("request") is not None:
                    schema=operation["requestBody"]["content"]["application/json"]["schema"]
                    Draft202012Validator({"$id":(CONTRACTS/"runner-lifecycle.openapi.json").as_uri(),**schema},registry=registry).validate(exchange["request"])
                status=str(exchange["status"])
                response=operation["responses"].get(status,operation["responses"].get("default"))
                schema=response["content"]["application/json"]["schema"]
                Draft202012Validator({"$id":(CONTRACTS/"runner-lifecycle.openapi.json").as_uri(),**schema},registry=registry).validate(exchange["response"])
    print("Runner lifecycle schemas, 19 examples, fixture input and supplied HTTP captures validated")

if __name__ == "__main__": main()
