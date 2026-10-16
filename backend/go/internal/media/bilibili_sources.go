package media

import (
	"context"
	"net/url"
	"strconv"
	"strings"
)

type SourceProvider interface {
	FetchSource(context.Context, string, string, string) ([]Metadata, string, error)
}

// FetchSource does not return a complete snapshot until every page and content
// was obtained. Any failure preserves Java's previously published membership.
func (b *Bilibili) FetchSource(ctx context.Context, kind, id, sourceURL string) ([]Metadata, string, error) {
	bound, err := b.bind(ctx)
	if err != nil {
		return nil, "", err
	}
	b = bound
	parsed, err := url.Parse(sourceURL)
	if err != nil {
		return nil, "", Fail("INVALID_SOURCE", "METADATA", false)
	}
	ids := []string{}
	seen := map[string]bool{}
	title := ""
	for page := 1; page <= 10000; page++ {
		var data object
		var err error
		more := false
		switch kind {
		case "FAVORITE":
			data, err = b.api(ctx, "/x/v3/fav/resource/list", url.Values{"media_id": {strings.TrimPrefix(id, "ml")}, "pn": {strconv.Itoa(page)}, "ps": {"20"}, "platform": {"web"}})
			if err != nil {
				return nil, "", err
			}
			title = str(obj(data["info"])["title"])
			for _, entry := range list(data["medias"]) {
				bvid := str(obj(entry)["bvid"])
				if bvid == "" {
					return nil, "", Fail("SOURCE_MEMBER_UNKNOWN", "METADATA", false)
				}
				if !seen[bvid] {
					seen[bvid] = true
					ids = append(ids, bvid)
				}
			}
			more = bval(data["has_more"])
			if _, ok := data["has_more"].(bool); !ok {
				return nil, "", Fail("SOURCE_COMPLETENESS_UNKNOWN", "METADATA", false)
			}
		case "COLLECTION":
			q := parsed.Query()
			mid := q.Get("mid")
			season := q.Get("season_id")
			segments := strings.Split(strings.Trim(parsed.Path, "/"), "/")
			if mid == "" && len(segments) > 0 {
				mid = segments[0]
			}
			if season == "" {
				season = id
			}
			if _, err := strconv.ParseUint(mid, 10, 64); err != nil {
				return nil, "", Fail("COLLECTION_OWNER_REQUIRED", "METADATA", false)
			}
			data, err = b.api(ctx, "/x/polymer/web-space/seasons_archives_list", url.Values{"mid": {mid}, "season_id": {season}, "page_num": {strconv.Itoa(page)}, "page_size": {"30"}})
			if err != nil {
				return nil, "", err
			}
			title = str(obj(data["meta"])["name"])
			for _, entry := range list(data["archives"]) {
				bvid := str(obj(entry)["bvid"])
				if bvid == "" {
					return nil, "", Fail("SOURCE_MEMBER_UNKNOWN", "METADATA", false)
				}
				if !seen[bvid] {
					seen[bvid] = true
					ids = append(ids, bvid)
				}
			}
			pageInfo := obj(data["page"])
			total := integer(pageInfo["total"])
			if _, ok := pageInfo["total"]; !ok {
				return nil, "", Fail("SOURCE_COMPLETENESS_UNKNOWN", "METADATA", false)
			}
			more = int64(page*30) < total
		default:
			return nil, "", Fail("SOURCE_TYPE_UNSUPPORTED", "METADATA", false)
		}
		if !more {
			result := make([]Metadata, 0, len(ids))
			for _, bvid := range ids {
				meta, err := b.Fetch(ctx, Reference{"BVID", bvid})
				if err != nil {
					return nil, "", err
				}
				result = append(result, meta)
			}
			return result, title, nil
		}
	}
	return nil, "", Fail("SOURCE_SCAN_LIMIT", "METADATA", false)
}
