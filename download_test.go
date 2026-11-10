package main

import (
	"net/http"
	"testing"
	"time"
)

// TestModelURLsValid verifies all model download URLs are reachable using HEAD requests.
// This catches broken URLs without downloading gigabytes of data.
func TestModelURLsValid(t *testing.T) {
	client := &http.Client{
		Timeout: 10 * time.Second,
	}

	for _, model := range availableModels {
		t.Run(model.Name, func(t *testing.T) {
			req, err := http.NewRequest("HEAD", model.URL, nil)
			if err != nil {
				t.Fatalf("failed to create request: %v", err)
			}

			resp, err := client.Do(req)
			if err != nil {
				t.Fatalf("HEAD request failed for %s: %v", model.URL, err)
			}
			defer func() { _ = resp.Body.Close() }()

			if resp.StatusCode != http.StatusOK {
				t.Errorf("URL %s returned status %d, expected 200", model.URL, resp.StatusCode)
			}

			// Optionally verify Content-Length matches expected size (if server provides it)
			if resp.ContentLength > 0 {
				// Allow 10% variance in case sizes change slightly between versions
				minSize := int64(float64(model.Size) * 0.9)
				maxSize := int64(float64(model.Size) * 1.1)
				if resp.ContentLength < minSize || resp.ContentLength > maxSize {
					t.Logf("Warning: Content-Length %d differs from expected %d for %s",
						resp.ContentLength, model.Size, model.Name)
				}
			}
		})
	}
}

func TestFormatBytes(t *testing.T) {
	tests := []struct {
		input    int64
		expected string
	}{
		{0, "0 B"},
		{100, "100 B"},
		{1024, "1.0 KB"},
		{1536, "1.5 KB"},
		{1048576, "1.0 MB"},
		{1073741824, "1.0 GB"},
		{1624199872, "1.5 GB"},
	}

	for _, tt := range tests {
		t.Run(tt.expected, func(t *testing.T) {
			result := formatBytes(tt.input)
			if result != tt.expected {
				t.Errorf("formatBytes(%d) = %q, want %q", tt.input, result, tt.expected)
			}
		})
	}
}
