package main

import (
	"math"

	"gonum.org/v1/gonum/dsp/fourier"
)

// DSP pipeline for voice recording quality improvement.
// Planned filters: high-pass (~80Hz), normalization, compression, low-pass (~8kHz).
// See conversation history for detailed DSP pipeline discussion.

// NormalizationTargetDB is the peak normalization target in decibels.
// -2 dB provides headroom for later processing and inter-sample peaks.
const NormalizationTargetDB = -2.0

// normalizationTargetLinear is -2 dB in linear scale: 10^(-2/20) ≈ 0.794
var normalizationTargetLinear = float32(math.Pow(10, NormalizationTargetDB/20))

// Normalize scales audio samples so the peak reaches -2 dB (≈ 0.794).
// Returns a new slice; does not modify the input.
// Silent input (all zeros) is returned unchanged.
func Normalize(samples []float32) []float32 {
	if len(samples) == 0 {
		return samples
	}

	// Find current peak (max absolute value)
	var peak float32
	for _, s := range samples {
		if s > peak {
			peak = s
		}
		if -s > peak {
			peak = -s
		}
	}

	// Can't normalize silence
	if peak == 0 {
		result := make([]float32, len(samples))
		copy(result, samples)
		return result
	}

	// Calculate scale factor to reach target
	scale := normalizationTargetLinear / peak

	// Apply scaling
	result := make([]float32, len(samples))
	for i, s := range samples {
		result[i] = s * scale
	}

	return result
}

// HighPass removes frequencies below cutoffHz using FFT-based filtering.
// Returns a new slice; does not modify the input.
func HighPass(samples []float32, cutoffHz float32, sampleRate int) []float32 {
	if len(samples) == 0 {
		return samples
	}

	// Convert to float64 for gonum
	n := len(samples)
	input := make([]float64, n)
	for i, s := range samples {
		input[i] = float64(s)
	}

	// Create FFT
	fft := fourier.NewFFT(n)
	coeffs := fft.Coefficients(nil, input)

	// Calculate which bin corresponds to the cutoff frequency
	// Bin k corresponds to frequency k * sampleRate / N
	// So cutoff bin = cutoffHz * N / sampleRate
	cutoffBin := int(float32(n) * cutoffHz / float32(sampleRate))

	// Zero out bins below cutoff (high-pass = remove low frequencies)
	// Bin 0 is DC, bins 1 to cutoffBin are low frequencies
	for i := 0; i <= cutoffBin && i < len(coeffs); i++ {
		coeffs[i] = 0
	}

	// Inverse FFT (gonum FFT is unnormalized, so divide by N)
	output := fft.Sequence(nil, coeffs)
	scale := 1.0 / float64(n)

	// Convert back to float32 with normalization
	result := make([]float32, n)
	for i := range result {
		result[i] = float32(output[i] * scale)
	}

	return result
}

// LowPass removes frequencies above cutoffHz using FFT-based filtering.
// Returns a new slice; does not modify the input.
func LowPass(samples []float32, cutoffHz float32, sampleRate int) []float32 {
	if len(samples) == 0 {
		return samples
	}

	// Convert to float64 for gonum
	n := len(samples)
	input := make([]float64, n)
	for i, s := range samples {
		input[i] = float64(s)
	}

	// Create FFT
	fft := fourier.NewFFT(n)
	coeffs := fft.Coefficients(nil, input)

	// Calculate which bin corresponds to the cutoff frequency
	cutoffBin := int(float32(n) * cutoffHz / float32(sampleRate))

	// Zero out bins above cutoff (low-pass = remove high frequencies)
	for i := cutoffBin + 1; i < len(coeffs); i++ {
		coeffs[i] = 0
	}

	// Inverse FFT (gonum FFT is unnormalized, so divide by N)
	output := fft.Sequence(nil, coeffs)
	scale := 1.0 / float64(n)

	// Convert back to float32 with normalization
	result := make([]float32, n)
	for i := range result {
		result[i] = float32(output[i] * scale)
	}

	return result
}

// ProcessAudio applies the full DSP pipeline to audio samples:
// 1. High-pass filter (80Hz) - removes low-frequency rumble
// 2. Low-pass filter (8kHz) - removes high-frequency noise
// 3. Normalize to -2dB - consistent output level
//
// Assumes 16kHz sample rate. Returns a new slice.
func ProcessAudio(samples []float32) []float32 {
	const sampleRate = 16000
	const highPassCutoff = 80.0  // Remove rumble below 80Hz
	const lowPassCutoff = 8000.0 // Remove noise above 8kHz

	// Apply filters in order
	result := HighPass(samples, highPassCutoff, sampleRate)
	result = LowPass(result, lowPassCutoff, sampleRate)
	result = Normalize(result)

	return result
}
