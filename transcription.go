package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"runtime"
	"strings"
	"sync"
	"time"
	"unsafe"

	"github.com/ggerganov/whisper.cpp/bindings/go/pkg/whisper"
	"github.com/rs/zerolog/log"
)

type Transcriber interface {
	Init() error
	Deinit() error
	TranscribeSamples(samples []float32) *TranscriptionResult
	TranscribeFile(audioPath string) *TranscriptionResult
}

// GroqTranscriber implements the Transcriber interface using Groq's cloud API.
// API reference: https://console.groq.com/docs/speech-text
// Endpoint: POST https://api.groq.com/openai/v1/audio/transcriptions
// Supports: flac, mp3, mp4, mpeg, mpga, m4a, ogg, wav, webm
type GroqTranscriber struct {
	apiKey    string
	modelName string
	baseURL   string
}

// TranscriptionResult represents a transcription result
type TranscriptionResult struct {
	Success       bool   `json:"success"`
	Error         string `json:"error,omitempty"`
	Transcription string `json:"transcription,omitempty"`
	// RawTranscription is the transcriber's original text, set only when
	// autocorrect changed Transcription.
	RawTranscription string `json:"rawTranscription,omitempty"`
	SegmentCount     int    `json:"segmentCount"`
}

// VADConfig holds Voice Activity Detection configuration.
// Detailed VAD tuning params (threshold, min speech/silence, etc.) are omitted
// to let whisper.cpp use its internal defaults.
type VADConfig struct {
	Enabled   bool
	ModelPath string
}

// DefaultVADConfig returns VAD configuration with whisper.cpp defaults.
func DefaultVADConfig() VADConfig {
	return VADConfig{
		Enabled: true,
	}
}

// WhisperCpp holds Whisper model state - all fields are exposed for direct access.
// Following Casey's philosophy: data is just data, expose what you've committed to.
type WhisperCpp struct {
	mu        sync.Mutex    // Protects Model access
	Model     whisper.Model // The loaded Whisper model (nil if unloaded)
	ModelPath string        // Path to the model file
	LastUsed  time.Time     // Last time the model was used (for TTL tracking)
	TTL       time.Duration // How long to keep model loaded when idle

	// VAD configuration
	VAD VADConfig
}

// NewTranscriber returns the transcription backend selected by c.TranscriberType.
// Whisper is the default; parakeet and groq are opt-in via TRANSCRIBER_TYPE.
func NewTranscriber(c AppConfig) Transcriber {
	switch c.TranscriberType {
	case TranscriberParakeet:
		return NewParakeetTranscriber(c)
	case TranscriberGroq:
		return NewGroqTranscriber(c.GroqAPIKey, c.GroqModel)
	default:
		return NewWhisperTranscriber(c)
	}
}

// NewWhisperTranscriber creates a new WhisperCpp with the given model path.
// Starts the TTL checker goroutine automatically.
func NewWhisperTranscriber(c AppConfig) *WhisperCpp {
	vadConfig := VADConfig{
		Enabled:   c.VADEnabled,
		ModelPath: c.VADModelPath,
	}
	t := &WhisperCpp{
		ModelPath: c.ModelPath,
		TTL:       7 * time.Minute,
		VAD:       vadConfig,
	}
	log.Debug().Any("whispercpp", t).Msg("NewTranscriber")
	go TranscriberTTLChecker(t)
	return t
}

// WhisperCpp Transcriber interface implementation

// Init loads the Whisper model if not already loaded.
func (w *WhisperCpp) Init() error {
	w.mu.Lock()
	defer w.mu.Unlock()

	if w.Model != nil {
		w.LastUsed = time.Now()
		return nil
	}

	log.Info().Str("path", w.ModelPath).Msg("Loading whisper model")
	start := time.Now()

	model, err := whisper.New(w.ModelPath)
	if err != nil {
		return fmt.Errorf("failed to load model: %w", err)
	}

	w.Model = model
	w.LastUsed = time.Now()
	log.Info().Dur("duration", time.Since(start)).Msg("Model loaded")
	return nil
}

// Deinit unloads the Whisper model and frees resources.
func (w *WhisperCpp) Deinit() error {
	w.mu.Lock()
	defer w.mu.Unlock()

	if w.Model != nil {
		if err := w.Model.Close(); err != nil {
			return fmt.Errorf("failed to close model: %w", err)
		}
		w.Model = nil
		runtime.GC()
	}
	return nil
}

// TranscribeSamples transcribes audio from float32 samples using WhisperCpp.
func (w *WhisperCpp) TranscribeSamples(samples []float32) *TranscriptionResult {
	if err := w.Init(); err != nil {
		return &TranscriptionResult{Success: false, Error: err.Error()}
	}

	w.mu.Lock()
	defer w.mu.Unlock()

	ctx, err := w.Model.NewContext()
	if err != nil {
		return &TranscriptionResult{Success: false, Error: fmt.Sprintf("failed to create context: %v", err)}
	}

	// Configure context
	if err := ctx.SetLanguage("en"); err != nil {
		return &TranscriptionResult{Success: false, Error: fmt.Sprintf("failed to set language: %v", err)}
	}

	// Configure VAD if enabled (uses whisper.cpp internal defaults for threshold/timing)
	if w.VAD.Enabled && w.VAD.ModelPath != "" {
		ctx.SetVAD(true)
		ctx.SetVADModelPath(w.VAD.ModelPath)
		log.Debug().Str("model", w.VAD.ModelPath).Msg("VAD enabled")
	}

	// Process audio
	if err := ctx.Process(samples, nil, nil, nil); err != nil {
		return &TranscriptionResult{Success: false, Error: fmt.Sprintf("transcription failed: %v", err)}
	}

	// Collect segments
	var text strings.Builder
	segmentCount := 0
	for {
		segment, err := ctx.NextSegment()
		if err != nil {
			break
		}
		text.WriteString(segment.Text + " ")
		segmentCount++
	}
	log.Debug().Int("segments", segmentCount).Msg("Transcribed segments")

	w.LastUsed = time.Now()

	return &TranscriptionResult{
		Success:       true,
		Transcription: strings.TrimSpace(text.String()) + " ",
		SegmentCount:  segmentCount,
	}
}

// TranscribeFile transcribes audio from a WAV file path using WhisperCpp.
func (w *WhisperCpp) TranscribeFile(audioPath string) *TranscriptionResult {
	samples, err := readWAV(audioPath)
	if err != nil {
		return &TranscriptionResult{Success: false, Error: fmt.Sprintf("failed to read audio: %v", err)}
	}
	return w.TranscribeSamples(samples)
}

// TranscriberTTLChecker periodically checks if model should be unloaded based on TTL.
// Runs in a goroutine for the lifetime of the process.
func TranscriberTTLChecker(t *WhisperCpp) {
	ticker := time.NewTicker(25 * time.Second)
	defer ticker.Stop()

	for range ticker.C {
		t.mu.Lock()
		if t.Model != nil && time.Since(t.LastUsed) > t.TTL {
			log.Info().Dur("idle", time.Since(t.LastUsed)).Msg("Unloading model due to TTL")
			_ = t.Model.Close()
			t.Model = nil
			runtime.GC()
		}
		t.mu.Unlock()
	}
}

// GroqTranscriber implementation

// NewGroqTranscriber creates a new GroqTranscriber instance
func NewGroqTranscriber(apiKey, model string) *GroqTranscriber {
	if model == "" {
		model = "whisper-large-v3" // Default model
	}
	return &GroqTranscriber{
		apiKey:    apiKey,
		modelName: model,
		baseURL:   "https://api.groq.com/openai/v1/audio/transcriptions",
	}
}

// Init validates the API key is set
func (g *GroqTranscriber) Init() error {
	if g.apiKey == "" {
		return fmt.Errorf("GROQ_API_KEY environment variable not set")
	}
	return nil
}

// Deinit is a no-op for API-based transcriber
func (g *GroqTranscriber) Deinit() error {
	return nil
}

// TranscribeSamples converts float32 samples to WAV and calls the Groq API
func (g *GroqTranscriber) TranscribeSamples(samples []float32) *TranscriptionResult {
	// Convert float32 samples to WAV file bytes
	// First convert to byte representation
	sampleBytes := make([]byte, len(samples)*4)
	for i, sample := range samples {
		bits := uint32(0)
		bits = *(*uint32)(unsafe.Pointer(&sample))
		sampleBytes[i*4+0] = byte(bits)
		sampleBytes[i*4+1] = byte(bits >> 8)
		sampleBytes[i*4+2] = byte(bits >> 16)
		sampleBytes[i*4+3] = byte(bits >> 24)
	}

	// Convert to WAV format
	wavData, err := Float32BytesToWAV(sampleBytes, 16000)
	if err != nil {
		return &TranscriptionResult{
			Success: false,
			Error:   fmt.Sprintf("failed to create WAV: %v", err),
		}
	}

	return g.callGroqAPI(wavData, "audio.wav")
}

// TranscribeFile reads a file and calls the Groq API
func (g *GroqTranscriber) TranscribeFile(audioPath string) *TranscriptionResult {
	// Read the audio file
	audioData, err := os.ReadFile(audioPath)
	if err != nil {
		return &TranscriptionResult{
			Success: false,
			Error:   fmt.Sprintf("failed to read audio file: %v", err),
		}
	}

	return g.callGroqAPI(audioData, audioPath)
}

// groqAPIResponse represents the JSON response from Groq API
type groqAPIResponse struct {
	Text string `json:"text"`
}

// callGroqAPI makes the HTTP request to Groq's transcription API
func (g *GroqTranscriber) callGroqAPI(audioData []byte, filename string) *TranscriptionResult {
	// Create multipart form
	var requestBody bytes.Buffer
	writer := multipart.NewWriter(&requestBody)

	// Add model field
	if err := writer.WriteField("model", g.modelName); err != nil {
		return &TranscriptionResult{
			Success: false,
			Error:   fmt.Sprintf("failed to write model field: %v", err),
		}
	}

	// Add temperature field
	if err := writer.WriteField("temperature", "0"); err != nil {
		return &TranscriptionResult{
			Success: false,
			Error:   fmt.Sprintf("failed to write temperature field: %v", err),
		}
	}

	// Add response_format field
	if err := writer.WriteField("response_format", "json"); err != nil {
		return &TranscriptionResult{
			Success: false,
			Error:   fmt.Sprintf("failed to write response_format field: %v", err),
		}
	}

	// Add audio file
	filePart, err := writer.CreateFormFile("file", filename)
	if err != nil {
		return &TranscriptionResult{
			Success: false,
			Error:   fmt.Sprintf("failed to create form file: %v", err),
		}
	}

	if _, err := io.Copy(filePart, bytes.NewReader(audioData)); err != nil {
		return &TranscriptionResult{
			Success: false,
			Error:   fmt.Sprintf("failed to copy audio data: %v", err),
		}
	}

	if err := writer.Close(); err != nil {
		return &TranscriptionResult{
			Success: false,
			Error:   fmt.Sprintf("failed to close writer: %v", err),
		}
	}

	// Create HTTP request
	req, err := http.NewRequest("POST", g.baseURL, &requestBody)
	if err != nil {
		return &TranscriptionResult{
			Success: false,
			Error:   fmt.Sprintf("failed to create request: %v", err),
		}
	}

	// Set headers
	req.Header.Set("Authorization", "Bearer "+g.apiKey)
	req.Header.Set("Content-Type", writer.FormDataContentType())

	// Make the request
	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return &TranscriptionResult{
			Success: false,
			Error:   fmt.Sprintf("API request failed: %v", err),
		}
	}
	defer func() { _ = resp.Body.Close() }()

	// Read response body
	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return &TranscriptionResult{
			Success: false,
			Error:   fmt.Sprintf("failed to read response: %v", err),
		}
	}

	// Check status code
	if resp.StatusCode != http.StatusOK {
		return &TranscriptionResult{
			Success: false,
			Error:   fmt.Sprintf("API error (status %d): %s", resp.StatusCode, string(respBody)),
		}
	}

	// Parse JSON response
	var apiResp groqAPIResponse
	if err := json.Unmarshal(respBody, &apiResp); err != nil {
		return &TranscriptionResult{
			Success: false,
			Error:   fmt.Sprintf("failed to parse response: %v", err),
		}
	}

	log.Debug().Str("text", apiResp.Text).Msg("Groq transcription complete")

	return &TranscriptionResult{
		Success:       true,
		Transcription: apiResp.Text,
		SegmentCount:  1, // Groq returns full text, not segments
	}
}
