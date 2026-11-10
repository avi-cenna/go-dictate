package main

import (
	"fmt"
	"html/template"
	"io"
	"net/http"
	_ "net/http/pprof" // Register pprof handlers
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"time"

	"dictation-server/autocorrect"

	"github.com/labstack/echo/v4"
	"github.com/labstack/echo/v4/middleware"
	"github.com/rs/zerolog/log"
)

// TemplateRenderer wraps Go's html/template for Echo.
type TemplateRenderer struct {
	templates *template.Template
}

// Render renders a template with the given data.
func (t *TemplateRenderer) Render(w io.Writer, name string, data interface{}, c echo.Context) error {
	return t.templates.ExecuteTemplate(w, name, data)
}

// Global server state - following Casey's philosophy of explicit singleton pattern.
// All fields are exposed for direct access and inspection.
var serverState *ServerState

// ServerState holds the server state - all fields are exposed.
// Following Casey's philosophy: data is just data, expose what you've committed to.
type ServerState struct {
	Mutex            sync.Mutex  // Protects CurrentRecording from concurrent handler access
	Port             int         // HTTP server port
	Echo             *echo.Echo  // Echo server instance
	CurrentRecording *Recording  // Currently active recording (nil if none), protected by Mutex
	Transcriber      Transcriber // Audio transcriber (WhisperCpp or GroqTranscriber)
}

// NewServer creates a new ServerState and sets the global singleton.
// Following Casey's philosophy: be explicit about singletons rather than hiding them.
func NewServer(transcriber Transcriber, port int) *ServerState {
	e := echo.New()
	e.HideBanner = true // Hide Echo startup banner (we log our own message)

	// Load templates
	t := &TemplateRenderer{
		templates: template.Must(template.ParseGlob("templates/*.html")),
	}
	e.Renderer = t

	serverState = &ServerState{
		Port:        port,
		Echo:        e,
		Transcriber: transcriber,
	}
	return serverState
}

// StartServer begins the HTTP server using the global serverState.
// Following Casey's philosophy: procedures that operate on the singleton state.
//
// pprof endpoints are available at /debug/pprof/* (Go's standard pprof via Echo wrapper).
func StartServer() error {
	e := serverState.Echo

	// Middleware
	e.Use(middleware.Recover()) // Recover from panics
	e.Use(echoLogger())         // Custom zerolog-based logger

	// Routes
	e.GET("/", handleIndex)
	e.POST("/dictation/start", handleStart)
	e.POST("/dictation/stop", handleStop)
	e.POST("/transcribe", handleTranscribe)
	e.GET("/health", handleHealth)

	// pprof routes (wrap Go's standard pprof handlers)
	e.GET("/debug/pprof/*", echo.WrapHandler(http.DefaultServeMux))

	addr := fmt.Sprintf(":%d", serverState.Port)
	log.Info().Str("addr", "http://localhost"+addr).Msg("Server listening")
	log.Info().Str("pprof", "http://localhost"+addr+"/debug/pprof/").Msg("pprof available")

	return e.Start(addr)
}

// echoLogger creates a custom Echo middleware that logs requests using zerolog.
// Following Casey's philosophy: free function that operates on global logger.
func echoLogger() echo.MiddlewareFunc {
	return func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c echo.Context) error {
			req := c.Request()
			res := c.Response()
			start := time.Now()

			err := next(c)

			log.Info().
				Str("method", req.Method).
				Str("uri", req.RequestURI).
				Int("status", res.Status).
				Dur("latency", time.Since(start)).
				Msg("Request")

			return err
		}
	}
}

// CloseServer cleans up server resources.
// Stops any active recording.
func CloseServer() {
	serverState.Mutex.Lock()
	rec := serverState.CurrentRecording
	serverState.Mutex.Unlock()

	if rec != nil {
		_, _ = StopAudioCapture(rec)
	}
}

// handleStart starts audio recording.
// Free function that operates on the global serverState singleton.
func handleStart(c echo.Context) error {
	// Check if recording is already in progress
	serverState.Mutex.Lock()
	if serverState.CurrentRecording != nil {
		serverState.Mutex.Unlock()
		return c.JSON(http.StatusBadRequest, map[string]string{
			"status":  "error",
			"message": "Recording already in progress",
		})
	}
	serverState.Mutex.Unlock()

	// Start recording
	rec, err := StartAudioCapture()
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{
			"status":  "error",
			"message": err.Error(),
		})
	}

	// Set the current recording
	serverState.Mutex.Lock()
	serverState.CurrentRecording = rec
	serverState.Mutex.Unlock()

	// Preload model in background
	go func() {
		if err := serverState.Transcriber.Init(); err != nil {
			log.Warn().Err(err).Msg("Failed to preload model")
		}
	}()

	return c.JSON(http.StatusOK, map[string]string{
		"status":  "success",
		"message": "Recording started",
	})
}

// handleStop stops audio recording and transcribes the recorded audio.
// Free function that operates on the global serverState singleton.
func handleStop(c echo.Context) error {
	// Get and clear the current recording
	serverState.Mutex.Lock()
	rec := serverState.CurrentRecording
	if rec == nil {
		serverState.Mutex.Unlock()
		return c.JSON(http.StatusBadRequest, &TranscriptionResult{
			Success: false,
			Error:   "No recording in progress",
		})
	}
	serverState.CurrentRecording = nil
	serverState.Mutex.Unlock()

	bytes, err := StopAudioCapture(rec)
	if err != nil {
		return c.JSON(http.StatusInternalServerError, &TranscriptionResult{
			Success: false,
			Error:   err.Error(),
		})
	}

	if len(bytes) == 0 {
		return c.JSON(http.StatusBadRequest, &TranscriptionResult{
			Success: false,
			Error:   "No audio recorded",
		})
	}

	// Convert bytes to float32 samples for transcription
	samples := rec.ToFloat32Samples()

	// Write WAV to temp folder in background
	go func() {
		tempPath := filepath.Join(os.TempDir(), fmt.Sprintf("dictation_%d.wav", time.Now().Unix()))
		if err := writeWAV(tempPath, samples); err != nil {
			log.Warn().Err(err).Msg("Failed to write debug WAV")
		} else {
			log.Debug().Str("path", tempPath).Msg("Debug audio saved")
		}
	}()

	// Transcribe
	result := serverState.Transcriber.TranscribeSamples(samples)
	autocorrectResult(result)
	return c.JSON(http.StatusOK, result)
}

// autocorrectResult removes dictation fillers and fixes misheard vocabulary in a
// successful transcription, keeping the original in RawTranscription.
func autocorrectResult(result *TranscriptionResult) {
	if !appConfig.AutocorrectEnabled || !result.Success || result.Transcription == "" {
		return
	}

	start := time.Now()
	corrected := autocorrect.Correct(result.Transcription)
	log.Debug().
		Str("raw", result.Transcription).
		Str("corrected", corrected).
		Dur("latency", time.Since(start)).
		Msg("Autocorrect")

	if corrected != result.Transcription {
		result.RawTranscription = result.Transcription
		result.Transcription = corrected
	}
}

// handleTranscribe accepts an audio file upload and returns a transcription.
// Converts the uploaded audio to PCM WAV via ffmpeg before transcribing.
// Supports optional VAD via "vad" form field.
func handleTranscribe(c echo.Context) error {
	file, err := c.FormFile("audio")
	if err != nil {
		return c.JSON(http.StatusBadRequest, &TranscriptionResult{
			Success: false,
			Error:   "No audio file provided",
		})
	}

	src, err := file.Open()
	if err != nil {
		return c.JSON(http.StatusInternalServerError, &TranscriptionResult{
			Success: false,
			Error:   fmt.Sprintf("Failed to open file: %v", err),
		})
	}
	defer func() { _ = src.Close() }()

	// Save to temp file
	tempFile, err := os.CreateTemp("", "upload-*")
	if err != nil {
		return c.JSON(http.StatusInternalServerError, &TranscriptionResult{
			Success: false,
			Error:   fmt.Sprintf("Failed to create temp file: %v", err),
		})
	}
	tempPath := tempFile.Name()
	defer func() { _ = os.Remove(tempPath) }()

	if _, err := io.Copy(tempFile, src); err != nil {
		_ = tempFile.Close()
		return c.JSON(http.StatusInternalServerError, &TranscriptionResult{
			Success: false,
			Error:   fmt.Sprintf("Failed to save file: %v", err),
		})
	}
	_ = tempFile.Close()

	// Convert to PCM WAV using ffmpeg (handles WAVE_FORMAT_EXTENSIBLE and other formats)
	convertedPath := tempPath + "-converted.wav"
	defer func() { _ = os.Remove(convertedPath) }()

	ffmpegCmd := exec.Command("ffmpeg", "-y", "-i", tempPath,
		"-acodec", "pcm_s16le", "-ar", "16000", "-ac", "1", convertedPath)
	if output, err := ffmpegCmd.CombinedOutput(); err != nil {
		log.Warn().Err(err).Str("output", string(output)).Msg("ffmpeg conversion failed")
		return c.JSON(http.StatusInternalServerError, &TranscriptionResult{
			Success: false,
			Error:   fmt.Sprintf("Audio conversion failed: %v", err),
		})
	}
	log.Debug().Str("input", tempPath).Str("output", convertedPath).Msg("Audio converted to PCM WAV")

	// Check if VAD is enabled
	vadEnabled := c.FormValue("vad") == "true"

	var result *TranscriptionResult

	if vadEnabled {
		if _, ok := serverState.Transcriber.(*WhisperCpp); ok {
			vadCfg := appConfig
			vadCfg.VADEnabled = true
			if vadCfg.VADModelPath == "" {
				vadCfg.VADModelPath = "./models/ggml-silero-v6.2.0.bin"
			}
			vadTranscriber := NewWhisperTranscriber(vadCfg)
			log.Info().Bool("vad", true).Str("vad_model", vadCfg.VADModelPath).Msg("Transcribing with VAD")
			result = vadTranscriber.TranscribeFile(convertedPath)
		} else {
			log.Warn().Msg("VAD requested but transcriber doesn't support it")
			result = serverState.Transcriber.TranscribeFile(convertedPath)
		}
	} else {
		log.Info().Bool("vad", false).Msg("Transcribing without VAD")
		result = serverState.Transcriber.TranscribeFile(convertedPath)
	}
	autocorrectResult(result)

	// Return HTML for HTMX requests, JSON for API clients
	if c.Request().Header.Get("HX-Request") == "true" {
		return c.Render(http.StatusOK, "result.html", result)
	}
	return c.JSON(http.StatusOK, result)
}

// handleHealth responds with server health status.
// Free function - doesn't need to access serverState.
func handleHealth(c echo.Context) error {
	return c.JSON(http.StatusOK, map[string]string{"status": "ok"})
}

// handleIndex serves the main frontend page.
func handleIndex(c echo.Context) error {
	return c.Render(http.StatusOK, "index.html", nil)
}
