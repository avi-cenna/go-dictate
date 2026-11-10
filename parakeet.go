package main

// Parakeet TDT 0.6B v3 transcription via whisper.cpp's parakeet C API.
//
// whisper.cpp ships parakeet as a second ASR engine with its own public header
// (third_party/whisper.cpp/include/parakeet.h) and its own static lib, but it has
// no upstream Go bindings - only Ruby does. So the cgo shim lives here, next to
// the Transcriber that uses it, rather than in the pinned submodule.
//
// Unlike whisper, parakeet_full_params has no language, VAD, or initial-prompt
// fields: v3 is multilingual with no language selector, and the TDT decoder
// doesn't hallucinate on silence the way whisper does, so VAD isn't needed.

/*
// Only -lparakeet here: libparakeet.a also needs ggml, libc++ and the Metal/
// Accelerate frameworks, but the whisper binding this package already imports
// declares all of those, and repeating them just makes ld warn about duplicates.
#cgo LDFLAGS: -lparakeet
#include <parakeet.h>
#include <stdlib.h>
*/
import "C"

import (
	"fmt"
	"runtime"
	"strings"
	"sync"
	"time"
	"unsafe"

	"github.com/rs/zerolog/log"
)

// ParakeetCpp holds Parakeet model state. Mirrors WhisperCpp: exposed fields,
// lazy load, idle unload after TTL.
type ParakeetCpp struct {
	mu        sync.Mutex                 // Protects ctx access
	ctx       *C.struct_parakeet_context // The loaded model (nil if unloaded)
	ModelPath string                     // Path to the model file
	LastUsed  time.Time                  // Last time the model was used (for TTL tracking)
	TTL       time.Duration              // How long to keep model loaded when idle
}

// NewParakeetTranscriber creates a ParakeetCpp with the given model path.
// Starts the TTL checker goroutine automatically.
func NewParakeetTranscriber(c AppConfig) *ParakeetCpp {
	t := &ParakeetCpp{
		ModelPath: c.ParakeetModelPath,
		TTL:       7 * time.Minute,
	}
	log.Debug().Str("model", t.ModelPath).Msg("NewParakeetTranscriber")
	go ParakeetTTLChecker(t)
	return t
}

// ParakeetCpp Transcriber interface implementation

// Init loads the Parakeet model if not already loaded.
func (p *ParakeetCpp) Init() error {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.ctx != nil {
		p.LastUsed = time.Now()
		return nil
	}

	log.Info().Str("path", p.ModelPath).Msg("Loading parakeet model")
	start := time.Now()

	cPath := C.CString(p.ModelPath)
	defer C.free(unsafe.Pointer(cPath))

	params := C.parakeet_context_default_params()
	params.use_gpu = C.bool(true)

	ctx := C.parakeet_init_from_file_with_params(cPath, params)
	if ctx == nil {
		return fmt.Errorf("failed to load model: %s", p.ModelPath)
	}

	p.ctx = ctx
	p.LastUsed = time.Now()
	log.Info().Dur("duration", time.Since(start)).Msg("Model loaded")
	return nil
}

// Deinit unloads the Parakeet model and frees resources.
func (p *ParakeetCpp) Deinit() error {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.ctx != nil {
		C.parakeet_free(p.ctx)
		p.ctx = nil
		runtime.GC()
	}
	return nil
}

// TranscribeSamples transcribes audio from float32 samples using ParakeetCpp.
// parakeet_full handles chunking internally, so arbitrary-length audio is fine.
func (p *ParakeetCpp) TranscribeSamples(samples []float32) *TranscriptionResult {
	if err := p.Init(); err != nil {
		return &TranscriptionResult{Success: false, Error: err.Error()}
	}

	p.mu.Lock()
	defer p.mu.Unlock()

	if len(samples) == 0 {
		return &TranscriptionResult{Success: false, Error: "no audio samples"}
	}

	// Init() bumps LastUsed, so the TTL checker can't unload between the two
	// locks - but a nil ctx here would segfault inside C rather than panic, so
	// it's worth one check to keep that from ever being the failure mode.
	if p.ctx == nil {
		return &TranscriptionResult{Success: false, Error: "model not loaded"}
	}

	params := C.parakeet_full_default_params(C.PARAKEET_SAMPLING_GREEDY)
	params.n_threads = C.int(parakeetThreads())

	// samples is Go-allocated and not retained by parakeet_full, so passing the
	// backing array directly is safe under cgo's pointer-passing rules.
	ret := C.parakeet_full(p.ctx, params, (*C.float)(unsafe.Pointer(&samples[0])), C.int(len(samples)))
	if ret != 0 {
		return &TranscriptionResult{Success: false, Error: fmt.Sprintf("transcription failed: %d", int(ret))}
	}

	// Collect segments
	var text strings.Builder
	segmentCount := int(C.parakeet_full_n_segments(p.ctx))
	for i := 0; i < segmentCount; i++ {
		cText := C.parakeet_full_get_segment_text(p.ctx, C.int(i))
		if cText == nil {
			continue
		}
		text.WriteString(C.GoString(cText) + " ")
	}
	log.Debug().Int("segments", segmentCount).Msg("Transcribed segments")

	p.LastUsed = time.Now()

	return &TranscriptionResult{
		Success:       true,
		Transcription: strings.TrimSpace(text.String()) + " ",
		SegmentCount:  segmentCount,
	}
}

// TranscribeFile transcribes audio from a WAV file path using ParakeetCpp.
func (p *ParakeetCpp) TranscribeFile(audioPath string) *TranscriptionResult {
	samples, err := readWAV(audioPath)
	if err != nil {
		return &TranscriptionResult{Success: false, Error: fmt.Sprintf("failed to read audio: %v", err)}
	}
	return p.TranscribeSamples(samples)
}

// ParakeetTTLChecker periodically checks if the model should be unloaded based on TTL.
// Runs in a goroutine for the lifetime of the process.
func ParakeetTTLChecker(p *ParakeetCpp) {
	ticker := time.NewTicker(25 * time.Second)
	defer ticker.Stop()

	for range ticker.C {
		p.mu.Lock()
		if p.ctx != nil && time.Since(p.LastUsed) > p.TTL {
			log.Info().Dur("idle", time.Since(p.LastUsed)).Msg("Unloading model due to TTL")
			C.parakeet_free(p.ctx)
			p.ctx = nil
			runtime.GC()
		}
		p.mu.Unlock()
	}
}

// parakeetThreads matches parakeet-cli's default: min(4, hardware concurrency).
func parakeetThreads() int {
	if n := runtime.NumCPU(); n < 4 {
		return n
	}
	return 4
}
