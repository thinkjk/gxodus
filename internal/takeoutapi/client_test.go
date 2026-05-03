package takeoutapi

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
)

func TestClient_CallRPC(t *testing.T) {
	var capturedURL, capturedBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Two paths: GET / for the HTML (XSRF bootstrap), POST /_/TakeoutUi/data/batchexecute for the call.
		if r.Method == "GET" {
			_, _ = w.Write([]byte(`<html><script>WIZ_global_data = {"SNlM0e":"TEST_AT","cfb2h":"TEST_BL"};</script></html>`))
			return
		}
		capturedURL = r.URL.String()
		bodyBytes, _ := io.ReadAll(r.Body)
		capturedBody = string(bodyBytes)

		// Build a chunk with dynamic length so it always matches.
		chunk := `[["wrb.fr","X1Y2Z3","42",null,null,null,"generic"]]`
		resp := ")]}'\n" + strconv.Itoa(len(chunk)) + "\n" + chunk
		_, _ = w.Write([]byte(resp))
	}))
	defer srv.Close()

	c := newClientForTest(srv.URL, []*http.Cookie{{Name: "SID", Value: "fake"}})
	raw, err := c.CallRPC(context.Background(), "X1Y2Z3", "[]", "generic")
	if err != nil {
		t.Fatalf("CallRPC: %v", err)
	}

	if string(raw) != "42" {
		t.Errorf("raw = %q, want %q", raw, "42")
	}

	// Verify we sent the right URL params and body.
	if !strings.Contains(capturedURL, "rpcids=X1Y2Z3") {
		t.Errorf("URL missing rpcid: %s", capturedURL)
	}
	if !strings.Contains(capturedURL, "bl=TEST_BL") {
		t.Errorf("URL missing build label: %s", capturedURL)
	}
	if !strings.Contains(capturedBody, "at=TEST_AT") {
		t.Errorf("body missing XSRF token: %s", capturedBody)
	}
	if !strings.Contains(capturedBody, "X1Y2Z3") {
		t.Errorf("body missing rpcid: %s", capturedBody)
	}
}
