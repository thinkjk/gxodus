package notify

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

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
