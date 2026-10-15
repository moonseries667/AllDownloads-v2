package platforms_test

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"testing"
	"time"

	p "github.com/moonseries667/AllDownloads-v2/backend/go/internal/platforms"
	"github.com/moonseries667/AllDownloads-v2/backend/go/internal/platforms/telegram"
)

func ptr[T any](v T) *T { return &v }
func raw(id string, group *string, media ...telegram.RawMedia) telegram.RawMessage {
	return telegram.RawMessage{ID: id, SentAt: time.Date(2026, 10, 15, 1, 2, 3, 0, time.UTC), Text: "测试正文", GroupID: group, Media: media}
}

type saved struct {
	pages      []telegram.Page
	groups     map[string]telegram.Group
	calls      int
	groupCalls int
	after      []string
	tokens     []*string
}

func (s *saved) SavedPage(ctx context.Context, account, after string, token *string) (telegram.Page, error) {
	s.after = append(s.after, after)
	s.tokens = append(s.tokens, token)
	p := s.pages[s.calls]
	s.calls++
	return p, nil
}
func (s *saved) SavedGroup(ctx context.Context, account, group string) (telegram.Group, error) {
	s.groupCalls++
	return s.groups[group], nil
}

func TestClassificationUsesFactsNotFilenames(t *testing.T) {
	for _, tc := range []struct {
		name string
		m    telegram.RawMedia
		kind string
	}{
		{"fake extension", telegram.RawMedia{Name: "video.mp4", Document: true, MIMEType: ptr("application/pdf")}, "DOCUMENT"},
		{"MIME video", telegram.RawMedia{MIMEType: ptr("VIDEO/mp4; codecs=avc")}, "VIDEO"},
		{"MIME audio", telegram.RawMedia{MIMEType: ptr("audio/mpeg")}, "AUDIO"},
		{"voice", telegram.RawMedia{Voice: true}, "AUDIO"},
		{"round", telegram.RawMedia{RoundVideo: true}, "VIDEO"},
		{"photo", telegram.RawMedia{Photo: true}, "PHOTO"},
		{"ambiguous", telegram.RawMedia{Photo: true, Video: true}, "UNKNOWN"},
		{"unknown", telegram.RawMedia{Name: "song.mp3"}, "UNKNOWN"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := telegram.Classify(tc.m); got != tc.kind {
				t.Fatalf("got %s want %s", got, tc.kind)
			}
		})
	}
}

func TestScanIncludesExcludedTypesAndDropsOnlyPreviewThumbnails(t *testing.T) {
	photo := telegram.RawMedia{ExternalID: "p", Name: "p.jpg", Photo: true}
	doc := telegram.RawMedia{ExternalID: "d", Name: "a.pdf", Document: true, MIMEType: ptr("application/pdf")}
	video := telegram.RawMedia{ExternalID: "v", Name: "v", Video: true}
	client := &saved{pages: []telegram.Page{{Messages: []telegram.RawMessage{raw("1", nil), raw("2", nil, photo), raw("3", nil, doc), raw("4", nil, photo, doc), raw("5", nil, video), raw("6", nil, photo, video, telegram.RawMedia{Thumbnail: true})}, Through: "6", Complete: true}}}
	if client.calls != 0 {
		t.Fatal("construction scanned")
	}
	res, err := telegram.Scan(context.Background(), client, telegram.ScanRequest{SchemaVersion: 1, AccountID: "test", AfterMessageID: "0"})
	if err != nil {
		t.Fatal(err)
	}
	if !res.Complete || res.ScannedThroughMessageID != "6" || len(res.Messages) != 6 {
		t.Fatalf("facts lost: %+v", res)
	}
	if len(res.Messages[3].Attachments) != 2 || len(res.Messages[5].Attachments) != 2 {
		t.Fatal("attachments lost or thumbnail included")
	}
	if !reflect.DeepEqual(client.after, []string{"0"}) {
		t.Fatal(client.after)
	}
	for _, m := range res.Messages {
		if !m.GroupComplete {
			t.Fatal("single incomplete")
		}
	}
}

func TestCompleteAlbumIncludesMembersAcrossPaginationAndIncrementalBoundary(t *testing.T) {
	group := ptr("album-real-id")
	photo := raw("99", group, telegram.RawMedia{ExternalID: "photo", Name: "p.jpg", Photo: true})
	video := raw("101", group, telegram.RawMedia{ExternalID: "video", Name: "v.mp4", Video: true})
	for _, after := range []string{"0", "100"} {
		client := &saved{pages: []telegram.Page{{Messages: []telegram.RawMessage{video}, Through: "101", Complete: true}}, groups: map[string]telegram.Group{*group: {Messages: []telegram.RawMessage{video, photo}, Complete: true}}}
		res, err := telegram.Scan(context.Background(), client, telegram.ScanRequest{SchemaVersion: 1, AccountID: "test", AfterMessageID: after})
		if err != nil {
			t.Fatal(err)
		}
		if len(res.Messages) != 2 || res.Messages[0].ExternalID != "99" || !res.Messages[0].GroupComplete || client.groupCalls != 1 {
			t.Fatalf("lost previous photo: %+v", res)
		}
	}
	// A first partial page is allowed; the next complete fact repeats every
	// member with the same stable identity, so Java can replace pending facts.
	client := &saved{pages: []telegram.Page{{Messages: []telegram.RawMessage{photo}, Through: "99", Next: ptr("p2")}, {Messages: []telegram.RawMessage{video}, Through: "101", Complete: true}}, groups: map[string]telegram.Group{*group: {Messages: []telegram.RawMessage{photo}, Complete: false}}}
	first, err := telegram.Scan(context.Background(), client, telegram.ScanRequest{SchemaVersion: 1, AccountID: "test", AfterMessageID: "0"})
	if err != nil {
		t.Fatal(err)
	}
	if first.Complete || first.Messages[0].GroupComplete || first.ScannedThroughMessageID != "99" {
		t.Fatal(first)
	}
	client.groups[*group] = telegram.Group{Messages: []telegram.RawMessage{photo, video}, Complete: true}
	second, err := telegram.Scan(context.Background(), client, telegram.ScanRequest{SchemaVersion: 1, AccountID: "test", AfterMessageID: "0", Continuation: first.Continuation})
	if err != nil {
		t.Fatal(err)
	}
	if second.Messages[0].ID != first.Messages[0].ID || second.Messages[0].Attachments[0].ID != first.Messages[0].Attachments[0].ID {
		t.Fatal("identity changed across pages")
	}
}

func TestIncompleteAtEndRemainsPendingWithoutInventedContinuation(t *testing.T) {
	m := raw("10", ptr("g"), telegram.RawMedia{ExternalID: "p", Name: "photo", Photo: true})
	client := &saved{pages: []telegram.Page{{Messages: []telegram.RawMessage{m}, Through: "10", Complete: true}}, groups: map[string]telegram.Group{"g": {Messages: []telegram.RawMessage{m}}}}
	res, err := telegram.Scan(context.Background(), client, telegram.ScanRequest{SchemaVersion: 1, AccountID: "test", AfterMessageID: "0"})
	if err != nil {
		t.Fatal(err)
	}
	if !res.Complete || res.Continuation != nil || res.Messages[0].GroupComplete {
		t.Fatalf("must preserve traversal vs group completeness: %+v", res)
	}
}

func TestScanNeverMergesUnrelatedAdjacentMessagesAndRejectsBrokenGroup(t *testing.T) {
	a, b := raw("1", nil), raw("2", nil)
	client := &saved{pages: []telegram.Page{{Messages: []telegram.RawMessage{a, b}, Through: "2", Complete: true}}}
	res, err := telegram.Scan(context.Background(), client, telegram.ScanRequest{SchemaVersion: 1, AccountID: "t", AfterMessageID: "0"})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Messages) != 2 || client.groupCalls != 0 {
		t.Fatal("inferred group")
	}
	a.GroupID = ptr("group")
	client = &saved{pages: []telegram.Page{{Messages: []telegram.RawMessage{a}, Through: "1", Complete: true}}, groups: map[string]telegram.Group{"group": {Complete: true}}}
	_, err = telegram.Scan(context.Background(), client, telegram.ScanRequest{SchemaVersion: 1, AccountID: "t", AfterMessageID: "0"})
	if err == nil {
		t.Fatal("complete group missing trigger accepted")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = telegram.Scan(ctx, nil, telegram.ScanRequest{SchemaVersion: 1, AccountID: "t", AfterMessageID: "0"})
	var fault *p.Fault
	if !errors.As(err, &fault) || fault.Status != "CANCELLED" {
		t.Fatal(err)
	}
}

func TestScanWireNullsAndDecimalIDs(t *testing.T) {
	client := &saved{pages: []telegram.Page{{Messages: []telegram.RawMessage{raw("9007199254740993", nil, telegram.RawMedia{ExternalID: "a", Name: "a", Audio: true})}, Through: "9007199254740993", Complete: true}}}
	result, err := telegram.Scan(context.Background(), client, telegram.ScanRequest{SchemaVersion: 1, AccountID: "t", AfterMessageID: "0"})
	if err != nil {
		t.Fatal(err)
	}
	data, _ := json.Marshal(result)
	var wire map[string]any
	if err := json.Unmarshal(data, &wire); err != nil {
		t.Fatal(err)
	}
	msg := wire["messages"].([]any)[0].(map[string]any)
	if msg["externalId"] != "9007199254740993" || msg["groupId"] != nil || wire["continuation"] != nil {
		t.Fatal(string(data))
	}
}
