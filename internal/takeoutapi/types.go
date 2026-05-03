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

// Export is a parsed Takeout export from the fhjYTc list response.
type Export struct {
	UUID         string
	CreatedAt    time.Time
	Status       ExportStatus
	DownloadURLs []string // populated when Status == StatusComplete
}

// CreateExportOptions configures a new export request (sent to U5lrKc).
type CreateExportOptions struct {
	Products  []string // slugs like "drive", "gmail", "bond" (Access Log Activity)
	Format    string   // "ZIP" or "TGZ"
	SizeBytes int64    // archive split size, e.g. 2*1024*1024*1024 for 2 GB
	Frequency string   // "once" | "every_2_months" — translated to internal codes
}

// parseExportListResponse extracts Export structs from an fhjYTc response payload.
// The shape (positional indexes) was reverse-engineered in the 2026-05-02 spike.
// See docs/spikes/2026-05-02-batchexecute-api.md.
func parseExportListResponse(raw json.RawMessage) ([]*Export, error) {
	// Top-level shape: [null, [[null, [<export-fields>]]], null, "<userId>", false, [<duplicate>]]
	var top []json.RawMessage
	if err := json.Unmarshal(raw, &top); err != nil {
		return nil, fmt.Errorf("unmarshal top: %w", err)
	}
	if len(top) < 2 {
		return nil, fmt.Errorf("response too short: %d top-level elements", len(top))
	}

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
		exports = append(exports, exp)
	}
	return exports, nil
}

// parseExportFields parses one export's positional-indexed fields.
// Positions per the 2026-05-02 spike:
//   [1] = UUID, [9] = status code, [22] = creation timestamp ms.
// Download URLs: TBD — currently always returns empty slice. Once we capture
// a completed-export fhjYTc response, update this to extract from the right
// position.
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

	var statusCode int
	if err := json.Unmarshal(fields[9], &statusCode); err == nil {
		exp.Status = mapStatusCode(statusCode)
	}

	var createdMs int64
	if err := json.Unmarshal(fields[22], &createdMs); err == nil {
		exp.CreatedAt = time.UnixMilli(createdMs)
	}

	return exp, nil
}

// mapStatusCode is best-guess pending discovery of completed-export status code.
// Position [9] = 0 confirmed for in-progress (per 2026-05-02 spike). Other
// values inferred until proven by Task 6 captures of completed/failed exports.
func mapStatusCode(code int) ExportStatus {
	switch code {
	case 0:
		return StatusInProgress
	case 1:
		return StatusComplete
	case 2:
		return StatusFailed
	case 3:
		return StatusExpired
	default:
		return StatusUnknown
	}
}
