// Package runner implements contracts at d508c7a (protocolVersion 1).
package runner

import (
	"encoding/json"
	"time"
)

type Operation struct {
	ProtocolVersion int             `json:"protocolVersion"`
	OperationID     string          `json:"operationId"`
	TaskID          string          `json:"taskId"`
	Attempt         int             `json:"attempt"`
	LeaseID         string          `json:"leaseId"`
	RunnerID        string          `json:"runnerId"`
	FencingToken    string          `json:"fencingToken"`
	LeaseExpiresAt  time.Time       `json:"leaseExpiresAt"`
	OperationType   string          `json:"operationType"`
	Platform        string          `json:"platform"`
	AccountRef      *string         `json:"accountRef"`
	ResourceKey     string          `json:"resourceKey"`
	StorageRevision string          `json:"storageRevision"`
	Destination     *Destination    `json:"destination"`
	Payload         json.RawMessage `json:"payload"`
}
type Destination struct {
	RootID       string `json:"rootId"`
	RootPath     string `json:"rootPath"`
	RelativePath string `json:"relativePath"`
}
type Track struct {
	ID             string   `json:"id"`
	Kind           string   `json:"kind"`
	Available      bool     `json:"available"`
	ReasonCode     *string  `json:"reasonCode"`
	Language       *string  `json:"language"`
	LanguageLabel  *string  `json:"languageLabel"`
	SubtitleOrigin string   `json:"subtitleOrigin"`
	Duration       *float64 `json:"durationSeconds"`
	Size           *string  `json:"sizeBytes"`
}
type Unit struct {
	ID                 string   `json:"id"`
	ContentID          string   `json:"contentId"`
	ExternalID         string   `json:"externalId"`
	Title              string   `json:"title"`
	Index              int      `json:"index"`
	Kind               string   `json:"kind"`
	Language           *string  `json:"language"`
	LanguageLabel      *string  `json:"languageLabel"`
	Duration           *float64 `json:"durationSeconds"`
	Availability       string   `json:"availability"`
	AvailabilityReason *string  `json:"availabilityReason"`
	Tracks             []Track  `json:"tracks"`
}
type Output struct {
	ID           string   `json:"id"`
	RelativePath string   `json:"relativePath"`
	TrackIDs     []string `json:"trackIds"`
	Kind         string   `json:"kind"`
	Size         *string  `json:"sizeBytes"`
}
type DownloadPayload struct {
	SchemaVersion   int      `json:"schemaVersion"`
	ContentID       string   `json:"contentId"`
	ContentRevision string   `json:"contentRevision"`
	ContentKind     string   `json:"contentKind"`
	ExternalID      string   `json:"externalId"`
	ExternalIDType  string   `json:"externalIdType"`
	QualityPolicy   string   `json:"qualityPolicy"`
	ArchivePolicy   string   `json:"archivePolicy"`
	Units           []Unit   `json:"units"`
	Outputs         []Output `json:"outputs"`
}
type Source struct {
	ID              string     `json:"id"`
	Platform        string     `json:"platform"`
	Kind            string     `json:"kind"`
	Title           string     `json:"title"`
	Alias           *string    `json:"alias"`
	URL             string     `json:"url"`
	Status          string     `json:"status"`
	LastSyncedAt    *time.Time `json:"lastSyncedAt"`
	ItemCount       int        `json:"itemCount"`
	Revision        string     `json:"revision"`
	Pinned          bool       `json:"pinned"`
	NavigationGroup string     `json:"navigationGroup"`
	DisplayLabel    string     `json:"displayLabel"`
}
type Content struct {
	ID                 string            `json:"id"`
	Platform           string            `json:"platform"`
	ExternalID         string            `json:"externalId"`
	ExternalIDType     string            `json:"externalIdType"`
	Kind               string            `json:"kind"`
	Revision           string            `json:"revision"`
	Title              string            `json:"title"`
	CoverURL           *string           `json:"coverUrl"`
	SourceIDs          []string          `json:"sourceIds"`
	OriginalURL        string            `json:"originalUrl"`
	Availability       string            `json:"availability"`
	AvailabilityReason *string           `json:"availabilityReason"`
	DownloadSummary    map[string]string `json:"downloadSummary"`
	ArchiveSummary     map[string]string `json:"archiveSummary"`
	UpdatedAt          time.Time         `json:"updatedAt"`
	PublishedAt        *time.Time        `json:"publishedAt"`
	Author             map[string]string `json:"author"`
	Description        string            `json:"description"`
	SeasonID           *string           `json:"seasonId"`
	Sources            []Source          `json:"sources"`
	MediaUnits         []Unit            `json:"mediaUnits"`
	Warnings           []string          `json:"warnings"`
}
type File struct {
	OutputID     string   `json:"outputId"`
	RelativePath string   `json:"relativePath"`
	SizeBytes    string   `json:"sizeBytes"`
	SHA256       string   `json:"sha256"`
	MediaKinds   []string `json:"mediaKinds"`
	Duration     *float64 `json:"durationSeconds"`
	Verified     bool     `json:"verified"`
}
type ResultError struct {
	Code              string   `json:"code"`
	Message           string   `json:"message"`
	RetryAfterSeconds *int     `json:"retryAfterSeconds"`
	CompletedEffects  []string `json:"completedEffects"`
	PendingEffects    []string `json:"pendingEffects"`
}
type Result struct {
	EventID      string       `json:"eventId"`
	RunnerID     string       `json:"runnerId"`
	LeaseID      string       `json:"leaseId"`
	Attempt      int          `json:"attempt"`
	FencingToken string       `json:"fencingToken"`
	Status       string       `json:"status"`
	Result       any          `json:"result"`
	Error        *ResultError `json:"error"`
}
