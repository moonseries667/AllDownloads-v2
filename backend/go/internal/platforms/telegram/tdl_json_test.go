package telegram

import (
	"errors"
	"strings"
	"testing"
	"time"

	p "github.com/moonseries667/AllDownloads-v2/backend/go/internal/platforms"
)

func TestParseTDLExportMapsSavedMessageFactsAndMedia(t *testing.T) {
	const highID = "9007199254740993"
	input := `{
	  "id": 9007199254740993,
	  "messages": [
	    {"id":1,"file":"","date":1710000000,"text":"plain text","raw":{"ID":1,"Date":1710000000,"Message":"plain text","GroupedID":0,"Media":null,"Entities":[]}},
	    {"id":2,"file":"","date":1710000001,"text":"photo caption","raw":{"ID":2,"Date":1710000001,"Message":"photo caption","GroupedID":9007199254740995,"Media":{"Photo":{"ID":9007199254740993,"Sizes":[{"Type":"s","Size":100},{"Type":"x","Size":2048},{"Type":"y","Sizes":[1024,4096]}]}},"Entities":[{"Offset":0,"Length":5,"URL":"https://example.test/source"}]}},
	    {"id":3,"file":"","date":1710000002,"text":"video","raw":{"ID":3,"Date":1710000002,"Message":"video","Media":{"Document":{"ID":31,"MimeType":"application/octet-stream","Attributes":[{"FileName":"movie.mp4"},{"Duration":12.5,"W":640,"H":360,"SupportsStreaming":true}]}}}},
	    {"id":4,"file":"","date":1710000003,"text":"voice","raw":{"ID":4,"Date":1710000003,"Message":"voice","Media":{"Document":{"ID":41,"Size":700,"MimeType":"application/octet-stream","Attributes":[{"Voice":true,"Duration":4},{"FileName":"note.ogg"}]}}}},
	    {"id":5,"file":"report.mp4","date":1710000004,"text":"ordinary file","raw":{"ID":5,"Date":1710000004,"Message":"ordinary file","Media":{"Document":{"ID":51,"Size":0,"MimeType":"application/pdf","Attributes":[{"FileName":"document.pdf"}]}}}},
	    {"id":6,"file":"","date":1710000005,"text":"unknown media","raw":{"ID":6,"Date":1710000005,"Message":"unknown media","Media":{"Webpage":{"URL":"https://example.test/page"}}}}
	  ]
	}`

	result, err := ParseTDLExport(strings.NewReader(input))
	if err != nil {
		t.Fatal(err)
	}
	if result.PeerID != highID || len(result.Messages) != 6 {
		t.Fatalf("got peer=%q messages=%d", result.PeerID, len(result.Messages))
	}
	if result.Messages[0].ID != "1" || result.Messages[0].Text != "plain text" || !result.Messages[0].SentAt.Equal(time.Unix(1710000000, 0).UTC()) || len(result.Messages[0].Media) != 0 {
		t.Fatalf("bad text message: %+v", result.Messages[0])
	}

	photo := result.Messages[1]
	if photo.ID != "2" || photo.GroupID == nil || *photo.GroupID != "9007199254740995" || len(photo.Media) != 1 {
		t.Fatalf("bad photo identity or group: %+v", photo)
	}
	if photo.Media[0].ExternalID != "photo:"+highID+":4096" || photo.Media[0].Name != "photo-"+highID+".jpg" || Classify(photo.Media[0]) != "PHOTO" {
		t.Fatalf("bad photo facts: %+v", photo.Media[0])
	}
	if photo.SourceLinks == nil || len(photo.SourceLinks) != 1 || photo.SourceLinks[0] != "https://example.test/source" || len(photo.AuthorTags) != 0 {
		t.Fatalf("unexpected link or author facts: %+v", photo)
	}

	video := result.Messages[2].Media[0]
	if video.ExternalID != "document:31:unknown" || video.Name != "movie.mp4" || video.SizeBytes != nil || !video.Video || Classify(video) != "VIDEO" {
		t.Fatalf("bad video document facts: %+v", video)
	}
	voice := result.Messages[3].Media[0]
	if voice.ExternalID != "document:41:700" || voice.Name != "note.ogg" || voice.SizeBytes == nil || *voice.SizeBytes != "700" || !voice.Audio || Classify(voice) != "AUDIO" {
		t.Fatalf("bad audio document facts: %+v", voice)
	}
	ordinary := result.Messages[4].Media[0]
	if ordinary.Name != "report.mp4" || ordinary.SizeBytes == nil || *ordinary.SizeBytes != "0" || Classify(ordinary) != "DOCUMENT" {
		t.Fatalf("filename incorrectly made document media: %+v", ordinary)
	}
	unknown := result.Messages[5].Media
	if len(unknown) != 1 || unknown[0].ExternalID != "unknown:6" || Classify(unknown[0]) != "UNKNOWN" {
		t.Fatalf("unknown media was dropped or guessed: %+v", unknown)
	}
}

func TestParseTDLExportUsesDocumentAttributeNameAndLeavesUnknownSizesNil(t *testing.T) {
	input := `{"id":1,"messages":[
		{"id":10,"file":"","date":1710000010,"text":"photo","raw":{"ID":10,"Date":1710000010,"Message":"photo","Media":{"Photo":{"ID":100,"Sizes":[{"Type":"s","Size":12}]}}}},
		{"id":11,"file":"","date":1710000011,"text":"doc","raw":{"ID":11,"Date":1710000011,"Message":"doc","Media":{"Document":{"ID":101,"MimeType":"application/octet-stream","Attributes":[{"FileName":"archive.bin"}]}}}}
	]}`
	result, err := ParseTDLExport(strings.NewReader(input))
	if err != nil {
		t.Fatal(err)
	}
	photo, document := result.Messages[0].Media[0], result.Messages[1].Media[0]
	if photo.SizeBytes != nil || photo.ExternalID != "photo:100:unknown" {
		t.Fatalf("thumbnail treated as original size: %+v", photo)
	}
	if document.SizeBytes != nil || document.ExternalID != "document:101:unknown" || document.Name != "archive.bin" {
		t.Fatalf("bad missing-size document facts: %+v", document)
	}
}

func TestParseTDLExportRejectsIntegerOverflowWithoutFloatRounding(t *testing.T) {
	cases := map[string]string{
		"peer id overflow":      `{"id":9223372036854775808,"messages":[]}`,
		"message id overflow":   `{"id":1,"messages":[{"id":9223372036854775808,"date":1,"text":"x","raw":{"ID":1,"Date":1,"Message":"x"}}]}`,
		"raw media id overflow": `{"id":1,"messages":[{"id":1,"date":1,"text":"x","raw":{"ID":1,"Date":1,"Message":"x","Media":{"Document":{"ID":9223372036854775808,"Size":1}}}}]}`,
	}
	for name, input := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := ParseTDLExport(strings.NewReader(input))
			assertProviderChanged(t, err)
		})
	}

}

func TestParseTDLExportRejectsDamagedStructureAndDuplicateIDs(t *testing.T) {
	cases := map[string]string{
		"truncated":             `{"id":1,"messages":[`,
		"missing raw":           `{"id":1,"messages":[{"id":1,"date":1710000000,"text":"secret"}]}`,
		"raw id mismatch":       `{"id":1,"messages":[{"id":1,"date":1710000000,"text":"secret","raw":{"ID":2,"Date":1710000000,"Message":"secret"}}]}`,
		"raw date mismatch":     `{"id":1,"messages":[{"id":1,"date":1710000000,"text":"secret","raw":{"ID":1,"Date":1710000001,"Message":"secret"}}]}`,
		"out of range date":     `{"id":1,"messages":[{"id":1,"date":9223372036854775807,"text":"x","raw":{"ID":1,"Date":9223372036854775807,"Message":"x"}}]}`,
		"duplicate message ids": `{"id":1,"messages":[{"id":1,"date":1710000000,"text":"x","raw":{"ID":1,"Date":1710000000,"Message":"x"}},{"id":1,"date":1710000000,"text":"x","raw":{"ID":1,"Date":1710000000,"Message":"x"}}]}`,
		"invalid size":          `{"id":1,"messages":[{"id":1,"date":1710000000,"text":"x","raw":{"ID":1,"Date":1710000000,"Message":"x","Media":{"Document":{"ID":2,"Size":1.5}}}}]}`,
	}
	for name, input := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := ParseTDLExport(strings.NewReader(input))
			assertProviderChanged(t, err)
			if strings.Contains(err.Error(), "secret") {
				t.Fatalf("parser leaked input in error: %v", err)
			}
		})
	}
}

func TestParseTDLExportBoundsTemporaryMessageBuffer(t *testing.T) {
	input := `{"id":1,"messages":[{"id":1,"date":1710000000,"text":"` + strings.Repeat("x", maxTDLMessageBytes) + `"}]}`
	_, err := ParseTDLExport(strings.NewReader(input))
	assertProviderChanged(t, err)
}

func assertProviderChanged(t *testing.T, err error) {
	t.Helper()
	var fault *p.Fault
	if !errors.As(err, &fault) || fault.Code != "PROVIDER_CHANGED" || fault.Stage != "SCAN" {
		t.Fatalf("got %v, want PROVIDER_CHANGED/SCAN", err)
	}
}
