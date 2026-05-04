package takeoutapi

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
)

// ListExports returns all exports visible on the user's manage-exports page.
// Includes in-progress, complete, and recently-expired exports.
func (c *Client) ListExports(ctx context.Context) ([]*Export, error) {
	raw, err := c.CallRPC(ctx, "fhjYTc", "[]", "generic")
	if err != nil {
		return nil, fmt.Errorf("listing exports: %w", err)
	}
	return parseExportListResponse(raw)
}

// GetExport fetches a single export by UUID. Returns nil + nil error if not found.
func (c *Client) GetExport(ctx context.Context, uuid string) (*Export, error) {
	exports, err := c.ListExports(ctx)
	if err != nil {
		return nil, err
	}
	for _, e := range exports {
		if e.UUID == uuid {
			return e, nil
		}
	}
	return nil, nil
}

// CreateExport submits a new Takeout export request. Returns the (best-effort)
// parsed response — may include the new export's UUID once we know the exact
// U5lrKc response shape (see docs/spikes/2026-05-02-batchexecute-api.md).
func (c *Client) CreateExport(ctx context.Context, opts CreateExportOptions) (*Export, error) {
	args, err := buildCreateExportArgs(opts)
	if err != nil {
		return nil, fmt.Errorf("building create-export args: %w", err)
	}

	raw, err := c.CallRPC(ctx, "U5lrKc", string(args), "generic")
	if err != nil {
		return nil, fmt.Errorf("creating export: %w", err)
	}

	// Best-effort: try to extract a UUID from the response. If the shape is
	// different than expected, return a minimal Export{} so the caller can
	// fall back to ListExports to find the new one by recency.
	exp := &Export{Status: StatusInProgress}
	var anyResp interface{}
	if err := json.Unmarshal(raw, &anyResp); err == nil {
		if uuid := scrapeUUID(anyResp); uuid != "" {
			exp.UUID = uuid
		}
	}
	return exp, nil
}

// buildCreateExportArgs constructs the args payload for U5lrKc.
//
// Captured 2026-05-03 from a real browser create:
//
//	["ac.t.st", [[["drive"]], "ZIP", null, 5, null, 2147483648, 1, null, null, null, "0"]]
//
// The inner positional args are nested inside their own array — NOT flattened
// alongside the "ac.t.st" action name. Sending them flat produces error code
// 3 (INVALID_ARGUMENT). See docs/spikes/2026-05-03-u5lrkc-debug-state.md.
func buildCreateExportArgs(opts CreateExportOptions) ([]byte, error) {
	products := make([][]string, len(opts.Products))
	for i, p := range opts.Products {
		products[i] = []string{p}
	}

	freqCode, ok := frequencyCodes[opts.Frequency]
	if !ok && opts.Frequency != "" {
		return nil, fmt.Errorf("unknown frequency %q (use once|every_2_months)", opts.Frequency)
	}
	if opts.Frequency == "" {
		freqCode = frequencyCodes["once"]
	}

	format := opts.Format
	if format == "" {
		format = "ZIP"
	}

	size := opts.SizeBytes
	if size == 0 {
		size = 2 * 1024 * 1024 * 1024 // default 2 GB
	}

	inner := []interface{}{
		products,
		format,
		nil,
		freqCode,
		nil,
		size,
		1,
		nil, nil, nil,
		"0",
	}
	args := []interface{}{"ac.t.st", inner}
	return json.Marshal(args)
}

// frequencyCodes maps human-friendly names to the integer codes Google uses.
// Values are best-guess from a single capture; adjust when we capture an
// every-2-months variant for comparison.
var frequencyCodes = map[string]int{
	"once":           5,
	"every_2_months": 6, // GUESS — confirm with capture
}

// scrapeUUID walks an arbitrary parsed-JSON tree looking for a UUID-shaped
// string (8-4-4-4-12 hex). Useful when we don't know the exact response shape.
func scrapeUUID(v interface{}) string {
	switch t := v.(type) {
	case string:
		if isUUID(t) {
			return t
		}
	case []interface{}:
		for _, item := range t {
			if u := scrapeUUID(item); u != "" {
				return u
			}
		}
	case map[string]interface{}:
		for _, item := range t {
			if u := scrapeUUID(item); u != "" {
				return u
			}
		}
	}
	return ""
}

var uuidRE = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

func isUUID(s string) bool {
	return uuidRE.MatchString(s)
}
