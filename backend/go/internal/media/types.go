// Package media contains private platform facts and exact file execution plans.
// Wire models are translated by runner; these are not a second public protocol.
package media

import (
	"context"
	"errors"
	"net/http"
	"os"
	"syscall"
	"time"
)

type Failure struct {
	Code, Stage string
	Retryable   bool
}

func (e *Failure) Error() string                    { return e.Stage + ": " + e.Code }
func Fail(code, stage string, retryable bool) error { return &Failure{code, stage, retryable} }
func Classify(err error, stage string) *Failure {
	var f *Failure
	if errors.As(err, &f) {
		return f
	}
	if errors.Is(err, context.Canceled) {
		return &Failure{"CANCELLED", stage, false}
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return &Failure{"NETWORK_TIMEOUT", stage, true}
	}
	if errors.Is(err, os.ErrPermission) {
		return &Failure{"PERMISSION_DENIED", stage, false}
	}
	if errors.Is(err, os.ErrNotExist) {
		return &Failure{"LOCAL_FILE_NOT_FOUND", stage, false}
	}
	if errors.Is(err, syscall.Errno(112)) || errors.Is(err, syscall.Errno(39)) {
		return &Failure{"DISK_FULL", stage, false}
	}
	return &Failure{"IO_ERROR", stage, false}
}

type Reference struct{ Kind, ID string }
type Source struct {
	URL, Version string
	Headers      http.Header
	Size         *int64
	BackupURLs   []string `json:",omitempty"`
}
type Track struct {
	ID, Kind, Language, LanguageName, SubtitleType, Availability, Reason string
	Duration                                                             *float64
	Size                                                                 *int64
	Source                                                               Source
}
type Unit struct {
	ID, ExternalID, Kind, Title, Language, LanguageName string
	Index                                               int
	Duration                                            *float64
	Tracks                                              []Track
	Warnings                                            []string
}
type Metadata struct {
	ID, ExternalID, IdentifierType, Kind, Title, Description, CoverURL, Author, OriginalURL string
	PublishedAt                                                                             *time.Time
	Units                                                                                   []Unit
	Warnings                                                                                []string
}
type Provider interface {
	Fetch(context.Context, Reference) (Metadata, error)
}

type Input struct {
	TrackID, Kind string
	Source        Source
}
type Output struct {
	ID, RelativePath, Mode string
	Inputs                 []Input
}
type Plan struct {
	OperationID, ResourceKey string
	Outputs                  []Output
}
type FileFact struct {
	ID, RelativePath, SizeBytes, SHA256 string
	Duration                            *float64
	Streams                             []string
}

// Selectable never treats an unclassified subtitle as human-authored.
func (t Track) Selectable() bool {
	return t.Availability == "AVAILABLE" && (t.Kind != "SUBTITLE" || t.SubtitleType == "HUMAN")
}
