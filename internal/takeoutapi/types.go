package takeoutapi

import (
	"encoding/json"
	"fmt"
	"time"
)

// ExportStatus is the parsed state of a Takeout export.
type ExportStatus int

const (
	StatusUnknown ExportStatus = iota
	StatusInProgress
	StatusComplete
	StatusFailed
	StatusExpired
)

func (s ExportStatus) String() string {
	switch s {
	case StatusInProgress:
		return "in_progress"
	case StatusComplete:
		return "complete"
	case StatusFailed:
		return "failed"
	case StatusExpired:
		return "expired"
	default:
		return "unknown"
	}
}

// FileEntry is one downloadable archive within a completed export.
type FileEntry struct {
	Filename string
	Size     int64
	Index    int // the "i=" parameter in the download URL
}

// Export is a parsed Takeout export from the fhjYTc list response.
type Export struct {
	UUID         string
	UserID       string    // owning Google user-id-number; required for download URLs
	CreatedAt    time.Time
	CompletedAt  time.Time // zero for in-progress exports
	ExpiresAt    time.Time // zero for in-progress exports
	Status       ExportStatus
	Files        []FileEntry
	DownloadURLs []string // computed when Status == StatusComplete
	TotalBytes   int64
}

// CreateExportOptions configures a new export request (sent to U5lrKc).
type CreateExportOptions struct {
	Products  []string // slugs like "drive", "gmail", "bond" (Access Log Activity)
	Format    string   // "ZIP" or "TGZ"
	SizeBytes int64    // archive split size, e.g. 2*1024*1024*1024 for 2 GB
	Frequency string   // "once" | "every_2_months"
}

// parseExportListResponse extracts Export structs from an fhjYTc response payload.
// Top-level shape (per 2026-05-02 capture of a completed export):
//
//	[ <action-prefix-or-list>,                     # [0]
//	  [[null, <export-fields>], ...],              # [1] wrapper of exports
//	  null,                                         # [2]
//	  "<google-user-id-number>",                    # [3]
//	  false,                                        # [4]
//	  [<duplicate of [1] flattened>]                # [5]
//	]
func parseExportListResponse(raw json.RawMessage) ([]*Export, error) {
	var top []json.RawMessage
	if err := json.Unmarshal(raw, &top); err != nil {
		return nil, fmt.Errorf("unmarshal top: %w", err)
	}
	if len(top) < 4 {
		return nil, fmt.Errorf("response too short: %d top-level elements", len(top))
	}

	var userID string
	_ = json.Unmarshal(top[3], &userID) // tolerate absent userID

	var wrapper [][]json.RawMessage
	if err := json.Unmarshal(top[1], &wrapper); err != nil {
		return nil, fmt.Errorf("unmarshal wrapper: %w", err)
	}

	var exports []*Export
	for _, w := range wrapper {
		if len(w) < 2 {
			continue
		}
		exp, err := parseExportFields(w[1])
		if err != nil {
			continue // skip malformed
		}
		exp.UserID = userID
		exp.buildDownloadURLs()
		exports = append(exports, exp)
	}
	return exports, nil
}

// parseExportFields parses one export's positional-indexed fields.
// Positions per the 2026-05-02 spike + completed-export capture:
//
//	[1]  UUID
//	[6]  total bytes
//	[7]  product catalog (we don't currently use)
//	[8]  files array — [[filename, size, dlCount, null, history, freq, null, "", index], ...]
//	[9]  status code: 0 = in_progress, 100 = complete (others TBD)
//	[22] creation Unix ms
//	[23] completion Unix ms (only present for completed)
//	[24] expiration Unix ms (only present for completed)
//	[27] manifest file (single FileEntry-shaped array, only present for completed)
func parseExportFields(raw json.RawMessage) (*Export, error) {
	var fields []json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		return nil, fmt.Errorf("unmarshal fields: %w", err)
	}
	if len(fields) < 23 {
		return nil, fmt.Errorf("export fields too short: %d", len(fields))
	}

	exp := &Export{}

	if err := json.Unmarshal(fields[1], &exp.UUID); err != nil {
		return nil, fmt.Errorf("parsing UUID: %w", err)
	}

	if len(fields) > 6 {
		_ = json.Unmarshal(fields[6], &exp.TotalBytes)
	}

	if len(fields) > 8 {
		exp.Files = append(exp.Files, parseFileArray(fields[8])...)
	}

	var statusCode int
	if err := json.Unmarshal(fields[9], &statusCode); err == nil {
		exp.Status = mapStatusCode(statusCode)
	}

	var createdMs int64
	if err := json.Unmarshal(fields[22], &createdMs); err == nil {
		exp.CreatedAt = time.UnixMilli(createdMs)
	}

	if len(fields) > 23 {
		var ms int64
		if err := json.Unmarshal(fields[23], &ms); err == nil && ms > 0 {
			exp.CompletedAt = time.UnixMilli(ms)
		}
	}
	if len(fields) > 24 {
		var ms int64
		if err := json.Unmarshal(fields[24], &ms); err == nil && ms > 0 {
			exp.ExpiresAt = time.UnixMilli(ms)
		}
	}

	if len(fields) > 27 {
		// Manifest file — single entry rather than an array of entries.
		if mf := parseFileEntry(fields[27]); mf != nil {
			exp.Files = append(exp.Files, *mf)
		}
	}

	return exp, nil
}

// parseFileArray parses fields[8] — a list of FileEntry-shaped arrays.
func parseFileArray(raw json.RawMessage) []FileEntry {
	var rows []json.RawMessage
	if err := json.Unmarshal(raw, &rows); err != nil {
		return nil
	}
	out := make([]FileEntry, 0, len(rows))
	for _, r := range rows {
		if f := parseFileEntry(r); f != nil {
			out = append(out, *f)
		}
	}
	return out
}

// parseFileEntry parses one [filename, size, dlCount, null, history, freq, null, "", index] row.
func parseFileEntry(raw json.RawMessage) *FileEntry {
	var row []json.RawMessage
	if err := json.Unmarshal(raw, &row); err != nil {
		return nil
	}
	if len(row) < 9 {
		return nil
	}
	var f FileEntry
	if err := json.Unmarshal(row[0], &f.Filename); err != nil || f.Filename == "" {
		return nil
	}
	_ = json.Unmarshal(row[1], &f.Size)
	_ = json.Unmarshal(row[8], &f.Index)
	return &f
}

// buildDownloadURLs populates DownloadURLs from Files + UUID + UserID.
// No-op when Status != StatusComplete (in-progress exports have no files yet).
// URL formula confirmed via capture 2026-05-02:
//
//	https://takeout.google.com/takeout/download?j={UUID}&i={INDEX}&user={USER_ID}
//
// rapt parameter is optional (only required for sensitive operations); regular
// cookie-authenticated requests work without it.
func (e *Export) buildDownloadURLs() {
	if e.Status != StatusComplete || e.UserID == "" {
		return
	}
	urls := make([]string, 0, len(e.Files))
	for _, f := range e.Files {
		urls = append(urls, fmt.Sprintf(
			"https://takeout.google.com/takeout/download?j=%s&i=%d&user=%s",
			e.UUID, f.Index, e.UserID,
		))
	}
	e.DownloadURLs = urls
}

// mapStatusCode maps Google's integer status codes to our enum.
// Confirmed values:
//
//	0   = in_progress (per 2026-05-02 spike)
//	100 = complete    (per 2026-05-02 completed-export capture)
//
// Failed/expired codes are still TBD until we observe them.
func mapStatusCode(code int) ExportStatus {
	switch code {
	case 0:
		return StatusInProgress
	case 100:
		return StatusComplete
	default:
		return StatusUnknown
	}
}
