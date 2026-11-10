#!/bin/bash
set -e

echo "Starting server in background..."
just serve &
SERVER_PID=$!
sleep 2

echo "Starting CPU profile capture..."
curl -o cpu-dictation.prof "http://localhost:8080/debug/pprof/profile?seconds=15" &
PROFILE_PID=$!

sleep 1
echo "Running dictation workload..."

# Start recording
curl -X POST http://localhost:8080/dictation/start
echo "Recording started - speak now for 5 seconds..."
sleep 5

# Stop recording (triggers transcription)
curl -X POST http://localhost:8080/dictation/stop
echo "Transcription complete"

# Wait for profile to finish
wait $PROFILE_PID

# Stop server
kill $SERVER_PID

echo "Profile saved to cpu-dictation.prof"
echo "View with: go tool pprof -http=:6060 cpu-dictation.prof"
