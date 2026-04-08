package notify

import (
	"bytes"
	"fmt"
	"os/exec"
	"text/template"
	"time"

	"github.com/jason/gxodus/internal/config"
)

type EventData struct {
	Error      string
	OutputPath string
	ExportSize int64
	Duration   time.Duration
}

// Fire executes the notification hook for the given event.
// It runs the configured shell command with template variables substituted.
// Execution is non-blocking and errors are logged but not propagated.
func Fire(cfg config.NotifyConfig, event string, data EventData) {
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
