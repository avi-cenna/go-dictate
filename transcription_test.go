package main

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestVADConfigDefaults(t *testing.T) {
	config := DefaultVADConfig()

	if !config.Enabled {
		t.Error("Expected VAD to be enabled by default")
	}
	if config.ModelPath != "" {
		t.Errorf("Expected empty ModelPath, got %q", config.ModelPath)
	}
}

func TestNewTranscriberWithVAD(t *testing.T) {
	cfg := AppConfig{
		ModelPath:    "/path/to/model.bin",
		VADEnabled:   true,
		VADModelPath: "/path/to/vad-model.bin",
	}

	transcriber := NewWhisperTranscriber(cfg)

	if transcriber.ModelPath != "/path/to/model.bin" {
		t.Errorf("Expected ModelPath '/path/to/model.bin', got '%s'", transcriber.ModelPath)
	}
	if !transcriber.VAD.Enabled {
		t.Error("Expected VAD to be enabled")
	}
	if transcriber.VAD.ModelPath != "/path/to/vad-model.bin" {
		t.Errorf("Expected VAD ModelPath '/path/to/vad-model.bin', got '%s'", transcriber.VAD.ModelPath)
	}
}

func TestNewTranscriberSelectsBackend(t *testing.T) {
	cases := []struct {
		name string
		typ  TranscriberType
		want string
	}{
		{"default is whisper", TranscriberWhisper, "*main.WhisperCpp"},
		{"parakeet", TranscriberParakeet, "*main.ParakeetCpp"},
		{"groq", TranscriberGroq, "*main.GroqTranscriber"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := fmt.Sprintf("%T", NewTranscriber(AppConfig{TranscriberType: tc.typ}))
			if got != tc.want {
				t.Errorf("NewTranscriber returned %s, want %s", got, tc.want)
			}
		})
	}
}

func TestTranscribeJFK(t *testing.T) {
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

	// Create transcriber
	cfg := AppConfig{ModelPath: modelPath}
	transcriber := NewTranscriber(cfg)

	// Measure transcription time
	t.Log("Starting transcription of jfk.wav...")
	start := time.Now()

	result := transcriber.TranscribeFile(audioPath)

	elapsed := time.Since(start)

	// Check result
	if !result.Success {
		t.Fatalf("Transcription failed: %s", result.Error)
	}

	// Print results
	t.Logf("✓ Transcription completed successfully")
	t.Logf("  Time taken: %v", elapsed)
	t.Logf("  Segments: %d", result.SegmentCount)
	t.Logf("  Transcription: %s", result.Transcription)

	// Verify we got some transcription
	if result.Transcription == "" {
		t.Error("Expected non-empty transcription")
	}

	if result.SegmentCount == 0 {
		t.Error("Expected at least one segment")
	}
}

func TestTranscribeParakeetJFK(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatalf("Failed to get home directory: %v", err)
	}
	modelPath := filepath.Join(home, "Synct", "ggml", "ggml-parakeet-tdt-0.6b-v3-f16.bin")

	if _, err := os.Stat(modelPath); os.IsNotExist(err) {
		t.Skipf("Model file not found at %s, skipping test", modelPath)
	}

	audioPath := jfkSample(t)

	transcriber := NewParakeetTranscriber(AppConfig{ParakeetModelPath: modelPath})

	t.Log("Starting parakeet transcription of jfk.wav...")
	start := time.Now()
	result := transcriber.TranscribeFile(audioPath)
	elapsed := time.Since(start)

	if !result.Success {
		t.Fatalf("Transcription failed: %s", result.Error)
	}
	if result.Transcription == "" {
		t.Error("Expected non-empty transcription")
	}
	if result.SegmentCount == 0 {
		t.Error("Expected at least one segment")
	}

	t.Logf("Transcription (%v): %s", elapsed, result.Transcription)
}
