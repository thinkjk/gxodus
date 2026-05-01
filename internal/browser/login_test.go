package browser

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

func TestFindLoggedInTab(t *testing.T) {
	tests := []struct {
		name string
		tabs []devtoolsTab
		want bool
	}{
		{
			name: "myaccount tab logged in",
			tabs: []devtoolsTab{{Type: "page", URL: "https://myaccount.google.com/"}},
			want: true,
		},
		{
			name: "drive tab logged in",
			tabs: []devtoolsTab{{Type: "page", URL: "https://drive.google.com/drive/my-drive"}},
			want: true,
		},
		{
			name: "still on login page",
			tabs: []devtoolsTab{{Type: "page", URL: "https://accounts.google.com/signin"}},
			want: false,
		},
		{
			name: "non-page targets ignored",
			tabs: []devtoolsTab{{Type: "service_worker", URL: "https://myaccount.google.com/sw.js"}},
			want: false,
		},
		{
			name: "multiple tabs, one logged in",
			tabs: []devtoolsTab{
				{Type: "page", URL: "https://accounts.google.com/signin"},
				{Type: "page", URL: "https://mail.google.com/mail/u/0/#inbox"},
			},
			want: true,
		},
		{
			name: "no tabs",
			tabs: nil,
			want: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := findLoggedInTab(tt.tabs); got != tt.want {
				t.Errorf("findLoggedInTab() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestGetBrowserWebSocketURL(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/json/version" {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write([]byte(`{"webSocketDebuggerUrl": "ws://localhost:9222/devtools/browser/abc-123"}`))
	}))
	defer srv.Close()

	got, err := getBrowserWebSocketURL(srv.URL)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := "ws://localhost:9222/devtools/browser/abc-123"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestGetBrowserWebSocketURLEmpty(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{}`))
	}))
	defer srv.Close()

	if _, err := getBrowserWebSocketURL(srv.URL); err == nil {
		t.Error("expected error when webSocketDebuggerUrl missing")
	}
}

func TestListTabs(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/json/list" {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write([]byte(`[
			{"type": "page", "url": "https://accounts.google.com/signin"},
			{"type": "page", "url": "https://drive.google.com/"}
		]`))
	}))
	defer srv.Close()

	tabs, err := listTabs(srv.URL)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(tabs) != 2 {
		t.Fatalf("expected 2 tabs, got %d", len(tabs))
	}
	if !findLoggedInTab(tabs) {
		t.Error("expected drive tab to register as logged-in")
	}
}

func TestWaitForDevToolsBecomesReady(t *testing.T) {
	var hits int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if atomic.AddInt32(&hits, 1) < 3 {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		_, _ = w.Write([]byte(`{"webSocketDebuggerUrl": "ws://x"}`))
	}))
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := waitForDevTools(ctx, srv.URL, 3*time.Second); err != nil {
		t.Fatalf("expected success, got %v", err)
	}
}

func TestWaitForDevToolsTimeout(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := waitForDevTools(ctx, srv.URL, 500*time.Millisecond); err == nil {
		t.Error("expected timeout error, got nil")
	}
}
