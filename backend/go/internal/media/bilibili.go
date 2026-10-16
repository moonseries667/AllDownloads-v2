package media

import (
	"context"
	"crypto/md5"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"path"
	"sort"
	"strconv"
	"strings"
	"time"
)

// BilibiliSession binds one metadata operation to a verified, immutable session.
// Implementations never expose credentials through the identity string.
type BilibiliSession interface {
	Bind(context.Context) (*http.Client, string, error)
}

// BilibiliIdentityHeader is private snapshot data, removed before network I/O.
const BilibiliIdentityHeader = "X-Alldownloads-Bilibili-Identity"

// Bilibili reads platform metadata using the one explicitly configured account.
type Bilibili struct {
	Client               *http.Client
	APIBase, CommentBase string
	Session              BilibiliSession
	identity             string
}
type object map[string]any

func obj(v any) object {
	if m, ok := v.(map[string]any); ok {
		return m
	}
	return object{}
}
func list(v any) []any { a, _ := v.([]any); return a }
func str(v any) string {
	switch x := v.(type) {
	case string:
		return x
	case json.Number:
		return string(x)
	}
	return ""
}
func number(v any) *float64 {
	n, ok := v.(json.Number)
	if !ok {
		return nil
	}
	f, e := n.Float64()
	if e != nil || f < 0 {
		return nil
	}
	return &f
}
func integer(v any) int64 { n, _ := strconv.ParseInt(str(v), 10, 64); return n }
func bval(v any) bool     { b, _ := v.(bool); return b }

func (b *Bilibili) api(ctx context.Context, route string, q url.Values) (object, error) {
	base := b.APIBase
	if base == "" {
		base = "https://api.bilibili.com"
	}
	req, err := http.NewRequestWithContext(ctx, "GET", strings.TrimRight(base, "/")+route+"?"+q.Encode(), nil)
	if err != nil {
		return nil, Fail("INVALID_PLATFORM_REQUEST", "METADATA", false)
	}
	req.Header.Set("Referer", "https://www.bilibili.com/")
	req.Header.Set("User-Agent", "Mozilla/5.0")
	client := b.Client
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Second}
	}
	res, err := client.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, Fail("NETWORK_TEMPORARY", "METADATA", true)
	}
	defer res.Body.Close()
	if res.StatusCode != 200 {
		return nil, HTTPFailure(res.StatusCode, "METADATA")
	}
	dec := json.NewDecoder(io.LimitReader(res.Body, 16<<20))
	dec.UseNumber()
	var data object
	if dec.Decode(&data) != nil {
		return nil, Fail("PLATFORM_RESPONSE_UNKNOWN", "METADATA", false)
	}
	if _, exists := data["code"]; !exists {
		return nil, Fail("PLATFORM_RESPONSE_UNKNOWN", "METADATA", false)
	}
	switch integer(data["code"]) {
	case 0:
	case -404:
		if route == "/x/web-interface/view" {
			return nil, Fail("RESOURCE_DELETED", "METADATA", false)
		}
		return nil, Fail("RESOURCE_UNAVAILABLE", "METADATA", false)
	case -101:
		return nil, Fail("AUTH_REQUIRED", "METADATA", false)
	case -403:
		return nil, Fail("PERMISSION_DENIED", "METADATA", false)
	case -412, -352, -509:
		return nil, Fail("RATE_LIMITED", "METADATA", true)
	case 10015002:
		return nil, Fail("REGION_RESTRICTED", "METADATA", false)
	default:
		return nil, Fail("PLATFORM_RESPONSE_UNKNOWN", "METADATA", false)
	}
	value := obj(data["data"])
	if len(value) == 0 {
		value = obj(data["result"])
	}
	if len(value) == 0 {
		return nil, Fail("PLATFORM_RESPONSE_UNKNOWN", "METADATA", false)
	}
	if route == "/x/web-interface/nav" && (!bval(value["isLogin"]) || str(value["mid"]) != b.identity) {
		return nil, Fail("AUTH_REQUIRED", "METADATA", false)
	}
	return value, nil
}
func (b *Bilibili) signed(ctx context.Context, q url.Values) (url.Values, error) {
	data, err := b.api(ctx, "/x/web-interface/nav", url.Values{})
	if err != nil {
		return nil, err
	}
	images := obj(data["wbi_img"])
	key := ""
	for _, field := range []string{"img_url", "sub_url"} {
		value := path.Base(str(images[field]))
		key += strings.SplitN(value, ".", 2)[0]
	}
	indexes := []int{46, 47, 18, 2, 53, 8, 23, 32, 15, 50, 10, 31, 58, 3, 45, 35, 27, 43, 5, 49, 33, 9, 42, 19, 29, 28, 14, 39, 12, 38, 41, 13}
	if len(key) < 64 {
		return nil, Fail("SIGNING_METADATA_UNAVAILABLE", "METADATA", true)
	}
	mixed := make([]byte, 32)
	for i, j := range indexes {
		mixed[i] = key[j]
	}
	q.Set("wts", strconv.FormatInt(time.Now().Unix(), 10))
	for k, values := range q {
		for i, v := range values {
			q[k][i] = strings.Map(func(r rune) rune {
				if strings.ContainsRune("!'()*", r) {
					return -1
				}
				return r
			}, v)
		}
	}
	sum := md5.Sum([]byte(q.Encode() + string(mixed)))
	q.Set("w_rid", hex.EncodeToString(sum[:]))
	return q, nil
}

func (b *Bilibili) Fetch(ctx context.Context, ref Reference) (Metadata, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()
	bound, err := b.bind(ctx)
	if err != nil {
		return Metadata{}, err
	}
	b = bound
	switch ref.Kind {
	case "BVID":
		return b.video(ctx, ref.ID)
	case "SEASON_ID", "EPISODE_ID":
		return b.season(ctx, ref)
	case "MEDIA_ID":
		data, err := b.api(ctx, "/pgc/review/user", url.Values{"media_id": {strings.TrimPrefix(ref.ID, "md")}})
		if err != nil {
			return Metadata{}, err
		}
		id := str(obj(data["media"])["season_id"])
		if id == "" {
			return Metadata{}, Fail("SEASON_ID_UNKNOWN", "METADATA", false)
		}
		return b.season(ctx, Reference{Kind: "SEASON_ID", ID: id})
	default:
		return Metadata{}, Fail("UNSUPPORTED_CONTENT_ID", "METADATA", false)
	}
}
func (b *Bilibili) video(ctx context.Context, bvid string) (Metadata, error) {
	data, err := b.api(ctx, "/x/web-interface/view", url.Values{"bvid": {bvid}})
	if err != nil {
		return Metadata{}, err
	}
	if str(data["bvid"]) != bvid {
		return Metadata{}, Fail("RESOURCE_VERSION_CHANGED", "METADATA", false)
	}
	meta := Metadata{ID: "bil:" + bvid, ExternalID: bvid, IdentifierType: "BVID", Kind: "SINGLE", Title: str(data["title"]), Description: str(data["desc"]), CoverURL: str(data["pic"]), Author: str(obj(data["owner"])["name"]), OriginalURL: "https://www.bilibili.com/video/" + bvid}
	if ts := integer(data["pubdate"]); ts > 0 {
		t := time.Unix(ts, 0).UTC()
		meta.PublishedAt = &t
	}
	pages := list(data["pages"])
	if len(pages) > 1 {
		meta.Kind = "MULTIPART"
	}
	if len(pages) == 0 {
		return Metadata{}, Fail("MEDIA_STRUCTURE_UNKNOWN", "METADATA", false)
	}
	for i, p := range pages {
		page := obj(p)
		cid := str(page["cid"])
		if cid == "" {
			return Metadata{}, Fail("MEDIA_STRUCTURE_UNKNOWN", "METADATA", false)
		}
		kind := "MAIN"
		if len(pages) > 1 {
			kind = "PART"
		}
		unit := Unit{ID: "cid:" + cid, ExternalID: cid, Kind: kind, Title: str(page["part"]), Index: i + 1, Duration: number(page["duration"])}
		if err := b.populate(ctx, &unit, bvid, ""); err != nil {
			return Metadata{}, err
		}
		meta.Units = append(meta.Units, unit)
	}
	return meta, nil
}
func (b *Bilibili) season(ctx context.Context, ref Reference) (Metadata, error) {
	q := url.Values{}
	if ref.Kind == "EPISODE_ID" {
		q.Set("ep_id", strings.TrimPrefix(ref.ID, "ep"))
	} else {
		q.Set("season_id", strings.TrimPrefix(ref.ID, "ss"))
	}
	data, err := b.api(ctx, "/pgc/view/web/season", q)
	if err != nil {
		return Metadata{}, err
	}
	seasonID := str(data["season_id"])
	if seasonID == "" {
		return Metadata{}, Fail("SEASON_ID_UNKNOWN", "METADATA", false)
	}
	meta := Metadata{ID: "bil:ss" + seasonID, ExternalID: seasonID, IdentifierType: "SEASON_ID", Kind: "SERIES", Title: str(data["title"]), Description: str(data["evaluate"]), CoverURL: str(data["cover"]), OriginalURL: "https://www.bilibili.com/bangumi/play/ss" + seasonID}
	if integer(data["type"]) == 2 {
		meta.Kind = "MOVIE"
	}
	publish := str(obj(data["publish"])["pub_time"])
	if publish != "" {
		if zone, e := time.LoadLocation("Asia/Shanghai"); e == nil {
			if t, e := time.ParseInLocation("2006-01-02 15:04:05", publish, zone); e == nil {
				utc := t.UTC()
				meta.PublishedAt = &utc
			}
		}
	}
	episodes := list(data["episodes"])
	if len(episodes) == 0 {
		return Metadata{}, Fail("MEDIA_STRUCTURE_UNKNOWN", "METADATA", false)
	}
	for i, value := range episodes {
		ep := obj(value)
		id, cid := str(ep["id"]), str(ep["cid"])
		if id == "" || cid == "" {
			return Metadata{}, Fail("MEDIA_STRUCTURE_UNKNOWN", "METADATA", false)
		}
		title := str(ep["long_title"])
		if title == "" {
			title = str(ep["title"])
		}
		kind := "EPISODE"
		if meta.Kind == "MOVIE" {
			kind = "MAIN"
		}
		duration := number(ep["duration"])
		if duration != nil {
			seconds := *duration / 1000
			duration = &seconds
		}
		unit := Unit{ID: "ep:" + id, ExternalID: id, Kind: kind, Title: title, Index: i + 1, Duration: duration}
		// The CID addresses playback and subtitles; episode identity remains independent.
		playback := unit
		playback.ExternalID = cid
		if err := b.populate(ctx, &playback, str(ep["bvid"]), id); err != nil {
			return Metadata{}, err
		}
		unit.Tracks, unit.Warnings = playback.Tracks, playback.Warnings
		if meta.Kind == "MOVIE" {
			meta.Units = append(meta.Units, movieLanguages(unit)...)
		} else {
			meta.Units = append(meta.Units, unit)
		}
	}
	return meta, nil
}
func (b *Bilibili) populate(ctx context.Context, unit *Unit, bvid, episode string) error {
	q := url.Values{"bvid": {bvid}, "cid": {unit.ExternalID}, "qn": {"127"}, "fnval": {"4048"}, "fourk": {"1"}}
	route := "/x/player/wbi/playurl"
	var play object
	var err error
	if episode != "" {
		route = "/pgc/player/web/v2/playurl"
		q.Set("ep_id", episode)
	} else {
		q, err = b.signed(ctx, q)
	}
	if err == nil {
		play, err = b.api(ctx, route, q)
	}
	if err != nil {
		if fatalBilibiliError(ctx, err) {
			return err
		}
		unit.Warnings = append(unit.Warnings, Classify(err, "METADATA").Code)
	} else {
		if info := obj(play["video_info"]); len(info) > 0 {
			play = info
		}
		if bval(play["is_preview"]) || integer(play["is_preview"]) == 1 {
			unit.Warnings = append(unit.Warnings, "PAYMENT_REQUIRED")
		} else {
			if duration := number(play["timelength"]); duration != nil {
				seconds := *duration / 1000
				unit.Duration = &seconds
			}
			dash := obj(play["dash"])
			for _, kind := range []string{"VIDEO", "AUDIO"} {
				candidates := append([]any{}, list(dash[strings.ToLower(kind)])...)
				if kind == "AUDIO" {
					candidates = append(candidates, list(obj(dash["dolby"])["audio"])...)
					if flac := obj(obj(dash["flac"])["audio"]); len(flac) > 0 {
						candidates = append(candidates, map[string]any(flac))
					}
				}
				sort.SliceStable(candidates, func(i, j int) bool {
					a, c := obj(candidates[i]), obj(candidates[j])
					if kind == "VIDEO" && integer(a["id"]) != integer(c["id"]) {
						return integer(a["id"]) > integer(c["id"])
					}
					return integer(a["bandwidth"]) > integer(c["bandwidth"])
				})
				if len(candidates) == 0 {
					unit.Warnings = append(unit.Warnings, kind+"_UNAVAILABLE")
					continue
				}
				seenLanguage := map[string]bool{}
				for _, candidate := range candidates {
					item := obj(candidate)
					language := firstString(item, "language", "lang", "lan")
					if kind == "VIDEO" {
						language = ""
					}
					if seenLanguage[language] {
						continue
					}
					locator := str(item["baseUrl"])
					if locator == "" {
						locator = str(item["base_url"])
					}
					if locator == "" {
						unit.Warnings = append(unit.Warnings, kind+"_UNAVAILABLE")
						continue
					}
					id := unit.ID + ":" + strings.ToLower(kind) + ":" + str(item["id"]) + ":" + str(item["codecid"])
					if language != "" {
						id += ":" + language
					}
					var size *int64
					if value, ok := item["size"]; ok {
						if n, err := strconv.ParseInt(str(value), 10, 64); err == nil && n >= 0 {
							size = &n
						}
					}
					unit.Tracks = append(unit.Tracks, Track{ID: id, Kind: kind, Language: language, LanguageName: firstString(item, "language_doc", "lang_doc", "lan_doc", "languageDoc", "langDoc", "lanDoc"), SubtitleType: "NOT_APPLICABLE", Availability: "AVAILABLE", Duration: unit.Duration, Size: size, Source: Source{URL: normalizeURL(locator), Version: b.sourceVersion(id), Headers: b.sourceHeaders(), Size: size}})
					seenLanguage[language] = true
				}
			}
			if len(obj(play["dubbing_info"])) > 0 {
				unit.Warnings = append(unit.Warnings, "ALTERNATE_AUDIO_METADATA_UNSUPPORTED")
			}
		}
	}
	player, err := b.api(ctx, "/x/player/wbi/v2", url.Values{"bvid": {bvid}, "cid": {unit.ExternalID}})
	if err != nil {
		if fatalBilibiliError(ctx, err) {
			return err
		}
		unit.Warnings = append(unit.Warnings, Classify(err, "METADATA").Code+"_SUBTITLE")
	} else {
		if bval(player["need_login_subtitle"]) {
			return Fail("AUTH_REQUIRED", "METADATA", false)
		}
		for _, value := range list(obj(player["subtitle"])["subtitles"]) {
			sub := obj(value)
			origin := subtitleOrigin(sub)
			id := str(sub["id_str"])
			if id == "" {
				id = str(sub["id"])
			}
			if origin != "HUMAN" {
				unit.Warnings = append(unit.Warnings, "SUBTITLE_"+origin+"_EXCLUDED")
				continue
			}
			locator := str(sub["subtitle_url"])
			if id == "" || locator == "" {
				unit.Warnings = append(unit.Warnings, "SUBTITLE_METADATA_UNKNOWN")
				continue
			}
			trackID := unit.ID + ":subtitle:" + id
			unit.Tracks = append(unit.Tracks, Track{ID: trackID, Kind: "SUBTITLE", Language: str(sub["lan"]), LanguageName: str(sub["lan_doc"]), SubtitleType: origin, Availability: "AVAILABLE", Source: Source{URL: normalizeURL(locator), Version: b.sourceVersion(trackID), Headers: b.sourceHeaders()}})
		}
	}
	// Endpoint is addressable by CID. Availability is established by the metadata
	// view response, not inferred from a fabricated zero byte count.
	track := Track{ID: unit.ID + ":danmaku", Kind: "DANMAKU", SubtitleType: "NOT_APPLICABLE", Availability: "UNKNOWN", Reason: "DANMAKU_AVAILABILITY_UNKNOWN"}
	if err := b.danmakuAvailable(ctx, unit.ExternalID); err == nil {
		base := b.CommentBase
		if base == "" {
			base = "https://comment.bilibili.com"
		}
		track.Availability = "AVAILABLE"
		track.Reason = ""
		track.Source = Source{URL: strings.TrimRight(base, "/") + "/" + unit.ExternalID + ".xml", Version: b.sourceVersion(track.ID), Headers: b.sourceHeaders()}
	} else {
		if fatalBilibiliError(ctx, err) {
			return err
		}
		unit.Warnings = append(unit.Warnings, Classify(err, "METADATA").Code+"_DANMAKU")
	}
	unit.Tracks = append(unit.Tracks, track)
	return nil
}

func (b *Bilibili) bind(ctx context.Context) (*Bilibili, error) {
	if b.identity != "" {
		return b, nil
	}
	if b.Session == nil {
		return nil, Fail("AUTH_REQUIRED", "METADATA", false)
	}
	client, identity, err := b.Session.Bind(ctx)
	if err != nil {
		return nil, err
	}
	if client == nil || identity == "" {
		return nil, Fail("AUTH_REQUIRED", "METADATA", false)
	}
	bound := *b
	bound.Client, bound.identity = client, identity
	return &bound, nil
}

func (b *Bilibili) sourceHeaders() http.Header {
	h := mediaHeaders()
	h.Set(BilibiliIdentityHeader, b.identity)
	return h
}

func (b *Bilibili) sourceVersion(id string) string { return "bilibili:" + b.identity + ":" + id }

func fatalBilibiliError(ctx context.Context, err error) bool {
	return ctx.Err() != nil || Classify(err, "METADATA").Code == "AUTH_REQUIRED"
}

func firstString(item object, keys ...string) string {
	for _, key := range keys {
		if value := str(item[key]); value != "" {
			return value
		}
	}
	return ""
}

// Only explicit upstream audio language facts create LANGUAGE units. Shared
// video references retain the same Source.Version and external video identity.
func movieLanguages(unit Unit) []Unit {
	audios := []Track{}
	for _, track := range unit.Tracks {
		if track.Kind == "AUDIO" {
			if track.Language == "" {
				return []Unit{unit}
			}
			audios = append(audios, track)
		}
	}
	if len(audios) < 2 {
		return []Unit{unit}
	}
	result := []Unit{}
	for _, audio := range audios {
		variant := unit
		variant.ID = unit.ID + ":language:" + audio.Language
		variant.Kind = "LANGUAGE"
		variant.Language = audio.Language
		variant.LanguageName = audio.LanguageName
		variant.Title = audio.LanguageName
		if variant.Title == "" {
			variant.Title = audio.Language
		}
		variant.Tracks = nil
		for _, track := range unit.Tracks {
			if track.Kind == "AUDIO" && track.ID != audio.ID {
				continue
			}
			copy := track
			copy.ID = variant.ID + ":" + track.ID
			variant.Tracks = append(variant.Tracks, copy)
		}
		result = append(result, variant)
	}
	return result
}
func (b *Bilibili) danmakuAvailable(ctx context.Context, cid string) error {
	base := b.APIBase
	if base == "" {
		base = "https://api.bilibili.com"
	}
	req, err := http.NewRequestWithContext(ctx, "GET", strings.TrimRight(base, "/")+"/x/v2/dm/web/view?type=1&oid="+url.QueryEscape(cid), nil)
	if err != nil {
		return err
	}
	req.Header = mediaHeaders()
	client := b.Client
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Second}
	}
	res, err := client.Do(req)
	if err != nil {
		return Fail("NETWORK_TEMPORARY", "METADATA", true)
	}
	defer res.Body.Close()
	if res.StatusCode != 200 {
		return HTTPFailure(res.StatusCode, "METADATA")
	}
	raw, err := io.ReadAll(io.LimitReader(res.Body, 1<<20))
	if err != nil {
		return Fail("NETWORK_TEMPORARY", "METADATA", true)
	}
	if len(raw) == 0 {
		return Fail("DANMAKU_AVAILABILITY_UNKNOWN", "METADATA", false)
	}
	for i := 0; i < len(raw); {
		tag, n := readVarint(raw[i:])
		if n == 0 || tag>>3 == 0 {
			return Fail("DANMAKU_AVAILABILITY_UNKNOWN", "METADATA", false)
		}
		i += n
		switch tag & 7 {
		case 0:
			value, n := readVarint(raw[i:])
			if n == 0 {
				return Fail("DANMAKU_AVAILABILITY_UNKNOWN", "METADATA", false)
			}
			i += n
			if tag>>3 == 1 && value != 0 {
				return Fail("DANMAKU_UNAVAILABLE", "METADATA", false)
			}
		case 2:
			size, n := readVarint(raw[i:])
			if n == 0 || size > uint64(len(raw)-i-n) {
				return Fail("DANMAKU_AVAILABILITY_UNKNOWN", "METADATA", false)
			}
			i += n + int(size)
		case 1:
			i += 8
		case 5:
			i += 4
		default:
			return Fail("DANMAKU_AVAILABILITY_UNKNOWN", "METADATA", false)
		}
		if i > len(raw) {
			return Fail("DANMAKU_AVAILABILITY_UNKNOWN", "METADATA", false)
		}
	}
	return nil
}
func readVarint(raw []byte) (uint64, int) {
	var value uint64
	for i, b := range raw {
		if i >= 10 || i == 9 && b > 1 {
			return 0, 0
		}
		value |= uint64(b&127) << uint(7*i)
		if b < 128 {
			return value, i + 1
		}
	}
	return 0, 0
}
func subtitleOrigin(sub object) string {
	if strings.HasPrefix(str(sub["lan"]), "ai-") {
		return "AI"
	}
	kind, hasKind := sub["type"]
	ai, hasAI := sub["ai_type"]
	if (hasKind && integer(kind) != 0) || (hasAI && integer(ai) != 0) {
		return "AI"
	}
	if hasKind && hasAI {
		return "HUMAN"
	}
	return "UNKNOWN"
}
func normalizeURL(s string) string {
	if strings.HasPrefix(s, "//") {
		return "https:" + s
	}
	return s
}
func mediaHeaders() http.Header {
	return http.Header{"Referer": {"https://www.bilibili.com/"}, "User-Agent": {"Mozilla/5.0"}}
}
