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

func TestFilterCookies(t *testing.T) {
	in := []*http.Cookie{
		// Duplicate SID — generic domain should win.
		{Name: "SID", Value: "from-accounts", Domain: "accounts.google.com"},
		{Name: "SID", Value: "from-google", Domain: ".google.com"},

		// Duplicate OSID — same scoring, last write wins.
		{Name: "OSID", Value: "first", Domain: "myaccount.google.com"},
		{Name: "OSID", Value: "second", Domain: "myaccount.google.com"},

		// Login-only cookies — should be dropped.
		{Name: "LSID", Value: "loginsid", Domain: "accounts.google.com"},
		{Name: "ACCOUNT_CHOOSER", Value: "ac", Domain: "accounts.google.com"},
		{Name: "SMSV", Value: "smsv", Domain: "accounts.google.com"},

		// __Host-* cookies — should be dropped.
		{Name: "__Host-1PLSID", Value: "host", Domain: "accounts.google.com"},
		{Name: "__Host-GAPS", Value: "gaps", Domain: "accounts.google.com"},

		// Normal generic cookie — should be kept.
		{Name: "NID", Value: "nid", Domain: ".google.com"},
	}

	out := filterCookies(in)

	byName := map[string]*http.Cookie{}
	for _, ck := range out {
		byName[ck.Name] = ck
	}

	// SID should be deduped to the .google.com one.
	if sid, ok := byName["SID"]; !ok || sid.Value != "from-google" {
		t.Errorf("SID not deduped to .google.com version: got %+v", sid)
	}

	// NID should be kept.
	if _, ok := byName["NID"]; !ok {
		t.Error("NID missing")
	}

	// OSID should be present (one of them).
	if _, ok := byName["OSID"]; !ok {
		t.Error("OSID missing")
	}

	// Login-only and __Host- cookies should be dropped.
	for _, name := range []string{"LSID", "ACCOUNT_CHOOSER", "SMSV", "__Host-1PLSID", "__Host-GAPS"} {
		if _, ok := byName[name]; ok {
			t.Errorf("%s should have been filtered out", name)
		}
	}
}
