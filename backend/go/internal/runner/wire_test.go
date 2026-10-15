package runner

import (
	"encoding/json"
	"os"
	"reflect"
	"testing"
)

// Samples are copied unchanged from d508c7a:contracts/feedback1.examples.json.
// Round trips catch dropped fields/casing and preserve null versus empty values.
func TestFrozenContractExamplesRoundTrip(t *testing.T) {
	for _, name := range []string{"operation", "result"} {
		t.Run(name, func(t *testing.T) {
			data, err := os.ReadFile("testdata/feedback1-" + name + ".json")
			if err != nil {
				t.Fatal(err)
			}
			var target any
			if name == "operation" {
				target = &Operation{}
			} else {
				target = &Result{}
			}
			if err := decode(data, target); err != nil {
				t.Fatal(err)
			}
			encoded, err := json.Marshal(target)
			if err != nil {
				t.Fatal(err)
			}
			var before, after any
			if err := json.Unmarshal(data, &before); err != nil {
				t.Fatal(err)
			}
			if err := json.Unmarshal(encoded, &after); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(before, after) {
				t.Fatalf("contract round trip changed example: %s", encoded)
			}
			if op, ok := target.(*Operation); ok {
				var payload DownloadPayload
				if err := decode(op.Payload, &payload); err != nil {
					t.Fatal(err)
				}
				if payload.ArchivePolicy != "NONE" || len(payload.Outputs) != 1 || len(payload.Units[0].Tracks) != 2 {
					t.Fatal("frozen selection lost")
				}
			}
		})
	}
}
