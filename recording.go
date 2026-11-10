package main

import (
	"encoding/binary"
	"fmt"
	"math"
	"sync"

	"github.com/gen2brain/malgo"
	"github.com/rs/zerolog/log"
)

const (
	format     = malgo.FormatF32
	sampleRate = 16000
)

// Recording encapsulates all state for an audio recording session.
// This struct-based approach (vs globals) is more idiomatic Go because:
// - Encapsulates related state and its protecting mutex together
// - Makes ownership and lifecycle of resources explicit
// - Improves testability and makes concurrent access patterns clearer
// - Allows multiple recordings if needed in the future
type Recording struct {
	mu              sync.Mutex // Protects the recording lifecycle (start/stop/active check)
	MiniaudioCtx    *malgo.AllocatedContext
	MiniaudioDevice *malgo.Device
	// CapturedBytes stores the raw audio bytes captured from the device.
	// Format is float32 (4 bytes per sample) at 16kHz mono, as configured in StartAudioCapture.
	// Use ToFloat32Samples() to convert to []float32 for transcription.
	CapturedBytes []byte
	Active        bool // Protected by mu: whether this recording is currently active
}

// StartAudioCapture initializes miniaudio and starts capturing audio from the default microphone.
// Audio is captured as 32-bit float mono at 16kHz sample rate.
// The caller is responsible for ensuring only one recording is active at a time.
func StartAudioCapture() (*Recording, error) {
	rec := &Recording{
		CapturedBytes: make([]byte, 0),
	}

	log.Info().Msg("Starting recording")

	// Initialize miniaudio context
	ctx, err := malgo.InitContext(nil, malgo.ContextConfig{}, func(message string) {
		//log.Debug().Msg(message)
	})
	if err != nil {
		return nil, fmt.Errorf("failed to initialize miniaudio context: %w", err)
	}
	rec.MiniaudioCtx = ctx

	// Configure device for capture only with float32 format
	deviceConfig := malgo.DefaultDeviceConfig(malgo.Capture)
	deviceConfig.Capture.Format = format
	deviceConfig.Capture.Channels = 1
	deviceConfig.SampleRate = sampleRate
	deviceConfig.Alsa.NoMMap = 1

	// Callback to handle incoming audio frames
	// Store raw bytes directly - they're already in float32 format from malgo
	onRecvFrames := func(outputSample, inputSample []byte, framecount uint32) {
		rec.CapturedBytes = append(rec.CapturedBytes, inputSample...)
	}

	captureCallbacks := malgo.DeviceCallbacks{
		Data: onRecvFrames,
	}

	device, err := malgo.InitDevice(rec.MiniaudioCtx.Context, deviceConfig, captureCallbacks)
	if err != nil {
		_ = rec.MiniaudioCtx.Uninit()
		rec.MiniaudioCtx.Free()
		rec.MiniaudioCtx = nil
		return nil, fmt.Errorf("failed to initialize audio device: %w", err)
	}
	rec.MiniaudioDevice = device

	if err := device.Start(); err != nil {
		device.Uninit()
		_ = rec.MiniaudioCtx.Uninit()
		rec.MiniaudioCtx.Free()
		rec.MiniaudioDevice = nil
		rec.MiniaudioCtx = nil
		return nil, fmt.Errorf("failed to start audio device: %w", err)
	}

	rec.Active = true
	log.Info().Msg("Recording started")
	return rec, nil
}

// StopAudioCapture stops the audio capture and returns the captured bytes.
// Bytes are in float32 format (4 bytes per sample) at 16kHz mono.
// Takes the Recording to stop as a parameter. Caller is responsible for ensuring it's the active recording.
func StopAudioCapture(rec *Recording) ([]byte, error) {
	if rec == nil {
		return nil, fmt.Errorf("recording is nil")
	}

	rec.mu.Lock()
	defer rec.mu.Unlock()

	if !rec.Active {
		return nil, fmt.Errorf("recording is not active")
	}

	rec.Active = false

	if rec.MiniaudioDevice != nil {
		rec.MiniaudioDevice.Uninit()
		rec.MiniaudioDevice = nil
	}

	if rec.MiniaudioCtx != nil {
		_ = rec.MiniaudioCtx.Uninit()
		rec.MiniaudioCtx.Free()
		rec.MiniaudioCtx = nil
	}

	bytes := rec.CapturedBytes
	numSamples := len(bytes) / 4 // 4 bytes per float32 sample

	log.Info().
		Int("bytes", len(bytes)).
		Int("samples", numSamples).
		Float64("seconds", float64(numSamples)/sampleRate).
		Msg("Recording stopped")

	return bytes, nil
}

// ToFloat32Samples converts the raw captured bytes (float32 format) to []float32 samples.
// Each float32 sample is 4 bytes in little-endian format.
func (r *Recording) ToFloat32Samples() []float32 {
	numSamples := len(r.CapturedBytes) / 4
	samples := make([]float32, numSamples)

	for i := 0; i < numSamples; i++ {
		bits := binary.LittleEndian.Uint32(r.CapturedBytes[i*4:])
		samples[i] = math.Float32frombits(bits)
	}

	return samples
}
