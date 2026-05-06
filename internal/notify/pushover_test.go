package notify

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/thinkjk/gxodus/internal/config"
)

func TestSendPushover_PostsExpectedForm(t *testing.T) {
	var captured url.Values
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" {
			t.Errorf("method = %s, want POST", r.Method)
		}
		if ct := r.Header.Get("Content-Type"); !strings.Contains(ct, "application/x-www-form-urlencoded") {
			t.Errorf("Content-Type = %q", ct)
		}
		_ = r.ParseForm()
		captured = r.PostForm
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"status":1,"request":"abc"}`))
	}))
	defer srv.Close()

	cfg := config.PushoverConfig{Token: "tk", UserKey: "uk"}
	err := sendPushoverTo(srv.URL, cfg, "Hello", "World")
	if err != nil {
		t.Fatalf("sendPushoverTo: %v", err)
	}
	if got := captured.Get("token"); got != "tk" {
		t.Errorf("token = %q", got)
	}
	if got := captured.Get("user"); got != "uk" {
		t.Errorf("user = %q", got)
	}
	if got := captured.Get("title"); got != "Hello" {
		t.Errorf("title = %q", got)
	}
	if got := captured.Get("message"); got != "World" {
		t.Errorf("message = %q", got)
	}
}

func TestSendPushover_ErrorOnNon2xx(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"status":0,"errors":["invalid token"]}`))
	}))
	defer srv.Close()

	err := sendPushoverTo(srv.URL, config.PushoverConfig{Token: "tk", UserKey: "uk"}, "T", "M")
	if err == nil {
		t.Fatal("expected error for non-2xx response, got nil")
	}
	if !strings.Contains(err.Error(), "400") {
		t.Errorf("error should mention status 400: %v", err)
	}
}

func TestFire_FiresPushoverForListedEvents(t *testing.T) {
	var hits int32
	var mu sync.Mutex
	captured := map[string]url.Values{}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		mu.Lock()
		captured[r.PostForm.Get("title")] = r.PostForm
		mu.Unlock()
		atomic.AddInt32(&hits, 1)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	pushoverEndpointOverride = srv.URL
	defer func() { pushoverEndpointOverride = "" }()

	cfg := config.NotifyConfig{
		Pushover: config.PushoverConfig{
			Token:   "tk",
			UserKey: "uk",
			Events:  []string{"auth_expired", "error"},
		},
	}

	// auth_expired is in events list — should fire.
	Fire(cfg, "auth_expired", EventData{})
	// export_started is NOT in events list — should be skipped.
	Fire(cfg, "export_started", EventData{})
	// error is in events list — should fire.
	Fire(cfg, "error", EventData{Error: "boom"})

	// Fire spawns goroutines; give them a tick.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) && atomic.LoadInt32(&hits) < 2 {
		time.Sleep(10 * time.Millisecond)
	}
	if got := atomic.LoadInt32(&hits); got != 2 {
		t.Errorf("Pushover hits = %d, want 2", got)
	}
	mu.Lock()
	if _, ok := captured["gxodus: re-auth needed"]; !ok {
		t.Errorf("missing auth_expired notification; captured titles: %v", keysOf(captured))
	}
	if v := captured["gxodus: error"]; v.Get("message") != "boom" {
		t.Errorf("error message = %q, want boom", v.Get("message"))
	}
	mu.Unlock()
}

func keysOf(m map[string]url.Values) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

func TestFire_PushoverTitleIncludesAccount(t *testing.T) {
	var captured url.Values
	var mu sync.Mutex
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		mu.Lock()
		captured = r.PostForm
		mu.Unlock()
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	pushoverEndpointOverride = srv.URL
	defer func() { pushoverEndpointOverride = "" }()

	cfg := config.NotifyConfig{
		Pushover: config.PushoverConfig{
			Token:   "tk",
			UserKey: "uk",
			Events:  []string{"auth_expired"},
		},
	}
	Fire(cfg, "auth_expired", EventData{Account: "jason@example.com"})

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		mu.Lock()
		ok := captured.Get("title") != ""
		mu.Unlock()
		if ok {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	mu.Lock()
	defer mu.Unlock()
	got := captured.Get("title")
	if got != "gxodus: re-auth needed [jason@example.com]" {
		t.Errorf("title = %q, want includes [jason@example.com]", got)
	}
}
