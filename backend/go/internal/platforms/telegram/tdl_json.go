package telegram

import (
	"bufio"
	"bytes"
	"encoding/json"
	"io"
	"strconv"
	"strings"
	"time"

	p "github.com/moonseries667/AllDownloads-v2/backend/go/internal/platforms"
)

const maxTDLMessageBytes = 8 << 20

// TDLExport is the structured result of a tdl chat export using --raw --all
// --with-content. PeerID and message IDs stay decimal strings on the boundary.
type TDLExport struct {
	PeerID   string
	Messages []RawMessage
}

// ParseTDLExport reads the root messages array one item at a time. It retains
// the normalized facts required by the platform boundary, not the raw export.
func ParseTDLExport(r io.Reader) (TDLExport, error) {
	result, err := parseTDLExport(r)
	if err != nil {
		return TDLExport{}, p.NewFault("PROVIDER_CHANGED", "SCAN")
	}
	return result, nil
}

func parseTDLExport(r io.Reader) (TDLExport, error) {
	if r == nil {
		return TDLExport{}, io.ErrUnexpectedEOF
	}
	br := bufio.NewReader(r)
	if err := expectByte(br, '{'); err != nil {
		return TDLExport{}, err
	}

	var result TDLExport
	seen := map[string]bool{}
	peerSeen, messagesSeen := false, false
	for {
		if err := skipJSONSpace(br); err != nil {
			return TDLExport{}, err
		}
		next, err := br.Peek(1)
		if err != nil || next[0] == '}' {
			if err == nil {
				_, _ = br.ReadByte()
			}
			break
		}

		keyRaw, err := readJSONValue(br, 1024)
		if err != nil || len(keyRaw) == 0 || keyRaw[0] != '"' {
			return TDLExport{}, io.ErrUnexpectedEOF
		}
		var key string
		if err := json.Unmarshal(keyRaw, &key); err != nil || seen[key] {
			return TDLExport{}, io.ErrUnexpectedEOF
		}
		seen[key] = true
		if err := expectByte(br, ':'); err != nil {
			return TDLExport{}, err
		}

		switch key {
		case "id":
			value, err := readJSONValue(br, 64)
			if err != nil {
				return TDLExport{}, err
			}
			n, err := jsonInt64(value)
			if err != nil || n == 0 {
				return TDLExport{}, io.ErrUnexpectedEOF
			}
			result.PeerID = strconv.FormatInt(n, 10)
			peerSeen = true
		case "messages":
			values, err := parseTDLMessages(br)
			if err != nil {
				return TDLExport{}, err
			}
			result.Messages = values
			messagesSeen = true
		default:
			return TDLExport{}, io.ErrUnexpectedEOF
		}

		if err := skipJSONSpace(br); err != nil {
			return TDLExport{}, err
		}
		delim, err := br.ReadByte()
		if err != nil {
			return TDLExport{}, err
		}
		if delim == '}' {
			break
		}
		if delim != ',' {
			return TDLExport{}, io.ErrUnexpectedEOF
		}
	}
	if !peerSeen || !messagesSeen {
		return TDLExport{}, io.ErrUnexpectedEOF
	}
	if err := skipJSONSpace(br); err != nil && err != io.EOF {
		return TDLExport{}, err
	}
	if _, err := br.Peek(1); err != io.EOF {
		return TDLExport{}, io.ErrUnexpectedEOF
	}
	return result, nil
}

func parseTDLMessages(br *bufio.Reader) ([]RawMessage, error) {
	if err := expectByte(br, '['); err != nil {
		return nil, err
	}
	messages := []RawMessage{}
	seen := map[string]struct{}{}
	for {
		if err := skipJSONSpace(br); err != nil {
			return nil, err
		}
		next, err := br.Peek(1)
		if err != nil {
			return nil, err
		}
		if next[0] == ']' {
			_, _ = br.ReadByte()
			return messages, nil
		}
		item, err := readJSONValue(br, maxTDLMessageBytes)
		if err != nil {
			return nil, err
		}
		message, err := parseTDLMessage(item)
		if err != nil {
			return nil, err
		}
		if _, duplicate := seen[message.ID]; duplicate {
			return nil, io.ErrUnexpectedEOF
		}
		seen[message.ID] = struct{}{}
		messages = append(messages, message)
		if err := skipJSONSpace(br); err != nil {
			return nil, err
		}
		delim, err := br.ReadByte()
		if err != nil {
			return nil, err
		}
		if delim == ']' {
			return messages, nil
		}
		if delim != ',' {
			return nil, io.ErrUnexpectedEOF
		}
	}
}

func parseTDLMessage(raw []byte) (RawMessage, error) {
	root, err := decodeJSONObject(raw)
	if err != nil {
		return RawMessage{}, err
	}
	messageID, err := requiredInt64(root, "id")
	if err != nil || messageID <= 0 {
		return RawMessage{}, io.ErrUnexpectedEOF
	}
	date, err := requiredInt64(root, "date")
	if err != nil || date <= 0 {
		return RawMessage{}, io.ErrUnexpectedEOF
	}
	textValue := ""
	textExists := false
	if value, exists := root["text"]; exists {
		textValue, err = jsonString(value)
		if err != nil {
			return RawMessage{}, err
		}
		textExists = true
	}
	fileName := ""
	if value, exists := root["file"]; exists {
		fileName, err = jsonString(value)
		if err != nil {
			return RawMessage{}, err
		}
	}
	rawMessageValue, exists := root["raw"]
	if !exists {
		return RawMessage{}, io.ErrUnexpectedEOF
	}
	rawMessage, ok := rawMessageValue.(map[string]any)
	if !ok {
		return RawMessage{}, io.ErrUnexpectedEOF
	}
	actualID, err := requiredInt64(rawMessage, "ID")
	if err != nil || actualID != messageID {
		return RawMessage{}, io.ErrUnexpectedEOF
	}
	actualDate, err := requiredInt64(rawMessage, "Date")
	if err != nil || actualDate != date {
		return RawMessage{}, io.ErrUnexpectedEOF
	}
	sentAt := time.Unix(date, 0).UTC()
	if sentAt.Year() < 1 || sentAt.Year() > 9999 {
		return RawMessage{}, io.ErrUnexpectedEOF
	}
	actualTextValue, exists := rawMessage["Message"]
	if !exists {
		return RawMessage{}, io.ErrUnexpectedEOF
	}
	actualText, err := jsonString(actualTextValue)
	if err != nil || (textExists && textValue != actualText) {
		return RawMessage{}, io.ErrUnexpectedEOF
	}
	if textValue == "" {
		textValue = actualText
	}

	var groupID *string
	if value, exists := rawMessage["GroupedID"]; exists && value != nil {
		group, err := jsonInt64Value(value)
		if err != nil {
			return RawMessage{}, err
		}
		if group != 0 {
			formatted := strconv.FormatInt(group, 10)
			groupID = &formatted
		}
	}

	attachments, err := parseTDLMedia(rawMessage["Media"], strconv.FormatInt(messageID, 10), fileName)
	if err != nil {
		return RawMessage{}, err
	}
	links, err := tdlSourceLinks(rawMessage)
	if err != nil {
		return RawMessage{}, err
	}
	return RawMessage{
		ID:          strconv.FormatInt(messageID, 10),
		SentAt:      sentAt,
		Text:        textValue,
		GroupID:     groupID,
		Media:       attachments,
		SourceLinks: links,
		AuthorTags:  []string{},
	}, nil
}

func tdlSourceLinks(message map[string]any) ([]string, error) {
	links := []string{}
	entities, exists := message["Entities"]
	if !exists || entities == nil {
		return links, nil
	}
	values, ok := entities.([]any)
	if !ok {
		return nil, io.ErrUnexpectedEOF
	}
	seen := map[string]bool{}
	for _, value := range values {
		entity, ok := value.(map[string]any)
		if !ok {
			return nil, io.ErrUnexpectedEOF
		}
		// MessageEntityTextURL exposes a concrete URL field. MessageEntityURL
		// has only offset/length and is indistinguishable from unrelated entities
		// after gotd's JSON encoding, so it is deliberately not guessed here.
		if value, exists := entity["URL"]; exists {
			link, err := jsonString(value)
			if err != nil {
				return nil, err
			}
			if link != "" && !seen[link] {
				seen[link] = true
				links = append(links, link)
			}
		}
	}
	return links, nil
}

func parseTDLMedia(value any, messageID, exportedName string) ([]RawMedia, error) {
	if value == nil {
		return []RawMedia{}, nil
	}
	media, ok := value.(map[string]any)
	if !ok {
		return nil, io.ErrUnexpectedEOF
	}
	photoValue, hasPhoto := media["Photo"]
	documentValue, hasDocument := media["Document"]
	if hasPhoto && hasDocument {
		return nil, io.ErrUnexpectedEOF
	}
	if hasPhoto {
		photo, ok := photoValue.(map[string]any)
		if !ok {
			return nil, io.ErrUnexpectedEOF
		}
		id, err := requiredInt64(photo, "ID")
		if err != nil || id <= 0 {
			return nil, io.ErrUnexpectedEOF
		}
		size, err := largestPhotoBytes(photo["Sizes"])
		if err != nil {
			return nil, err
		}
		idText := strconv.FormatInt(id, 10)
		name := strings.TrimSpace(exportedName)
		if name == "" {
			name = "photo-" + idText + ".jpg"
		}
		return []RawMedia{{
			ExternalID: "photo:" + idText + ":" + sizeOrUnknown(size),
			Name:       name,
			SizeBytes:  size,
			Photo:      true,
		}}, nil
	}
	if hasDocument {
		document, ok := documentValue.(map[string]any)
		if !ok {
			return nil, io.ErrUnexpectedEOF
		}
		return parseTDLDocument(document, exportedName)
	}
	// Unknown Telegram media remains visible as a non-qualifying attachment;
	// it must not be mistaken for a plain text message.
	name := strings.TrimSpace(exportedName)
	if name == "" {
		name = "unknown-media-" + messageID
	}
	return []RawMedia{{ExternalID: "unknown:" + messageID, Name: name}}, nil
}

func parseTDLDocument(document map[string]any, exportedName string) ([]RawMedia, error) {
	id, err := requiredInt64(document, "ID")
	if err != nil || id <= 0 {
		return nil, io.ErrUnexpectedEOF
	}
	size, err := optionalSize(document, "Size")
	if err != nil {
		return nil, err
	}
	mimeType, err := optionalStringPointer(document, "MimeType")
	if err != nil {
		return nil, err
	}
	attributes, err := optionalJSONArray(document, "Attributes")
	if err != nil {
		return nil, err
	}
	var attributeName string
	audio, video := false, false
	for _, value := range attributes {
		attribute, ok := value.(map[string]any)
		if !ok {
			return nil, io.ErrUnexpectedEOF
		}
		if value, exists := attribute["FileName"]; exists && attributeName == "" {
			name, err := jsonString(value)
			if err != nil {
				return nil, err
			}
			attributeName = name
		}
		if _, exists := attribute["Voice"]; exists {
			audio = true
		}
		if _, exists := attribute["Title"]; exists || hasAny(attribute, "Performer", "Waveform") {
			audio = true
		}
		if (hasAny(attribute, "W", "H") && hasAny(attribute, "Duration")) || hasAny(attribute, "SupportsStreaming", "RoundMessage", "Nosound") {
			video = true
		}
	}
	name := strings.TrimSpace(exportedName)
	if name == "" {
		name = strings.TrimSpace(attributeName)
	}
	idText := strconv.FormatInt(id, 10)
	if name == "" {
		name = "document-" + idText
	}
	formattedSize := "unknown"
	var sizeBytes *string
	if size != nil {
		formattedSize = *size
		sizeBytes = size
	}
	media := RawMedia{
		ExternalID: "document:" + idText + ":" + formattedSize,
		Name:       name,
		MIMEType:   mimeType,
		SizeBytes:  sizeBytes,
		Audio:      audio,
		Video:      video,
		Document:   true,
	}
	return []RawMedia{media}, nil
}

func largestPhotoBytes(value any) (*string, error) {
	if value == nil {
		return nil, nil
	}
	sizes, ok := value.([]any)
	if !ok {
		return nil, io.ErrUnexpectedEOF
	}
	var largest *string
	var largestValue int64
	hasLargest := false
	for _, item := range sizes {
		size, ok := item.(map[string]any)
		if !ok {
			return nil, io.ErrUnexpectedEOF
		}
		if typeValue, exists := size["Type"]; exists {
			typeName, err := jsonString(typeValue)
			if err != nil {
				return nil, err
			}
			switch strings.ToLower(typeName) {
			case "s", "m", "a", "b", "c":
				continue
			}
		}
		var current *string
		if direct, exists := size["Size"]; exists {
			current, _ = int64Size(direct)
			if current == nil {
				return nil, io.ErrUnexpectedEOF
			}
		} else if progressive, exists := size["Sizes"]; exists {
			values, ok := progressive.([]any)
			if !ok {
				return nil, io.ErrUnexpectedEOF
			}
			for _, candidate := range values {
				parsed, ok := int64Size(candidate)
				if !ok {
					return nil, io.ErrUnexpectedEOF
				}
				if current == nil || parsedValue(parsed) > parsedValue(current) {
					current = parsed
				}
			}
		}
		if current == nil {
			continue
		}
		parsed := parsedValue(current)
		if !hasLargest || parsed > largestValue {
			largest, largestValue, hasLargest = current, parsed, true
		}
	}
	return largest, nil
}

func sizeOrUnknown(size *string) string {
	if size == nil {
		return "unknown"
	}
	return *size
}

func optionalSize(value map[string]any, key string) (*string, error) {
	item, exists := value[key]
	if !exists || item == nil {
		return nil, nil
	}
	parsed, ok := int64Size(item)
	if !ok {
		return nil, io.ErrUnexpectedEOF
	}
	return parsed, nil
}

func int64Size(value any) (*string, bool) {
	number, err := jsonInt64Value(value)
	if err != nil || number < 0 {
		return nil, false
	}
	formatted := strconv.FormatInt(number, 10)
	return &formatted, true
}

func parsedValue(value *string) int64 {
	if value == nil {
		return 0
	}
	parsed, _ := strconv.ParseInt(*value, 10, 64)
	return parsed
}

func optionalJSONArray(value map[string]any, key string) ([]any, error) {
	item, exists := value[key]
	if !exists || item == nil {
		return []any{}, nil
	}
	array, ok := item.([]any)
	if !ok {
		return nil, io.ErrUnexpectedEOF
	}
	return array, nil
}

func optionalStringPointer(value map[string]any, key string) (*string, error) {
	item, exists := value[key]
	if !exists || item == nil {
		return nil, nil
	}
	text, err := jsonString(item)
	if err != nil {
		return nil, err
	}
	if text == "" {
		return nil, nil
	}
	return &text, nil
}

func hasAny(value map[string]any, keys ...string) bool {
	for _, key := range keys {
		if _, exists := value[key]; exists {
			return true
		}
	}
	return false
}

func decodeJSONObject(raw []byte) (map[string]any, error) {
	decoded, err := decodeJSON(raw)
	if err != nil {
		return nil, err
	}
	object, ok := decoded.(map[string]any)
	if !ok {
		return nil, io.ErrUnexpectedEOF
	}
	return object, nil
}

func decodeJSON(raw []byte) (any, error) {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return nil, err
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		return nil, io.ErrUnexpectedEOF
	}
	return value, nil
}

func requiredInt64(object map[string]any, key string) (int64, error) {
	value, exists := object[key]
	if !exists {
		return 0, io.ErrUnexpectedEOF
	}
	return jsonInt64Value(value)
}

func jsonInt64(raw []byte) (int64, error) {
	value, err := decodeJSON(raw)
	if err != nil {
		return 0, err
	}
	return jsonInt64Value(value)
}

func jsonInt64Value(value any) (int64, error) {
	number, ok := value.(json.Number)
	if !ok {
		return 0, io.ErrUnexpectedEOF
	}
	return number.Int64()
}

func jsonString(value any) (string, error) {
	text, ok := value.(string)
	if !ok {
		return "", io.ErrUnexpectedEOF
	}
	return text, nil
}

func expectByte(r *bufio.Reader, expected byte) error {
	if err := skipJSONSpace(r); err != nil {
		return err
	}
	actual, err := r.ReadByte()
	if err != nil || actual != expected {
		return io.ErrUnexpectedEOF
	}
	return nil
}

func skipJSONSpace(r *bufio.Reader) error {
	for {
		value, err := r.Peek(1)
		if err != nil {
			return err
		}
		switch value[0] {
		case ' ', '\t', '\r', '\n':
			_, _ = r.ReadByte()
		default:
			return nil
		}
	}
}

// readJSONValue consumes exactly one JSON value while enforcing a bound on
// temporary message storage before bytes are appended to the buffer.
func readJSONValue(r *bufio.Reader, maxBytes int) ([]byte, error) {
	if err := skipJSONSpace(r); err != nil {
		return nil, err
	}
	first, err := r.ReadByte()
	if err != nil {
		return nil, err
	}
	value := make([]byte, 0, min(maxBytes, 4096))
	appendByte := func(b byte) error {
		if len(value) >= maxBytes {
			return io.ErrUnexpectedEOF
		}
		value = append(value, b)
		return nil
	}
	if err := appendByte(first); err != nil {
		return nil, err
	}
	if first == '{' || first == '[' {
		stack := []byte{first}
		inString, escaped := false, false
		for len(stack) > 0 {
			b, err := r.ReadByte()
			if err != nil {
				return nil, err
			}
			if err := appendByte(b); err != nil {
				return nil, err
			}
			if inString {
				if escaped {
					escaped = false
				} else if b == '\\' {
					escaped = true
				} else if b == '"' {
					inString = false
				}
				continue
			}
			switch b {
			case '"':
				inString = true
			case '{', '[':
				stack = append(stack, b)
			case '}', ']':
				open := stack[len(stack)-1]
				if (open == '{' && b != '}') || (open == '[' && b != ']') {
					return nil, io.ErrUnexpectedEOF
				}
				stack = stack[:len(stack)-1]
			}
		}
		return value, nil
	}
	if first == '"' {
		escaped := false
		for {
			b, err := r.ReadByte()
			if err != nil {
				return nil, err
			}
			if err := appendByte(b); err != nil {
				return nil, err
			}
			if escaped {
				escaped = false
			} else if b == '\\' {
				escaped = true
			} else if b == '"' {
				return value, nil
			}
		}
	}
	for {
		peek, err := r.Peek(1)
		if err == io.EOF {
			return value, nil
		}
		if err != nil {
			return nil, err
		}
		if strings.ContainsRune(",]} \t\r\n", rune(peek[0])) {
			return value, nil
		}
		b, _ := r.ReadByte()
		if err := appendByte(b); err != nil {
			return nil, err
		}
	}
}
