package takeoutapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
)

func TestClient_ListExports(t *testing.T) {
	// fhjYTc-shaped inner result with one in-progress export.
	innerResult := `[null,[[null,["ac.t.ta","abc12345-6789-4def-9012-3456789abcde","May 2, 2026",null,"",null,0,[],null,0,null,false,null,null,["May 2, 2026","5:27 PM","104.2.75.91"],null,null,null,5,null,null,false,1777768027572,null,0,null,1,null,[null,0,true],true]]],null,"114106906800892523426",false,[]]`

	// Wrap as a wrb.fr chunk. Use json.Marshal to encode innerResult AS A
	// STRING (doubly-escaped), then build the chunk envelope manually.
	innerAsJSONString, _ := json.Marshal(innerResult)
	chunk := `[["wrb.fr","fhjYTc",` + string(innerAsJSONString) + `,null,null,null,"generic"]]`

	envelope := []byte(")]}'\n" + strconv.Itoa(len(chunk)) + "\n" + chunk)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "GET" {
			_, _ = w.Write([]byte(`<html><script>WIZ_global_data = {"SNlM0e":"AT","cfb2h":"BL"};</script></html>`))
			return
		}
		_, _ = w.Write(envelope)
	}))
	defer srv.Close()

	c := newClientForTest(srv.URL, []*http.Cookie{{Name: "SID", Value: "fake"}})
	exports, err := c.ListExports(context.Background())
	if err != nil {
		t.Fatalf("ListExports: %v", err)
	}
	if len(exports) != 1 {
		t.Fatalf("expected 1 export, got %d", len(exports))
	}
	if exports[0].UUID != "abc12345-6789-4def-9012-3456789abcde" {
		t.Errorf("UUID = %q", exports[0].UUID)
	}
	if exports[0].Status != StatusInProgress {
		t.Errorf("Status = %v", exports[0].Status)
	}
}

func TestClient_GetExport(t *testing.T) {
	innerResult := `[null,[[null,["ac.t.ta","target-uuid-here-1234-567890abcdef","May 2, 2026",null,"",null,0,[],null,0,null,false,null,null,["May 2, 2026","5:27 PM","104.2.75.91"],null,null,null,5,null,null,false,1777768027572,null,0,null,1,null,[null,0,true],true]]],null,"114106906800892523426",false,[]]`

	innerAsJSONString, _ := json.Marshal(innerResult)
	chunk := `[["wrb.fr","fhjYTc",` + string(innerAsJSONString) + `,null,null,null,"generic"]]`
	envelope := []byte(")]}'\n" + strconv.Itoa(len(chunk)) + "\n" + chunk)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "GET" {
			_, _ = w.Write([]byte(`<html><script>WIZ_global_data = {"SNlM0e":"AT","cfb2h":"BL"};</script></html>`))
			return
		}
		_, _ = w.Write(envelope)
	}))
	defer srv.Close()

	c := newClientForTest(srv.URL, []*http.Cookie{{Name: "SID", Value: "fake"}})

	// Found case
	got, err := c.GetExport(context.Background(), "target-uuid-here-1234-567890abcdef")
	if err != nil {
		t.Fatalf("GetExport: %v", err)
	}
	if got == nil {
		t.Fatal("expected non-nil export")
	}
	if got.UUID != "target-uuid-here-1234-567890abcdef" {
		t.Errorf("UUID = %q", got.UUID)
	}

	// Not-found case (different UUID)
	missing, err := c.GetExport(context.Background(), "no-such-uuid")
	if err != nil {
		t.Fatalf("GetExport (not found): %v", err)
	}
	if missing != nil {
		t.Error("expected nil for missing UUID")
	}
}
