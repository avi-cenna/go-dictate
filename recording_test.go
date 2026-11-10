package main

import (
	"fmt"
	"testing"
	"time"
)

func TestRecording(t *testing.T) {
	rec, err := StartAudioCapture()
	fmt.Println(rec)
	if err != nil {
		t.Fatalf("Failed to start recording: %v", err)
	}
	time.Sleep(2 * time.Second)
	bytes, err := StopAudioCapture(rec)
	if err != nil {
		t.Fatalf("Failed to stop recording: %v", err)
	}

	if len(bytes) == 0 {
		t.Fatal("Expected bytes to be non-empty, but got 0 bytes")
	}

	t.Logf("Recorded %d bytes (%d samples)", len(bytes), len(bytes)/4)

	// Convert bytes to float32 samples for WAV writing
	samples := rec.ToFloat32Samples()
	err = writeWAV("dummy.wav", samples)
	if err != nil {
		t.Fatalf("Failed to write dummy.wav: %v", err)
	}
	t.Log("Written dummy.wav file for manual verification")
}
