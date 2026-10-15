package media

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/moonseries667/AllDownloads-v2/backend/go/internal/executor"
)

type Downloader struct{ Client *http.Client }
type downloadState struct {
	Version, ETag string
	Total         int64
	Complete      bool
	SavedBytes    int64
	SHA256        string
}

func (d Downloader) Fetch(ctx context.Context, source Source, path string) error {
	if source.Version == "" || source.URL == "" {
		return Fail("RESOURCE_VERSION_MISSING", "DOWNLOAD", false)
	}
	client := d.Client
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Minute}
	}
	var state downloadState
	raw, err := os.ReadFile(path + ".json")
	if err == nil {
		if json.Unmarshal(raw, &state) != nil {
			return Fail("INVALID_DOWNLOAD_STATE", "DOWNLOAD", false)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	var offset int64
	if info, err := os.Stat(path); err == nil {
		offset = info.Size()
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if state.Version != "" && state.Version != source.Version {
		return Fail("RESOURCE_VERSION_CHANGED", "DOWNLOAD", false)
	}
	if offset > 0 && state.SavedBytes == offset && state.SHA256 != "" {
		actual, err := hashFile(path)
		if err != nil {
			return err
		}
		if actual != state.SHA256 {
			return Fail("PARTIAL_FILE_CORRUPT", "DOWNLOAD", false)
		}
	} else {
		offset = 0
	}
	if state.Complete && offset == state.Total && state.SHA256 != "" {
		return nil
	}
	// Only a strong validator authorizes range reuse. Otherwise restart this file.
	if state.ETag == "" || strings.HasPrefix(state.ETag, "W/") || state.Version == "" {
		offset = 0
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, source.URL, nil)
	if err != nil {
		return Fail("INVALID_MEDIA_URL", "DOWNLOAD", false)
	}
	req.Header = source.Headers.Clone()
	if req.Header == nil {
		req.Header = make(http.Header)
	}
	req.Header.Set("Accept-Encoding", "identity")
	if offset > 0 {
		req.Header.Set("Range", "bytes="+strconv.FormatInt(offset, 10)+"-")
		req.Header.Set("If-Range", state.ETag)
	}
	resp, err := client.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return Fail("NETWORK_ERROR", "DOWNLOAD", true)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 && resp.StatusCode != 206 {
		return HTTPFailure(resp.StatusCode, "DOWNLOAD")
	}
	etag := resp.Header.Get("ETag")
	total := resp.ContentLength
	if resp.StatusCode == 206 {
		start, end, full, ok := parseRange(resp.Header.Get("Content-Range"))
		if !ok || offset == 0 || start != offset || end < start || full <= end || etag != state.ETag || (state.Total >= 0 && full != state.Total) || (resp.ContentLength >= 0 && resp.ContentLength != end-start+1) {
			return Fail("RESOURCE_VERSION_CHANGED", "DOWNLOAD", false)
		}
		total = full
	} else {
		offset = 0
	}
	if source.Size != nil && total >= 0 && *source.Size != total {
		return Fail("RESOURCE_VERSION_CHANGED", "DOWNLOAD", false)
	}
	flags := os.O_CREATE | os.O_WRONLY
	if offset == 0 {
		flags |= os.O_TRUNC
	}
	file, err := os.OpenFile(path, flags, 0600)
	if err != nil {
		return err
	}
	if _, err := file.Seek(offset, io.SeekStart); err != nil {
		file.Close()
		return err
	}
	state = downloadState{Version: source.Version, ETag: etag, Total: total}
	if err := saveDownloadState(path, state); err != nil {
		file.Close()
		return err
	}
	n, copyErr := io.Copy(file, resp.Body)
	persistErr := errors.Join(file.Sync(), file.Close())
	if persistErr != nil {
		return persistErr
	}
	state.SavedBytes = offset + n
	state.SHA256, err = hashFile(path)
	if err != nil {
		return err
	}
	if err := saveDownloadState(path, state); err != nil {
		return err
	}
	err = copyErr
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		var fileError *os.PathError
		if errors.As(err, &fileError) {
			return err
		}
		return Fail("NETWORK_INTERRUPTED", "DOWNLOAD", true)
	}
	actual := offset + n
	if (total >= 0 && actual != total) || (source.Size != nil && actual != *source.Size) {
		return Fail("INCOMPLETE_DOWNLOAD", "DOWNLOAD", true)
	}
	state.Total = actual
	state.Complete = true
	return saveDownloadState(path, state)
}

func parseRange(s string) (int64, int64, int64, bool) {
	if !strings.HasPrefix(s, "bytes ") {
		return 0, 0, 0, false
	}
	pair := strings.Split(strings.TrimPrefix(s, "bytes "), "/")
	if len(pair) != 2 {
		return 0, 0, 0, false
	}
	bounds := strings.Split(pair[0], "-")
	if len(bounds) != 2 {
		return 0, 0, 0, false
	}
	a, e1 := strconv.ParseInt(bounds[0], 10, 64)
	b, e2 := strconv.ParseInt(bounds[1], 10, 64)
	c, e3 := strconv.ParseInt(pair[1], 10, 64)
	return a, b, c, e1 == nil && e2 == nil && e3 == nil && a >= 0
}
func saveDownloadState(path string, state downloadState) error {
	return executor.WriteStateFile(path+".json", state)
}
func hashFile(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
func HTTPFailure(status int, stage string) error {
	switch status {
	case 401:
		return Fail("AUTH_REQUIRED", stage, false)
	case 403:
		return Fail("PERMISSION_DENIED", stage, false)
	case 404, 410:
		return Fail("RESOURCE_UNAVAILABLE", stage, false)
	case 429:
		return Fail("RATE_LIMITED", stage, true)
	}
	if status >= 500 {
		return Fail("NETWORK_ERROR", stage, true)
	}
	return Fail("PLATFORM_RESPONSE_UNKNOWN", stage, false)
}
