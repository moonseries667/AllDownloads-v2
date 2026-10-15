package telegram

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"mime"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"time"

	p "github.com/moonseries667/AllDownloads-v2/backend/go/internal/platforms"
)

type Attachment struct {
	ID         string  `json:"id"`
	ExternalID string  `json:"externalId"`
	Name       string  `json:"name"`
	MediaType  string  `json:"mediaType"`
	MIMEType   *string `json:"mimeType"`
	SizeBytes  *string `json:"sizeBytes"`
}

type ScanMessage struct {
	ID            string       `json:"id"`
	ExternalID    string       `json:"externalId"`
	SentAt        string       `json:"sentAt"`
	Text          string       `json:"text"`
	GroupID       *string      `json:"groupId"`
	GroupComplete bool         `json:"groupComplete"`
	Attachments   []Attachment `json:"attachments"`
	SourceLinks   []string     `json:"sourceLinks"`
	AuthorTags    []string     `json:"authorTags"`
	Channel       *string      `json:"channel"`
}

// RawMedia is populated from actual message media and document attributes, not
// from display text or a guessed filename suffix. Preview thumbnails are ignored.
type RawMedia struct {
	ExternalID, Name                                            string
	MIMEType                                                    *string
	SizeBytes                                                   *string
	Photo, Video, RoundVideo, Audio, Voice, Document, Thumbnail bool
}

type RawMessage struct {
	ID                      string
	SentAt                  time.Time
	Text                    string
	GroupID                 *string
	Media                   []RawMedia
	SourceLinks, AuthorTags []string
	Channel                 *string
}

type Page struct {
	Messages []RawMessage
	// Through covers every accessible Saved Message up to this ID in this scan.
	// No messages may be skipped based on their media type.
	Through  string
	Next     *string
	Complete bool
}

type Group struct {
	Messages []RawMessage
	Complete bool
}

type SavedClient interface {
	SavedPage(context.Context, string, string, *string) (Page, error)
	// SavedGroup resolves the stable group in this account's Saved Messages,
	// including members before afterMessageId. Complete requires provider proof;
	// adjacency, an empty next page or matching timestamps alone are not proof.
	SavedGroup(context.Context, string, string) (Group, error)
}

type ScanRequest struct {
	SchemaVersion  int     `json:"schemaVersion"`
	AccountID      string  `json:"accountId"`
	AfterMessageID string  `json:"afterMessageId"`
	Continuation   *string `json:"continuation"`
}

type ScanResult struct {
	AccountID               string        `json:"accountId"`
	Messages                []ScanMessage `json:"messages"`
	Complete                bool          `json:"complete"`
	ScannedThroughMessageID string        `json:"scannedThroughMessageId"`
	Continuation            *string       `json:"continuation"`
}

func stableID(parts ...string) string {
	data, _ := json.Marshal(parts)
	return base64.RawURLEncoding.EncodeToString(data)
}

func messageNumber(s string) (uint64, error) {
	n, err := strconv.ParseUint(s, 10, 64)
	if err != nil || strconv.FormatUint(n, 10) != s {
		return 0, p.NewFault("INVALID_INPUT", "SCAN")
	}
	return n, nil
}

func Classify(m RawMedia) string {
	photo, video, audio := m.Photo, m.Video || m.RoundVideo, m.Audio || m.Voice
	count := 0
	for _, v := range []bool{photo, video, audio} {
		if v {
			count++
		}
	}
	if count > 1 {
		return "UNKNOWN"
	}
	if photo {
		return "PHOTO"
	}
	if video {
		return "VIDEO"
	}
	if audio {
		return "AUDIO"
	}
	if m.MIMEType != nil {
		kind, _, err := mime.ParseMediaType(*m.MIMEType)
		if err == nil {
			switch strings.SplitN(strings.ToLower(kind), "/", 2)[0] {
			case "video":
				return "VIDEO"
			case "audio":
				return "AUDIO"
			}
		}
	}
	if m.Document {
		return "DOCUMENT"
	}
	return "UNKNOWN"
}

func normalize(account string, m RawMessage, complete bool) (ScanMessage, error) {
	id, err := messageNumber(m.ID)
	if err != nil || id == 0 || m.SentAt.IsZero() || (m.GroupID != nil && *m.GroupID == "") {
		return ScanMessage{}, p.NewFault("PROVIDER_CHANGED", "SCAN")
	}
	msg := ScanMessage{ID: stableID(account, m.ID), ExternalID: m.ID, SentAt: m.SentAt.UTC().Format(time.RFC3339Nano), Text: m.Text, GroupID: m.GroupID, GroupComplete: complete, Attachments: []Attachment{}, SourceLinks: append([]string{}, m.SourceLinks...), AuthorTags: append([]string{}, m.AuthorTags...), Channel: m.Channel}
	seen := map[string]bool{}
	for _, media := range m.Media {
		if media.Thumbnail {
			continue
		}
		if media.ExternalID == "" || media.Name == "" || seen[media.ExternalID] {
			return ScanMessage{}, p.NewFault("PROVIDER_CHANGED", "SCAN")
		}
		if media.SizeBytes != nil {
			if _, err := messageNumber(*media.SizeBytes); err != nil {
				return ScanMessage{}, p.NewFault("PROVIDER_CHANGED", "SCAN")
			}
		}
		seen[media.ExternalID] = true
		msg.Attachments = append(msg.Attachments, Attachment{ID: stableID(account, m.ID, media.ExternalID), ExternalID: media.ExternalID, Name: media.Name, MediaType: Classify(media), MIMEType: media.MIMEType, SizeBytes: media.SizeBytes})
	}
	return msg, nil
}

// Scan returns all classification/group facts. Only Java decides eligibility,
// queues attachments and transactionally advances its confirmed cursor.
func Scan(ctx context.Context, client SavedClient, req ScanRequest) (ScanResult, error) {
	result := ScanResult{AccountID: req.AccountID, Messages: []ScanMessage{}, ScannedThroughMessageID: req.AfterMessageID}
	after, err := messageNumber(req.AfterMessageID)
	if err != nil || req.SchemaVersion != 1 || req.AccountID == "" || (req.Continuation != nil && *req.Continuation == "") {
		return result, p.NewFault("INVALID_INPUT", "SCAN")
	}
	if err := ctx.Err(); err != nil {
		return result, p.SafeFault(err, "SCAN")
	}
	if client == nil {
		return result, p.NewFault("PROVIDER_UNAVAILABLE", "SCAN")
	}
	page, err := client.SavedPage(ctx, req.AccountID, req.AfterMessageID, req.Continuation)
	if err != nil {
		return result, p.SafeFault(err, "SCAN")
	}
	through, err := messageNumber(page.Through)
	if err != nil || through < after || (page.Complete && page.Next != nil) || (!page.Complete && (page.Next == nil || *page.Next == "")) || (req.Continuation != nil && page.Next != nil && *req.Continuation == *page.Next) {
		return result, p.NewFault("PROVIDER_CHANGED", "SCAN")
	}
	messages := map[string]RawMessage{}
	groups := map[string]bool{}
	add := func(m RawMessage) error {
		if old, ok := messages[m.ID]; ok && !reflect.DeepEqual(old, m) {
			return p.NewFault("PROVIDER_CHANGED", "SCAN")
		}
		messages[m.ID] = m
		return nil
	}
	for _, msg := range page.Messages {
		n, e := messageNumber(msg.ID)
		if e != nil || n <= after || n > through {
			return result, p.NewFault("PROVIDER_CHANGED", "SCAN")
		}
		if err := add(msg); err != nil {
			return result, err
		}
		if msg.GroupID != nil {
			groups[*msg.GroupID] = false
		}
	}
	keys := make([]string, 0, len(groups))
	for key := range groups {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		if err := ctx.Err(); err != nil {
			return result, p.SafeFault(err, "SCAN")
		}
		group, err := client.SavedGroup(ctx, req.AccountID, key)
		if err != nil {
			return result, p.SafeFault(err, "SCAN_GROUP")
		}
		groupIDs := map[string]bool{}
		for _, msg := range group.Messages {
			if msg.GroupID == nil || *msg.GroupID != key {
				return result, p.NewFault("PROVIDER_CHANGED", "SCAN_GROUP")
			}
			if err := add(msg); err != nil {
				return result, err
			}
			groupIDs[msg.ID] = true
		}
		// A provider must account for the triggering member too before declaring
		// the group complete; missing members cannot be silently erased.
		for _, msg := range page.Messages {
			if msg.GroupID != nil && *msg.GroupID == key && !groupIDs[msg.ID] && group.Complete {
				return result, p.NewFault("PROVIDER_CHANGED", "SCAN_GROUP")
			}
		}
		groups[key] = group.Complete
	}
	for _, msg := range messages {
		complete := msg.GroupID == nil || groups[*msg.GroupID]
		normalized, err := normalize(req.AccountID, msg, complete)
		if err != nil {
			return result, err
		}
		result.Messages = append(result.Messages, normalized)
	}
	sort.Slice(result.Messages, func(i, j int) bool {
		a, _ := messageNumber(result.Messages[i].ExternalID)
		b, _ := messageNumber(result.Messages[j].ExternalID)
		return a < b
	})
	result.ScannedThroughMessageID = page.Through
	result.Complete, result.Continuation = page.Complete, page.Next
	return result, nil
}
