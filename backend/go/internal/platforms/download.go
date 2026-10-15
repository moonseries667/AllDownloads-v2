package platforms

import (
	"context"
	"encoding/hex"
	"path"
	"strconv"
	"strings"
)

// File is an internal binding of a frozen output to a private provider locator.
// The locator never enters the result JSON or browser-facing catalogue.
type File struct {
	Key    string
	Output Output
	Open   OpenSource
}

func ValidateOutputs(outputs []Output) error {
	ids, paths := map[string]bool{}, map[string]bool{}
	if len(outputs) == 0 {
		return NewFault("INVALID_INPUT", "PLAN")
	}
	for _, o := range outputs {
		if o.ID == "" || ids[o.ID] || paths[strings.ToLower(o.RelativePath)] || o.RelativePath == "" || path.IsAbs(o.RelativePath) || path.Clean(o.RelativePath) != o.RelativePath || strings.ContainsAny(o.RelativePath, "\\:\x00") || o.RelativePath == ".." || strings.HasPrefix(o.RelativePath, "../") || strings.HasPrefix(strings.ToLower(o.RelativePath), ".work/") {
			return NewFault("INVALID_INPUT", "PLAN")
		}
		if o.SizeBytes != nil {
			n, e := strconv.ParseUint(*o.SizeBytes, 10, 64)
			if e != nil || strconv.FormatUint(n, 10) != *o.SizeBytes {
				return NewFault("INVALID_INPUT", "PLAN")
			}
		}
		ids[o.ID] = true
		paths[strings.ToLower(o.RelativePath)] = true
	}
	return nil
}

func CheckEvidence(o Output, e FileEvidence) error {
	size, err := strconv.ParseUint(e.SizeBytes, 10, 64)
	hash, hashErr := hex.DecodeString(e.SHA256)
	if err != nil || strconv.FormatUint(size, 10) != e.SizeBytes || hashErr != nil || len(hash) != 32 || strings.ToLower(e.SHA256) != e.SHA256 || !e.Verified || e.OutputID != o.ID || e.RelativePath != o.RelativePath || (o.SizeBytes != nil && *o.SizeBytes != e.SizeBytes) || len(e.MediaKinds) != 1 || e.MediaKinds[0] != o.Kind {
		return NewFault("VERIFICATION_FAILED", "VERIFY")
	}
	return nil
}

// ExecuteFiles preserves a partial manifest when later files fail. Java owns
// retry timing; a repeated call asks the shared publisher to revalidate/reuse
// completed files, and only missing files need their provider source opened.
func ExecuteFiles(ctx context.Context, publisher Publisher, files []File) (DownloadResult, error) {
	result := DownloadResult{Files: []FileEvidence{}, Warnings: []string{}}
	outputs := make([]Output, len(files))
	for i, f := range files {
		outputs[i] = f.Output
	}
	if err := ValidateOutputs(outputs); err != nil {
		return result, err
	}
	if publisher == nil {
		return result, NewFault("PROVIDER_UNAVAILABLE", "DOWNLOAD")
	}
	for i, file := range files {
		var evidence FileEvidence
		err := ctx.Err()
		if err == nil {
			evidence, err = publisher.Ensure(ctx, file.Key, file.Output, file.Open)
		}
		if err == nil {
			err = CheckEvidence(file.Output, evidence)
		}
		if err != nil {
			fault := SafeFault(err, "DOWNLOAD")
			for _, done := range result.Files {
				fault.CompletedEffects = append(fault.CompletedEffects, done.OutputID)
			}
			for _, pending := range files[i:] {
				fault.PendingEffects = append(fault.PendingEffects, pending.Output.ID)
			}
			return result, fault
		}
		result.Files = append(result.Files, evidence)
	}
	return result, nil
}
