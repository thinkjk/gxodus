package takeoutapi

import (
	"context"
	"fmt"
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
