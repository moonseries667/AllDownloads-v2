package media

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestBilibiliMetadataMultiPartSeasonsAndSubtitleEvidence(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/x/web-interface/view":
			fmt.Fprint(w, `{"code":0,"data":{"bvid":"BVfixture","title":"标题","pubdate":1700000000,"owner":{"name":"作者"},"pages":[{"cid":11,"part":"开场","duration":12},{"cid":12,"part":"","duration":13}]}}`)
		case "/x/web-interface/nav":
			fmt.Fprint(w, `{"code":-101,"data":{"wbi_img":{"img_url":"https://example/abcdefghijklmnopqrstuvwxyz012345.png","sub_url":"https://example/ABCDEFGHIJKLMNOPQRSTUVWXYZ678901.png"}}}`)
		case "/x/player/wbi/playurl", "/pgc/player/web/v2/playurl":
			if r.URL.Path == "/x/player/wbi/playurl" && r.URL.Query().Get("w_rid") == "" {
				t.Error("missing platform signature")
			}
			fmt.Fprint(w, `{"code":0,"data":{"timelength":12000,"dash":{"video":[{"id":80,"codecid":7,"bandwidth":100,"baseUrl":"https://cdn.invalid/lower"},{"id":120,"codecid":7,"bandwidth":300,"baseUrl":"https://cdn.invalid/high"}],"audio":[{"id":30280,"bandwidth":200,"baseUrl":"https://cdn.invalid/audio"}]}}}`)
		case "/x/player/wbi/v2":
			fmt.Fprint(w, `{"code":0,"data":{"subtitle":{"subtitles":[{"id":9007199254740993,"id_str":"9007199254740993","lan":"en","lan_doc":"英语","subtitle_url":"https://cdn.invalid/human","type":0,"ai_type":0},{"id":2,"lan":"ai-zh","subtitle_url":"https://cdn.invalid/ai","type":1,"ai_type":1},{"id":3,"lan":"zh","subtitle_url":"https://cdn.invalid/unknown"}]}}}`)
		case "/x/v2/dm/web/view":
			w.Header().Set("Content-Type", "application/octet-stream")
			w.Write([]byte{8, 0, 18, 0})
		case "/pgc/view/web/season":
			id := r.URL.Query().Get("season_id")
			fmt.Fprintf(w, `{"code":0,"result":{"season_id":%s,"title":"同名番剧","type":1,"episodes":[{"id":%s1,"cid":%s2,"bvid":"BVepisode","long_title":"第一个故事","duration":12000}]}}`, id, id, id)
		default:
			t.Errorf("metadata attempted unexpected request %s", r.URL.Path)
			w.WriteHeader(500)
		}
	}))
	defer server.Close()
	provider := &Bilibili{Client: server.Client(), APIBase: server.URL}
	meta, err := provider.Fetch(context.Background(), Reference{"BVID", "BVfixture"})
	if err != nil {
		t.Fatal(err)
	}
	if meta.Kind != "MULTIPART" || len(meta.Units) != 2 || meta.PublishedAt == nil {
		t.Fatalf("metadata %#v", meta)
	}
	for _, unit := range meta.Units {
		if len(unit.Tracks) != 4 {
			t.Fatalf("tracks %#v", unit.Tracks)
		}
		if !strings.Contains(unit.Tracks[0].ID, ":120:") || unit.Tracks[0].Size != nil || unit.Tracks[0].Duration == nil {
			t.Fatal("quality or unknown size lost")
		}
		if unit.Tracks[2].SubtitleType != "HUMAN" || !strings.HasSuffix(unit.Tracks[2].ID, "9007199254740993") {
			t.Fatal("subtitle classification or large identifier lost")
		}
		if len(unit.Warnings) != 2 {
			t.Fatalf("classification warnings %v", unit.Warnings)
		}
	}
	first, err := provider.Fetch(context.Background(), Reference{"SEASON_ID", "101"})
	if err != nil {
		t.Fatal(err)
	}
	second, err := provider.Fetch(context.Background(), Reference{"SEASON_ID", "102"})
	if err != nil {
		t.Fatal(err)
	}
	if first.ID == second.ID || first.Units[0].ID == second.Units[0].ID || first.Units[0].Kind != "EPISODE" {
		t.Fatal("seasons merged")
	}
}
func TestBilibiliFailuresAndAbsentClassification(t *testing.T) {
	for _, code := range []int{-404, -403, -101, -412, 777} {
		t.Run(fmt.Sprint(code), func(t *testing.T) {
			s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				json.NewEncoder(w).Encode(map[string]any{"code": code, "message": "private details"})
			}))
			defer s.Close()
			_, err := (&Bilibili{Client: s.Client(), APIBase: s.URL}).Fetch(context.Background(), Reference{"BVID", "BVfixture"})
			f := Classify(err, "")
			if err == nil || strings.Contains(err.Error(), "private") {
				t.Fatal(err)
			}
			if code == -404 && f.Code != "RESOURCE_DELETED" || code != -404 && f.Code == "RESOURCE_DELETED" {
				t.Fatal(err)
			}
		})
	}
	if subtitleOrigin(object{"lan": "en"}) != "UNKNOWN" {
		t.Fatal("unknown classified as human")
	}
	if subtitleOrigin(object{"type": json.Number("0"), "ai_type": json.Number("1")}) != "AI" {
		t.Fatal("AI field ignored")
	}
}

func TestNativeMovieLanguagesKeepSharedVideoAndBestAudioPerLanguage(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/pgc/view/web/season":
			fmt.Fprint(w, `{"code":0,"result":{"season_id":10,"title":"电影","type":2,"episodes":[{"id":100,"cid":200,"bvid":"BVmovie","long_title":"电影"}]}}`)
		case "/pgc/player/web/v2/playurl":
			fmt.Fprint(w, `{"code":0,"result":{"video_info":{"dash":{"video":[{"id":80,"baseUrl":"https://cdn.invalid/video"}],"audio":[{"id":30280,"bandwidth":200,"language":"zh-CN","language_doc":"普通话","baseUrl":"https://cdn.invalid/zh"},{"id":30216,"bandwidth":100,"language":"zh-CN","language_doc":"普通话","baseUrl":"https://cdn.invalid/zh-low"},{"id":30280,"bandwidth":200,"language":"en","language_doc":"英语","baseUrl":"https://cdn.invalid/en"}]}}}}`)
		case "/x/player/wbi/v2":
			fmt.Fprint(w, `{"code":0,"data":{"subtitle":{"subtitles":[]}}}`)
		case "/x/v2/dm/web/view":
			w.Write([]byte{8, 0})
		default:
			t.Error(r.URL.Path)
			w.WriteHeader(500)
		}
	}))
	defer server.Close()
	meta, err := (&Bilibili{Client: server.Client(), APIBase: server.URL}).Fetch(context.Background(), Reference{"SEASON_ID", "10"})
	if err != nil {
		t.Fatal(err)
	}
	if meta.Kind != "MOVIE" || len(meta.Units) != 2 || meta.Units[0].LanguageName != "普通话" || meta.Units[1].LanguageName != "英语" {
		t.Fatalf("movie=%#v", meta)
	}
	first, second := meta.Units[0], meta.Units[1]
	if first.Kind != "LANGUAGE" || first.ExternalID != second.ExternalID || first.Tracks[0].Source.Version != second.Tracks[0].Source.Version || first.Tracks[0].ID == second.Tracks[0].ID {
		t.Fatal("shared video relation lost")
	}
	if len(first.Tracks) != 3 || first.Tracks[1].Source.URL != "https://cdn.invalid/zh" {
		t.Fatal("did not select highest audio per language")
	}
}
