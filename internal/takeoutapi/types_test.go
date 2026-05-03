package takeoutapi

import (
	"encoding/json"
	"testing"
)

func TestParseExport_FromMinimalFixture(t *testing.T) {
	// Synthetic fhjYTc-shaped response based on the 2026-05-02 spike capture.
	// Outer shape: [null, [[null, [<export-fields-29-positions>]]], null, "userId", false, ...]
	// Inner export array positional fields:
	//   [0]="ac.t.ta", [1]=UUID, [2]=date-display, ..., [9]=status (0=in_progress),
	//   [22]=createdAt-ms, ...
	raw := json.RawMessage(`[null,[[null,["ac.t.ta","0dc01143-391b-480f-8574-3e40c7c1e43f","May 2, 2026",null,"",null,0,[],null,0,null,false,null,null,["May 2, 2026","5:27 PM","104.2.75.91"],null,null,null,5,null,null,false,1777768027572,null,0,null,1,null,[null,0,true],true]]],null,"114106906800892523426",false,[]]`)

	exports, err := parseExportListResponse(raw)
	if err != nil {
		t.Fatalf("parseExportListResponse: %v", err)
	}

	if len(exports) != 1 {
		t.Fatalf("expected 1 export, got %d", len(exports))
	}

	e := exports[0]
	if e.UUID != "0dc01143-391b-480f-8574-3e40c7c1e43f" {
		t.Errorf("UUID = %q", e.UUID)
	}
	if e.Status != StatusInProgress {
		t.Errorf("Status = %v, want StatusInProgress", e.Status)
	}
	if e.CreatedAt.IsZero() {
		t.Errorf("CreatedAt is zero")
	}
}

func TestExportStatus_String(t *testing.T) {
	tests := map[ExportStatus]string{
		StatusUnknown:    "unknown",
		StatusInProgress: "in_progress",
		StatusComplete:   "complete",
		StatusFailed:     "failed",
		StatusExpired:    "expired",
	}
	for s, want := range tests {
		if got := s.String(); got != want {
			t.Errorf("ExportStatus(%d).String() = %q, want %q", s, got, want)
		}
	}
}
