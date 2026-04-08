package notify

import (
	"testing"
	"time"
)

func TestRenderTemplate(t *testing.T) {
	data := EventData{
		Error:      "something broke",
		OutputPath: "/exports/2026-04-07",
		ExportSize: 1024 * 1024 * 500,
		Duration:   2 * time.Hour,
	}

	tests := []struct {
		name     string
		template string
		expected string
	}{
		{
			name:     "error template",
			template: "echo 'Error: {{.Error}}'",
			expected: "echo 'Error: something broke'",
		},
		{
			name:     "output path template",
			template: "echo 'Done: {{.OutputPath}}'",
			expected: "echo 'Done: /exports/2026-04-07'",
		},
		{
			name:     "no variables",
			template: "echo 'hello'",
			expected: "echo 'hello'",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := renderTemplate(tt.template, data)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if result != tt.expected {
				t.Errorf("expected %q, got %q", tt.expected, result)
			}
		})
	}
}
