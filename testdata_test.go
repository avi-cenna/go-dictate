package main

import (
	"os"
	"os/exec"
	"testing"
)

// jfkSampleWAV is the JFK clip that ships with the whisper.cpp submodule. Tests
// read it from there rather than from a copy at the repo root, so there's one
// fixture in the tree instead of two that can drift apart.
const jfkSampleWAV = "third_party/whisper.cpp/samples/jfk.wav"

// jfkSample returns the sample path, skipping the caller if the submodule isn't
// checked out yet.
func jfkSample(tb testing.TB) string {
	tb.Helper()

	if _, err := os.Stat(jfkSampleWAV); err != nil {
		tb.Skipf("Sample not found at %s (run `git submodule update --init`), skipping", jfkSampleWAV)
	}
	return jfkSampleWAV
}

// whisperCLIBuilt is where `just whisper` puts the whisper-cli binary.
const whisperCLIBuilt = "third_party/whisper.cpp/build_go/bin/whisper-cli"

// whisperCLIPath returns the whisper-cli binary to compare the Go binding
// against, preferring the one `just whisper` builds from the pinned submodule
// over whatever happens to be on PATH - they can be different versions.
// Skips the caller if neither is present.
func whisperCLIPath(tb testing.TB) string {
	tb.Helper()

	if _, err := os.Stat(whisperCLIBuilt); err == nil {
		return whisperCLIBuilt
	}
	if onPath, err := exec.LookPath("whisper-cli"); err == nil {
		return onPath
	}
	tb.Skipf("whisper-cli not found at %s or on PATH (run `just whisper`), skipping", whisperCLIBuilt)
	return ""
}
