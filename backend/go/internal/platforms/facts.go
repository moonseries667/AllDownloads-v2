// Package platforms contains the Telegram/Quark boundary types from contract
// 6233234. It supplies facts and frozen file plans, never business scheduling.
package platforms

import (
	"context"
	"errors"
	"io"
)

// Fault has safe, fixed messages. Provider errors must be translated at the
// provider boundary; raw URLs, session data and stderr are never wire messages.
type Fault struct {
	Code              string   `json:"code"`
	Message           string   `json:"message"`
	Stage             string   `json:"stage"`
	RetryAfterSeconds *int     `json:"retryAfterSeconds"`
	CompletedEffects  []string `json:"completedEffects"`
	PendingEffects    []string `json:"pendingEffects"`
	Status            string   `json:"-"`
}

func (f *Fault) Error() string { return f.Code + ": " + f.Message }

func NewFault(code, stage string) *Fault {
	f := &Fault{Code: code, Stage: stage, Status: "NEEDS_ACTION", CompletedEffects: []string{}, PendingEffects: []string{}}
	switch code {
	case "CANCELLED":
		f.Status, f.Message = "CANCELLED", "操作已取消，已完成文件保留"
	case "NETWORK_TEMPORARY":
		f.Status, f.Message = "RETRYABLE_ERROR", "平台请求暂时失败，可重试未完成部分"
	case "RATE_LIMITED":
		f.Status, f.Message = "RETRYABLE_ERROR", "平台要求等待后重试"
	case "AUTH_REQUIRED":
		f.Message = "平台账号需要重新登录"
	case "PERMISSION_DENIED":
		f.Message = "账号没有访问或写入该对象的权限"
	case "RESOURCE_DELETED":
		f.Message = "平台已确认对象不存在"
	case "VERSION_CHANGED":
		f.Message = "平台对象或目标目录已变化，需要重新获取并确认"
	case "GROUP_INCOMPLETE":
		f.Message = "媒体组尚未完整收集，保留扫描事实等待补齐"
	case "RESULT_UNKNOWN":
		f.Status, f.Message = "RESULT_UNKNOWN", "平台操作结果尚未确认，需要核对"
	case "PROVIDER_UNAVAILABLE":
		f.Message = "平台适配器尚未配置或尚不支持该操作"
	case "VERIFICATION_FAILED":
		f.Message = "文件事实与冻结清单不一致"
	case "INVALID_INPUT":
		f.Message = "执行计划或平台对象字段无效"
	default:
		f.Code, f.Message = "PROVIDER_CHANGED", "平台返回的数据不完整或不符合已确认结构"
	}
	return f
}

func SafeFault(err error, stage string) *Fault {
	if errors.Is(err, context.Canceled) {
		return NewFault("CANCELLED", stage)
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return NewFault("NETWORK_TEMPORARY", stage)
	}
	var f *Fault
	if errors.As(err, &f) {
		copy := NewFault(f.Code, stage)
		copy.RetryAfterSeconds = f.RetryAfterSeconds
		return copy
	}
	return NewFault("NETWORK_TEMPORARY", stage)
}

type Output struct {
	ID           string   `json:"id"`
	RelativePath string   `json:"relativePath"`
	TrackIDs     []string `json:"trackIds"`
	Kind         string   `json:"kind"`
	SizeBytes    *string  `json:"sizeBytes"`
	AttachmentID string   `json:"attachmentId,omitempty"`
	NodeID       string   `json:"nodeId,omitempty"`
}

type FileEvidence struct {
	OutputID        string   `json:"outputId"`
	RelativePath    string   `json:"relativePath"`
	SizeBytes       string   `json:"sizeBytes"`
	SHA256          string   `json:"sha256"`
	MediaKinds      []string `json:"mediaKinds"`
	DurationSeconds *float64 `json:"durationSeconds"`
	Verified        bool     `json:"verified"`
}

type DownloadResult struct {
	Files    []FileEvidence `json:"files"`
	Warnings []string       `json:"warnings"`
}

// OpenSource is lazy: a publisher revalidates its existing evidence before it
// decides to call Open. Providers must enforce the requested immutable version.
type OpenSource func(context.Context) (io.ReadCloser, error)

// Publisher is supplied by the shared runner's receipt/publication core. The
// platform modules do not keep another receipt store or write final paths.
// Ensure must compare the immutable source key, output plan and existing bytes,
// persist intent before I/O, and return evidence only after safe publication.
type Publisher interface {
	Ensure(context.Context, string, Output, OpenSource) (FileEvidence, error)
}
