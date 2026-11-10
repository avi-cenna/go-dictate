package main

import (
	"math"
	"testing"
)

// targetPeakDB is the normalization target in decibels
const targetPeakDB = -2.0

// targetPeakLinear is -2 dB converted to linear scale: 10^(-2/20) ≈ 0.794
var targetPeakLinear = float32(math.Pow(10, targetPeakDB/20))

func TestNormalize(t *testing.T) {
	tests := []struct {
		name     string
		input    []float32
		wantPeak float32 // expected max absolute value after normalization
	}{
		{
			name:     "quiet signal scaled up",
			input:    []float32{0.1, -0.2, 0.15, -0.1},
			wantPeak: targetPeakLinear,
		},
		{
			name:     "loud signal scaled down",
			input:    []float32{0.5, -1.0, 0.3},
			wantPeak: targetPeakLinear,
		},
		{
			name:     "silence unchanged",
			input:    []float32{0.0, 0.0, 0.0},
			wantPeak: 0.0, // no scaling possible
		},
		{
			name:     "single sample",
			input:    []float32{0.5},
			wantPeak: targetPeakLinear,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Make a copy to avoid modifying test data
			samples := make([]float32, len(tt.input))
			copy(samples, tt.input)

			result := Normalize(samples)

			// Find peak of result
			var peak float32
			for _, s := range result {
				if s > peak {
					peak = s
				}
				if -s > peak {
					peak = -s
				}
			}

			// Use small tolerance for floating point comparison
			const tolerance = 1e-6
			if diff := peak - tt.wantPeak; diff > tolerance || diff < -tolerance {
				t.Errorf("peak = %v, want %v", peak, tt.wantPeak)
			}

			// Verify relative proportions are preserved (for non-silent input)
			if tt.wantPeak > 0 && len(tt.input) > 1 {
				// Find scale factor from first non-zero sample
				var scale float32
				for i, s := range tt.input {
					if s != 0 {
						scale = result[i] / s
						break
					}
				}
				// All samples should be scaled by the same factor (within tolerance)
				for i, s := range tt.input {
					if s != 0 {
						got := result[i] / s
						if diff := got - scale; diff > tolerance || diff < -tolerance {
							t.Errorf("inconsistent scaling: sample %d scaled by %v, expected %v", i, got, scale)
						}
					}
				}
			}
		})
	}
}

func BenchmarkNormalize(b *testing.B) {
	// Simulate 1 second of audio at 16kHz
	samples := make([]float32, 16000)
	for i := range samples {
		// Simple sine wave with some variation
		samples[i] = float32(i%1000) / 2000.0
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = Normalize(samples)
	}
}

// generateSineWave creates a sine wave at the given frequency
func generateSineWave(freqHz float32, sampleRate int, durationSec float32) []float32 {
	numSamples := int(float32(sampleRate) * durationSec)
	samples := make([]float32, numSamples)
	for i := range samples {
		t := float32(i) / float32(sampleRate)
		samples[i] = float32(math.Sin(2 * math.Pi * float64(freqHz) * float64(t)))
	}
	return samples
}

// measureRMS calculates the root mean square of samples (a measure of signal power)
func measureRMS(samples []float32) float32 {
	if len(samples) == 0 {
		return 0
	}
	var sum float64
	for _, s := range samples {
		sum += float64(s) * float64(s)
	}
	return float32(math.Sqrt(sum / float64(len(samples))))
}

func TestHighPass(t *testing.T) {
	const sampleRate = 16000
	const cutoffHz float32 = 80.0
	const duration float32 = 0.5 // 500ms of audio

	tests := []struct {
		name       string
		inputFreq  float32
		shouldPass bool // true if frequency should pass through mostly unchanged
	}{
		{
			name:       "50Hz should be attenuated (below cutoff)",
			inputFreq:  50,
			shouldPass: false,
		},
		{
			name:       "30Hz should be attenuated (well below cutoff)",
			inputFreq:  30,
			shouldPass: false,
		},
		{
			name:       "200Hz should pass (above cutoff)",
			inputFreq:  200,
			shouldPass: true,
		},
		{
			name:       "1000Hz should pass (well above cutoff)",
			inputFreq:  1000,
			shouldPass: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			input := generateSineWave(tt.inputFreq, sampleRate, duration)
			inputRMS := measureRMS(input)

			result := HighPass(input, cutoffHz, sampleRate)
			outputRMS := measureRMS(result)

			ratio := outputRMS / inputRMS

			if tt.shouldPass {
				// Passband: should retain most of the signal (at least 70%)
				if ratio < 0.7 {
					t.Errorf("frequency %vHz should pass but was attenuated: ratio = %.2f", tt.inputFreq, ratio)
				}
			} else {
				// Stopband: should be significantly attenuated (less than 50%)
				if ratio > 0.5 {
					t.Errorf("frequency %vHz should be attenuated but ratio = %.2f", tt.inputFreq, ratio)
				}
			}
		})
	}
}

func TestLowPass(t *testing.T) {
	const sampleRate = 16000
	const cutoffHz float32 = 4000.0 // Using 4kHz for easier testing
	const duration float32 = 0.5

	tests := []struct {
		name       string
		inputFreq  float32
		shouldPass bool
	}{
		{
			name:       "1000Hz should pass (below cutoff)",
			inputFreq:  1000,
			shouldPass: true,
		},
		{
			name:       "2000Hz should pass (below cutoff)",
			inputFreq:  2000,
			shouldPass: true,
		},
		{
			name:       "6000Hz should be attenuated (above cutoff)",
			inputFreq:  6000,
			shouldPass: false,
		},
		{
			name:       "7000Hz should be attenuated (well above cutoff)",
			inputFreq:  7000,
			shouldPass: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			input := generateSineWave(tt.inputFreq, sampleRate, duration)
			inputRMS := measureRMS(input)

			result := LowPass(input, cutoffHz, sampleRate)
			outputRMS := measureRMS(result)

			ratio := outputRMS / inputRMS

			if tt.shouldPass {
				// Passband: should retain most of the signal (at least 70%)
				if ratio < 0.7 {
					t.Errorf("frequency %vHz should pass but was attenuated: ratio = %.2f", tt.inputFreq, ratio)
				}
			} else {
				// Stopband: should be significantly attenuated (less than 50%)
				if ratio > 0.5 {
					t.Errorf("frequency %vHz should be attenuated but ratio = %.2f", tt.inputFreq, ratio)
				}
			}
		})
	}
}

func BenchmarkHighPass(b *testing.B) {
	// 1 second of audio at 16kHz
	samples := generateSineWave(440, 16000, 1.0)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = HighPass(samples, 80, 16000)
	}
}

func BenchmarkLowPass(b *testing.B) {
	// 1 second of audio at 16kHz
	samples := generateSineWave(440, 16000, 1.0)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = LowPass(samples, 8000, 16000)
	}
}

// BenchmarkProcessAudioJFK benchmarks the full DSP pipeline on jfk.wav.
// Run with: just test -bench=BenchmarkProcessAudioJFK -benchtime=3x -v
func BenchmarkProcessAudioJFK(b *testing.B) {
	samples, err := readWAV(jfkSample(b))
	if err != nil {
		b.Skipf("Could not read %s: %v", jfkSampleWAV, err)
	}

	// Calculate and print duration (16kHz sample rate)
	const sampleRate = 16000
	durationSec := float64(len(samples)) / float64(sampleRate)
	b.Logf("jfk.wav duration: %.2f seconds (%d samples)", durationSec, len(samples))

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = ProcessAudio(samples)
	}
}
