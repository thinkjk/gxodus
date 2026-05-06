// Package statusserver hosts a tiny read-only HTTP page summarizing
// per-account state — exists so users on a NAS can glance at "is the
// box healthy?" in a browser without ssh-ing in.
package statusserver

import (
	"fmt"
	"html/template"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/thinkjk/gxodus/internal/accounts"
	"github.com/thinkjk/gxodus/internal/auth"
	"github.com/thinkjk/gxodus/internal/config"
)

// startedAt is set when ListenAndServe is called so the page can show uptime.
var startedAt = time.Now()

// fileInfo is a serialized export-file row.
type fileInfo struct {
	Name    string
	Size    string
	ModTime string
}

// accountInfo is the per-account block on the page.
type accountInfo struct {
	Email      string
	HasSession bool
	Pending    string
	Files      []fileInfo
	OutputDir  string
}

// pageData drives the template.
type pageData struct {
	Accounts []accountInfo
	Hostname string
	Uptime   string
	Interval string
	Now      string
	NovncURL string // e.g. http://<host>:6080/vnc.html — derived from request Host header
}

const pageTemplate = `<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta http-equiv="refresh" content="30">
<title>gxodus status — {{.Hostname}}</title>
<style>
  body { font-family: -apple-system, system-ui, sans-serif; max-width: 980px; margin: 2em auto; padding: 0 1em; color: #222; }
  h1 { margin-bottom: 0.2em; }
  .meta { color: #666; font-size: 0.9em; margin-bottom: 2em; }
  .tools { margin-bottom: 2em; padding: 0.5em 0.8em; background: #eef3fa; border-radius: 6px; font-size: 0.9em; }
  .tools a { color: #1e3a73; font-weight: 600; text-decoration: none; }
  .tools a:hover { text-decoration: underline; }
  .account { border: 1px solid #ddd; border-radius: 6px; padding: 1em 1.4em; margin: 1em 0; background: #fafafa; }
  .account h2 { margin: 0 0 0.4em 0; font-size: 1.1em; }
  .badge { display: inline-block; padding: 2px 8px; border-radius: 10px; font-size: 0.8em; font-weight: 600; }
  .badge-ok { background: #dff5e3; color: #1f6b2a; }
  .badge-warn { background: #fde8d8; color: #8a3e0e; }
  .badge-info { background: #e0e8f5; color: #1e3a73; }
  .files { margin-top: 0.6em; }
  .files table { border-collapse: collapse; width: 100%; font-size: 0.9em; }
  .files th, .files td { text-align: left; padding: 4px 8px; border-bottom: 1px solid #eee; }
  .files th { color: #666; font-weight: 500; }
  .files .size { text-align: right; font-variant-numeric: tabular-nums; }
  .empty { color: #999; font-style: italic; padding: 4px 0; }
  footer { margin-top: 3em; color: #888; font-size: 0.8em; }
</style>
</head>
<body>
<h1>gxodus status</h1>
<div class="meta">{{.Hostname}} · uptime {{.Uptime}} · interval {{.Interval}} · {{.Now}}</div>
<div class="tools"><a href="{{.NovncURL}}" target="_blank" rel="noopener">Open noVNC ↗</a> — for re-auth via the chromium UI</div>

{{if .Accounts}}
{{range .Accounts}}
<div class="account">
  <h2>{{.Email}}
    {{if .HasSession}}<span class="badge badge-ok">session ✓</span>
    {{else}}<span class="badge badge-warn">no session — run gxodus auth --account {{.Email}}</span>{{end}}
    {{if .Pending}}<span class="badge badge-info">pending {{.Pending}}</span>{{end}}
  </h2>
  <div class="files">
    {{if .Files}}
    <table>
      <tr><th>file</th><th class="size">size</th><th>modified</th></tr>
      {{range .Files}}
      <tr><td>{{.Name}}</td><td class="size">{{.Size}}</td><td>{{.ModTime}}</td></tr>
      {{end}}
    </table>
    {{else}}
    <div class="empty">no files in {{.OutputDir}}</div>
    {{end}}
  </div>
</div>
{{end}}
{{else}}
<p>No accounts configured. Run <code>gxodus auth</code> to add one.</p>
{{end}}

<footer>read-only status page · refreshes every 30s · auto-rendered from filesystem state (no API calls)</footer>
</body>
</html>
`

var tmpl = template.Must(template.New("status").Parse(pageTemplate))

// ListenAndServe blocks serving the status page on addr.
func ListenAndServe(addr, outputDir string) error {
	startedAt = time.Now()
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}
		renderPage(w, r, outputDir)
	})
	log.Printf("[statusserver] listening on %s\n", addr)
	return http.ListenAndServe(addr, mux)
}

func renderPage(w http.ResponseWriter, r *http.Request, outputDir string) {
	data := pageData{
		Hostname: hostname(),
		Uptime:   time.Since(startedAt).Round(time.Second).String(),
		Interval: envOr("GXODUS_INTERVAL", "(not set / one-shot)"),
		Now:      time.Now().Format("2006-01-02 15:04:05 MST"),
		NovncURL: novncURL(r.Host),
	}

	all, err := accounts.ScanAccounts()
	if err != nil {
		http.Error(w, "scanning accounts: "+err.Error(), http.StatusInternalServerError)
		return
	}

	for _, a := range all {
		info := accountInfo{
			Email:      a.Email,
			HasSession: a.HasSession && auth.SessionExists(a.Dir),
			OutputDir:  filepath.Join(outputDir, a.Email),
		}
		// Read pending UUID (best-effort).
		if data, err := os.ReadFile(filepath.Join(a.Dir, "pending_export.uuid")); err == nil {
			info.Pending = string(stripTrailingNewlines(data))
		}
		info.Files = listFiles(info.OutputDir)
		data.Accounts = append(data.Accounts, info)
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := tmpl.Execute(w, data); err != nil {
		log.Printf("[statusserver] render error: %v\n", err)
	}
}

func listFiles(dir string) []fileInfo {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	type rawFile struct {
		name  string
		size  int64
		mtime time.Time
	}
	raw := make([]rawFile, 0, len(entries))
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		st, err := e.Info()
		if err != nil {
			continue
		}
		raw = append(raw, rawFile{name: e.Name(), size: st.Size(), mtime: st.ModTime()})
	}
	sort.Slice(raw, func(i, j int) bool {
		// newest first
		return raw[i].mtime.After(raw[j].mtime)
	})
	out := make([]fileInfo, 0, len(raw))
	for _, f := range raw {
		out = append(out, fileInfo{
			Name:    f.name,
			Size:    formatBytes(f.size),
			ModTime: f.mtime.Format("2006-01-02 15:04"),
		})
	}
	return out
}

func formatBytes(b int64) string {
	const (
		kb = 1024
		mb = 1024 * kb
		gb = 1024 * mb
	)
	switch {
	case b >= gb:
		return fmt.Sprintf("%.2f GB", float64(b)/float64(gb))
	case b >= mb:
		return fmt.Sprintf("%.2f MB", float64(b)/float64(mb))
	case b >= kb:
		return fmt.Sprintf("%.2f KB", float64(b)/float64(kb))
	default:
		return fmt.Sprintf("%d B", b)
	}
}

func hostname() string {
	host := os.Getenv("GXODUS_PUBLIC_HOSTNAME")
	if host == "" {
		if h, err := os.Hostname(); err == nil {
			host = h
		} else {
			host = "(unknown)"
		}
	}
	return host
}

func envOr(k, fallback string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return fallback
}

// novncURL builds a noVNC URL using the same hostname the status page
// was reached at + the noVNC port (default 6080, override via
// GXODUS_NOVNC_PORT). reqHost is the request's Host header
// (host[:port]) — strip the port and use the noVNC port instead.
func novncURL(reqHost string) string {
	port := os.Getenv("GXODUS_NOVNC_PORT")
	if port == "" {
		port = "6080"
	}
	host := reqHost
	if i := strings.LastIndex(host, ":"); i >= 0 {
		host = host[:i]
	}
	if host == "" {
		host = "localhost"
	}
	return fmt.Sprintf("http://%s:%s/vnc.html", host, port)
}

func stripTrailingNewlines(b []byte) []byte {
	for len(b) > 0 && (b[len(b)-1] == '\n' || b[len(b)-1] == '\r') {
		b = b[:len(b)-1]
	}
	return b
}

// Compile-time check that we use config (placeholder until ResolveOutputDir is needed).
var _ = config.ConfigDir
