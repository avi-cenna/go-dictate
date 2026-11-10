package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestTranscribeCLI tests transcription using the whisper-cli binary
// and compares performance with the Go binding implementation
func TestTranscribeCLI(t *testing.T) {
	cliPath := whisperCLIPath(t)
	t.Logf("Found whisper-cli at: %s", cliPath)

	// Use default model path
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatalf("Failed to get home directory: %v", err)
	}
	modelPath := filepath.Join(home, "Synct", "ggml", "ggml-large-v3-turbo.bin")

	// Check if model exists
	if _, err := os.Stat(modelPath); os.IsNotExist(err) {
		t.Skipf("Model file not found at %s, skipping test", modelPath)
	}

	audioPath := jfkSample(t)

	t.Log("=== Testing whisper-cli ===")

	// Run whisper-cli and measure time
	t.Log("Starting CLI transcription of jfk.wav...")
	startCLI := time.Now()

	cmd := exec.Command(cliPath, "-m", modelPath, "-f", audioPath)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("whisper-cli failed: %v\nOutput: %s", err, string(output))
	}

	cliElapsed := time.Since(startCLI)

	// Parse CLI output
	cliOutput := strings.TrimSpace(string(output))
	t.Logf("✓ CLI transcription completed")
	t.Logf("  Time taken: %v", cliElapsed)
	t.Logf("  Output length: %d bytes", len(cliOutput))

	// Now test Go binding for comparison
	t.Log("\n=== Testing Go binding ===")
	transcriber := NewTranscriber(AppConfig{ModelPath: modelPath})
	t.Log("Starting Go binding transcription of jfk.wav...")
	startGo := time.Now()

	result := transcriber.TranscribeFile(audioPath)

	goElapsed := time.Since(startGo)

	if !result.Success {
		t.Fatalf("Go transcription failed: %s", result.Error)
	}

	t.Logf("✓ Go binding transcription completed")
	t.Logf("  Time taken: %v", goElapsed)
	t.Logf("  Segments: %d", result.SegmentCount)
	t.Logf("  Transcription: %s", result.Transcription)

	// Compare timings
	t.Log("\n=== Performance Comparison ===")
	t.Logf("  CLI:        %v", cliElapsed)
	t.Logf("  Go binding: %v", goElapsed)
	if goElapsed < cliElapsed {
		speedup := float64(cliElapsed) / float64(goElapsed)
		t.Logf("  Go binding is %.2fx faster", speedup)
	} else {
		slowdown := float64(goElapsed) / float64(cliElapsed)
		t.Logf("  CLI is %.2fx faster", slowdown)
	}

	// Verify both produced output
	if result.Transcription == "" {
		t.Error("Expected non-empty transcription from Go binding")
	}

	if len(cliOutput) == 0 {
		t.Error("Expected non-empty output from CLI")
	}
}
