package main

import (
	"os"
	"path/filepath"
)

// TranscriberType selects the transcription backend.
type TranscriberType int

const (
	TranscriberWhisper TranscriberType = iota
	TranscriberGroq
	TranscriberParakeet
)

// AppConfig holds top-level configuration for the application.
// VAD tuning params (threshold, min speech/silence, etc.) are omitted;
// whisper.cpp uses its internal defaults for those.
type AppConfig struct {
	TranscriberType TranscriberType

	// Whisper model path
	ModelPath string

	// Parakeet model path (only used when TranscriberType == TranscriberParakeet)
	ParakeetModelPath string

	// Initial prompt for whisper context (helps with domain-specific vocabulary)
	InitialPrompt string

	// VAD settings
	VADEnabled   bool
	VADModelPath string

	// Groq settings (only used when TranscriberType == TranscriberGroq)
	GroqAPIKey string
	GroqModel  string

	// Remove dictation fillers ("you know", "um", ...) from transcriptions
	AutocorrectEnabled bool
}

// DefaultAppConfig returns an AppConfig with sensible defaults.
// Env vars override defaults where set.
func DefaultAppConfig() AppConfig {
	home, _ := os.UserHomeDir()

	cfg := AppConfig{
		TranscriberType:   TranscriberWhisper,
		ModelPath:         filepath.Join(home, "Synct", "ggml", "ggml-large-v3-turbo.bin"),
		ParakeetModelPath: filepath.Join(home, "Synct", "ggml", "ggml-parakeet-tdt-0.6b-v3-f16.bin"),
		VADEnabled:        true,
		VADModelPath:      "./models/ggml-silero-v6.2.0.bin",
		GroqModel:         "whisper-large-v3",

		AutocorrectEnabled: true,
	}

	switch os.Getenv("TRANSCRIBER_TYPE") {
	case "groq":
		cfg.TranscriberType = TranscriberGroq
	case "parakeet":
		cfg.TranscriberType = TranscriberParakeet
	}
	if v := os.Getenv("WHISPER_MODEL_PATH"); v != "" {
		cfg.ModelPath = v
	}
	if v := os.Getenv("PARAKEET_MODEL_PATH"); v != "" {
		cfg.ParakeetModelPath = v
	}
	if v := os.Getenv("GROQ_API_KEY"); v != "" {
		cfg.GroqAPIKey = v
	}
	if v := os.Getenv("GROQ_MODEL"); v != "" {
		cfg.GroqModel = v
	}
	if os.Getenv("AUTOCORRECT") == "off" {
		cfg.AutocorrectEnabled = false
	}

	return cfg
}
