package takeoutapi

import (
	"encoding/json"
	"testing"
)

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

func TestParseExportListResponse_InProgress(t *testing.T) {
	// Synthetic in-progress export: status code 0, no files at [8], no [23-27].
	raw := json.RawMessage(`[null,[[null,["ac.t.ta","aaaaaaaa-1111-2222-3333-444444444444","May 2, 2026",null,"",null,123456,[],[],0,null,false,null,null,["May 2, 2026","5:27 PM","1.2.3.4"],null,null,null,5,null,null,false,1777768027572,null,0,null,1,null,[null,0,true],true]]],null,"my-user-id-123",false,[]]`)

	exports, err := parseExportListResponse(raw)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(exports) != 1 {
		t.Fatalf("got %d exports, want 1", len(exports))
	}

	e := exports[0]
	if e.UUID != "aaaaaaaa-1111-2222-3333-444444444444" {
		t.Errorf("UUID = %q", e.UUID)
	}
	if e.UserID != "my-user-id-123" {
		t.Errorf("UserID = %q", e.UserID)
	}
	if e.Status != StatusInProgress {
		t.Errorf("Status = %v, want StatusInProgress", e.Status)
	}
	if len(e.Files) != 0 {
		t.Errorf("expected no files for in-progress; got %d", len(e.Files))
	}
	if len(e.DownloadURLs) != 0 {
		t.Errorf("expected no download URLs for in-progress; got %d", len(e.DownloadURLs))
	}
	if !e.CompletedAt.IsZero() {
		t.Errorf("CompletedAt should be zero for in-progress")
	}
}

func TestParseExportListResponse_Complete(t *testing.T) {
	// Synthetic completed export based on the 2026-05-02 real capture:
	//   - top[0] is the wrapper (NOT top[1] like in-progress responses)
	//   - status code 100 at fields[9]
	//   - 2 files at fields[8]
	//   - completion + expiration timestamps at fields[23], fields[24]
	//   - manifest file at fields[27]
	raw := json.RawMessage(`[[[null,["ac.t.ta","c250266d-f25e-45d1-a3e1-73b83441cc67","May 1, 2026","May 2, 2026","May 9, 2026",null,565301917378,[],[["takeout-x-001.zip",100,0,null,null,5,null,"",0],["takeout-x-002.zip",200,0,null,null,5,null,"",1]],100,null,false,null,null,["May 1, 2026","11:23 PM","1.2.3.4"],null,null,null,5,null,null,false,1777703002265,1777729531134,1778334331134,null,1,["takeout-manifest-001.zip",50,0,null,null,5,null,"",16],[null,0,true],false]]],null,null,"109418410415921684377",false,[]]`)

	exports, err := parseExportListResponse(raw)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(exports) != 1 {
		t.Fatalf("got %d exports, want 1", len(exports))
	}

	e := exports[0]
	if e.UUID != "c250266d-f25e-45d1-a3e1-73b83441cc67" {
		t.Errorf("UUID = %q", e.UUID)
	}
	if e.UserID != "109418410415921684377" {
		t.Errorf("UserID = %q", e.UserID)
	}
	if e.Status != StatusComplete {
		t.Errorf("Status = %v, want StatusComplete", e.Status)
	}
	if e.TotalBytes != 565301917378 {
		t.Errorf("TotalBytes = %d", e.TotalBytes)
	}
	if e.CompletedAt.IsZero() {
		t.Errorf("CompletedAt should be set")
	}
	if e.ExpiresAt.IsZero() {
		t.Errorf("ExpiresAt should be set")
	}

	// 2 files at [8] + 1 manifest at [27] = 3
	if len(e.Files) != 3 {
		t.Fatalf("Files count = %d, want 3", len(e.Files))
	}
	if e.Files[0].Filename != "takeout-x-001.zip" || e.Files[0].Index != 0 {
		t.Errorf("Files[0] = %+v", e.Files[0])
	}
	if e.Files[2].Filename != "takeout-manifest-001.zip" || e.Files[2].Index != 16 {
		t.Errorf("Files[2] (manifest) = %+v", e.Files[2])
	}

	if len(e.DownloadURLs) != 3 {
		t.Fatalf("DownloadURLs count = %d, want 3", len(e.DownloadURLs))
	}
	expected := "https://takeout.google.com/takeout/download?j=c250266d-f25e-45d1-a3e1-73b83441cc67&i=0&user=109418410415921684377"
	if e.DownloadURLs[0] != expected {
		t.Errorf("DownloadURLs[0] =\n got: %s\nwant: %s", e.DownloadURLs[0], expected)
	}
	expectedManifest := "https://takeout.google.com/takeout/download?j=c250266d-f25e-45d1-a3e1-73b83441cc67&i=16&user=109418410415921684377"
	if e.DownloadURLs[2] != expectedManifest {
		t.Errorf("DownloadURLs[2] (manifest) =\n got: %s\nwant: %s", e.DownloadURLs[2], expectedManifest)
	}
}

func TestParseExportListResponse_Mixed(t *testing.T) {
	// Real-world shape captured 2026-05-04: completed export wrapper at top[0]
	// AND in-progress export wrapper at top[1]. Earlier parser short-circuited
	// on top[0] and silently dropped the in-progress one — caller's UUID
	// lookup then returned nil and the poller stayed in_progress forever.
	completedFields := `["ac.t.ta","comp1111-1111-1111-1111-111111111111","May 1, 2026","May 2, 2026","May 9, 2026",null,500,[],[["c-001.zip",100,0,null,null,5,null,"",0]],100,null,false,null,null,["May 1, 2026","11:23 PM","1.2.3.4"],null,null,null,5,null,null,false,1777703002265,1777729531134,1778334331134,null,1,null,[null,0,true],false]`
	inProgressFields := `["ac.t.ta","prog2222-2222-2222-2222-222222222222","May 4, 2026",null,"",null,123,[],[],0,null,false,null,null,["May 4, 2026","2:00 PM","1.2.3.4"],null,null,null,5,null,null,false,1777768027572,null,0,null,1,null,[null,0,true],true]`

	raw := json.RawMessage(`[[[null,` + completedFields + `]],[[null,` + inProgressFields + `]],null,"my-user",false,[]]`)

	exports, err := parseExportListResponse(raw)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(exports) != 2 {
		t.Fatalf("got %d exports, want 2", len(exports))
	}

	byUUID := map[string]*Export{}
	for _, e := range exports {
		byUUID[e.UUID] = e
	}
	completed, ok := byUUID["comp1111-1111-1111-1111-111111111111"]
	if !ok {
		t.Fatal("completed export missing from result")
	}
	if completed.Status != StatusComplete {
		t.Errorf("completed.Status = %v, want StatusComplete", completed.Status)
	}
	inProgress, ok := byUUID["prog2222-2222-2222-2222-222222222222"]
	if !ok {
		t.Fatal("in-progress export missing from result — this is the regression")
	}
	if inProgress.Status != StatusInProgress {
		t.Errorf("inProgress.Status = %v, want StatusInProgress", inProgress.Status)
	}
}

func TestMapStatusCode(t *testing.T) {
	cases := map[int]ExportStatus{
		0:   StatusInProgress,
		100: StatusComplete,
		999: StatusUnknown, // unknown values fall through
	}
	for code, want := range cases {
		if got := mapStatusCode(code); got != want {
			t.Errorf("mapStatusCode(%d) = %v, want %v", code, got, want)
		}
	}
}
