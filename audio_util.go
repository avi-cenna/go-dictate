package main

import (
	"encoding/binary"
	"fmt"
	"io"
	"os"
	"unsafe"

	"github.com/rs/zerolog/log"
)

// readWAV reads a WAV file and returns float32 samples normalized to [-1, 1]
// Expects 16-bit PCM, converts to 16kHz mono if needed
func readWAV(path string) ([]float32, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()

	// Read RIFF header
	var riffHeader [12]byte
	if _, err := io.ReadFull(f, riffHeader[:]); err != nil {
		return nil, fmt.Errorf("failed to read RIFF header: %w", err)
	}
	if string(riffHeader[0:4]) != "RIFF" || string(riffHeader[8:12]) != "WAVE" {
		return nil, fmt.Errorf("not a valid WAV file")
	}

	// Find fmt and data chunks
	var sampleRate uint32
	var numChannels, bitsPerSample uint16
	var audioData []byte

	for {
		var chunkHeader [8]byte
		if _, err := io.ReadFull(f, chunkHeader[:]); err != nil {
			if err == io.EOF {
				break
			}
			return nil, fmt.Errorf("failed to read chunk header: %w", err)
		}

		chunkID := string(chunkHeader[0:4])
		chunkSize := binary.LittleEndian.Uint32(chunkHeader[4:8])

		switch chunkID {
		case "fmt ":
			fmtData := make([]byte, chunkSize)
			if _, err := io.ReadFull(f, fmtData); err != nil {
				return nil, fmt.Errorf("failed to read fmt chunk: %w", err)
			}
			audioFormat := binary.LittleEndian.Uint16(fmtData[0:2])
			if audioFormat != 1 {
				return nil, fmt.Errorf("unsupported audio format: %d (only PCM supported)", audioFormat)
			}
			numChannels = binary.LittleEndian.Uint16(fmtData[2:4])
			sampleRate = binary.LittleEndian.Uint32(fmtData[4:8])
			bitsPerSample = binary.LittleEndian.Uint16(fmtData[14:16])

		case "data":
			audioData = make([]byte, chunkSize)
			if _, err := io.ReadFull(f, audioData); err != nil {
				return nil, fmt.Errorf("failed to read audio data: %w", err)
			}

		default:
			// Skip unknown chunks
			if _, err := f.Seek(int64(chunkSize), io.SeekCurrent); err != nil {
				return nil, fmt.Errorf("failed to skip chunk %s: %w", chunkID, err)
			}
		}
	}

	if audioData == nil {
		return nil, fmt.Errorf("no audio data found")
	}

	// Convert to float32 samples
	var samples []float32

	switch bitsPerSample {
	case 16:
		numSamples := len(audioData) / 2
		samples = make([]float32, numSamples)
		for i := 0; i < numSamples; i++ {
			sample := int16(binary.LittleEndian.Uint16(audioData[i*2:]))
			samples[i] = float32(sample) / 32768.0
		}
	case 32:
		numSamples := len(audioData) / 4
		samples = make([]float32, numSamples)
		for i := 0; i < numSamples; i++ {
			sample := int32(binary.LittleEndian.Uint32(audioData[i*4:]))
			samples[i] = float32(sample) / 2147483648.0
		}
	default:
		return nil, fmt.Errorf("unsupported bits per sample: %d", bitsPerSample)
	}

	// Convert stereo to mono if needed
	if numChannels == 2 {
		mono := make([]float32, len(samples)/2)
		for i := 0; i < len(mono); i++ {
			mono[i] = (samples[i*2] + samples[i*2+1]) / 2
		}
		samples = mono
	}

	// Resample to 16kHz if needed (simple decimation, good enough for speech)
	if sampleRate != 16000 {
		ratio := float64(sampleRate) / 16000.0
		newLen := int(float64(len(samples)) / ratio)
		resampled := make([]float32, newLen)
		for i := 0; i < newLen; i++ {
			srcIdx := int(float64(i) * ratio)
			if srcIdx >= len(samples) {
				srcIdx = len(samples) - 1
			}
			resampled[i] = samples[srcIdx]
		}
		samples = resampled
		log.Debug().Uint32("from", sampleRate).Int("to", 16000).Msg("Resampled audio")
	}

	return samples, nil
}

// writeWAV writes float32 samples to a WAV file (16kHz mono 16-bit)
func writeWAV(path string, samples []float32) (err error) {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer func() {
		if cerr := f.Close(); cerr != nil && err == nil {
			err = cerr
		}
	}()

	// Convert float32 to int16
	audioData := make([]byte, len(samples)*2)
	for i, sample := range samples {
		// Clamp to [-1, 1]
		if sample > 1 {
			sample = 1
		} else if sample < -1 {
			sample = -1
		}
		intSample := int16(sample * 32767)
		binary.LittleEndian.PutUint16(audioData[i*2:], uint16(intSample))
	}

	// Write RIFF header
	dataSize := uint32(len(audioData))
	fileSize := 36 + dataSize

	if _, err := f.Write([]byte("RIFF")); err != nil {
		return err
	}
	if err := binary.Write(f, binary.LittleEndian, fileSize); err != nil {
		return err
	}
	if _, err := f.Write([]byte("WAVE")); err != nil {
		return err
	}

	// Write fmt chunk
	if _, err := f.Write([]byte("fmt ")); err != nil {
		return err
	}
	if err := binary.Write(f, binary.LittleEndian, uint32(16)); err != nil {
		return err
	}
	if err := binary.Write(f, binary.LittleEndian, uint16(1)); err != nil {
		return err
	}
	if err := binary.Write(f, binary.LittleEndian, uint16(1)); err != nil {
		return err
	}
	if err := binary.Write(f, binary.LittleEndian, uint32(16000)); err != nil {
		return err
	}
	if err := binary.Write(f, binary.LittleEndian, uint32(32000)); err != nil {
		return err
	}
	if err := binary.Write(f, binary.LittleEndian, uint16(2)); err != nil {
		return err
	}
	if err := binary.Write(f, binary.LittleEndian, uint16(16)); err != nil {
		return err
	}

	// Write data chunk
	if _, err := f.Write([]byte("data")); err != nil {
		return err
	}
	if err := binary.Write(f, binary.LittleEndian, dataSize); err != nil {
		return err
	}
	if _, err := f.Write(audioData); err != nil {
		return err
	}

	return nil
}

// Float32BytesToWAV converts raw float32 bytes (from audio capture) to a complete WAV file in memory.
// The input bytes are in float32 format (4 bytes per sample) at the specified sample rate, mono.
// Returns WAV file data (with headers) as []byte, suitable for API calls or file writing.
func Float32BytesToWAV(f32bytes []byte, sampleRate int) ([]byte, error) {
	if len(f32bytes)%4 != 0 {
		return nil, fmt.Errorf("f32bytes length must be multiple of 4, got %d", len(f32bytes))
	}

	// Convert float32 bytes to float32 samples first
	numSamples := len(f32bytes) / 4
	samples := make([]float32, numSamples)

	for i := 0; i < numSamples; i++ {
		bits := binary.LittleEndian.Uint32(f32bytes[i*4:])
		samples[i] = *(*float32)(unsafe.Pointer(&bits))
	}

	// Convert float32 samples to int16 PCM
	audioData := make([]byte, numSamples*2)
	for i, sample := range samples {
		// Clamp to [-1, 1]
		if sample > 1 {
			sample = 1
		} else if sample < -1 {
			sample = -1
		}
		intSample := int16(sample * 32767)
		binary.LittleEndian.PutUint16(audioData[i*2:], uint16(intSample))
	}

	// Build complete WAV file in memory
	dataSize := uint32(len(audioData))
	fileSize := 36 + dataSize

	// Pre-allocate buffer for entire WAV file
	buf := make([]byte, 0, 44+len(audioData))

	// Helper to append bytes
	append32 := func(v uint32) {
		b := make([]byte, 4)
		binary.LittleEndian.PutUint32(b, v)
		buf = append(buf, b...)
	}
	append16 := func(v uint16) {
		b := make([]byte, 2)
		binary.LittleEndian.PutUint16(b, v)
		buf = append(buf, b...)
	}

	// RIFF header
	buf = append(buf, []byte("RIFF")...)
	append32(fileSize)
	buf = append(buf, []byte("WAVE")...)

	// fmt chunk
	buf = append(buf, []byte("fmt ")...)
	append32(16) // fmt chunk size
	append16(1)  // audio format (PCM)
	append16(1)  // num channels (mono)
	append32(uint32(sampleRate))
	append32(uint32(sampleRate * 2)) // byte rate
	append16(2)                      // block align
	append16(16)                     // bits per sample

	// data chunk
	buf = append(buf, []byte("data")...)
	append32(dataSize)
	buf = append(buf, audioData...)

	return buf, nil
}
