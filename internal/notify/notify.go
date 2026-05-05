package notify

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"text/template"
	"time"

	"github.com/thinkjk/gxodus/internal/config"
)

type EventData struct {
	Error      string
	OutputPath string
	ExportSize int64
	Duration   time.Duration
}

// Fire executes the notification hook for the given event.
// Runs the configured shell command (if any) AND fires Pushover (if
// configured and the event is in cfg.Pushover.Events). Both are
// non-blocking; errors are logged but never propagated.
func Fire(cfg config.NotifyConfig, event string, data EventData) {
	fireShellHook(cfg, event, data)
	firePushover(cfg, event, data)
}

func fireShellHook(cfg config.NotifyConfig, event string, data EventData) {
	var cmdTemplate string

	switch event {
	case "auth_expired":
		cmdTemplate = cfg.OnAuthExpired
	case "export_started":
		cmdTemplate = cfg.OnExportStarted
	case "export_complete":
		cmdTemplate = cfg.OnExportComplete
	case "error":
		cmdTemplate = cfg.OnError
	default:
		return
	}

	if cmdTemplate == "" {
		return
	}

	rendered, err := renderTemplate(cmdTemplate, data)
	if err != nil {
		fmt.Printf("Warning: failed to render notification template for %s: %v\n", event, err)
		return
	}

	go func() {
		cmd := exec.Command("sh", "-c", rendered)
		output, err := cmd.CombinedOutput()
		if err != nil {
			fmt.Printf("Warning: notification hook '%s' failed: %v\nOutput: %s\n", event, err, string(output))
		}
	}()
}

func firePushover(cfg config.NotifyConfig, event string, data EventData) {
	if cfg.Pushover.Token == "" || cfg.Pushover.UserKey == "" {
		return
	}
	enabled := false
	for _, e := range cfg.Pushover.Events {
		if e == event {
			enabled = true
			break
		}
	}
	if !enabled {
		return
	}

	title, message := pushoverMessageFor(event, data)
	endpoint := pushoverAPIURL
	if pushoverEndpointOverride != "" {
		endpoint = pushoverEndpointOverride
	}

	go func() {
		if err := sendPushoverTo(endpoint, cfg.Pushover, title, message); err != nil {
			fmt.Printf("Warning: pushover for '%s' failed: %v\n", event, err)
		}
	}()
}

func pushoverMessageFor(event string, data EventData) (title, message string) {
	host := os.Getenv("GXODUS_PUBLIC_HOSTNAME")
	if host == "" {
		if h, err := os.Hostname(); err == nil {
			host = h
		} else {
			host = "the gxodus container"
		}
	}
	switch event {
	case "auth_expired":
		return "gxodus: re-auth needed",
			fmt.Sprintf("Open noVNC at %s:6080/vnc.html and complete the password challenge.", host)
	case "export_complete":
		return "gxodus: export ready",
			fmt.Sprintf("Downloaded %d bytes to %s.", data.ExportSize, data.OutputPath)
	case "error":
		return "gxodus: error", data.Error
	case "export_started":
		return "gxodus: export started", "New Takeout submitted."
	}
	return "gxodus", event
}

func renderTemplate(tmpl string, data EventData) (string, error) {
	t, err := template.New("notify").Parse(tmpl)
	if err != nil {
		return "", fmt.Errorf("parsing template: %w", err)
	}

	var buf bytes.Buffer
	if err := t.Execute(&buf, data); err != nil {
		return "", fmt.Errorf("executing template: %w", err)
	}

	return buf.String(), nil
}
