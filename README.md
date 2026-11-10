# Dictation Server

Audio transcription server using Whisper.cpp and Groq.

## Setup

whisper.cpp is a git submodule at `third_party/whisper.cpp`.

```bash
git submodule update --init
just build
```

## Usage

### Start the server

```bash
dictation-server serve -p 8080
```

### Transcribe a file

```bash
dictation-server transcribe audio.wav
```

## Profiling with pprof

The server includes built-in pprof endpoints for performance analysis and memory inspection.

### Available pprof endpoints

When the server is running (default: http://localhost:8080):

- `http://localhost:8080/debug/pprof/` - Index of available profiles
- `http://localhost:8080/debug/pprof/heap` - Heap profile (memory allocations)
- `http://localhost:8080/debug/pprof/goroutine` - Goroutine profile
- `http://localhost:8080/debug/pprof/profile?seconds=30` - CPU profile (30 second sample)
- `http://localhost:8080/debug/pprof/allocs` - All past memory allocations
- `http://localhost:8080/debug/pprof/block` - Block profile
- `http://localhost:8080/debug/pprof/mutex` - Mutex profile

### Inspecting memory usage

#### Option 1: Web UI (Interactive)

1. Start the server:
   ```bash
   dictation-server serve
   ```

2. Open the pprof web UI in your browser:
   ```bash
   go tool pprof -http=:6060 http://localhost:8080/debug/pprof/heap
   ```

3. This will open an interactive web interface at http://localhost:6060 where you can:
   - View flame graphs
   - See memory allocation graphs
   - Explore call graphs
   - Analyze top memory consumers

#### Option 2: Terminal Analysis

1. Capture a heap profile:
   ```bash
   curl http://localhost:8080/debug/pprof/heap > heap.prof
   ```

2. Analyze it:
   ```bash
   go tool pprof heap.prof
   ```

3. Useful commands in the pprof interactive shell:
   - `top` - Show top memory consumers
   - `list <function>` - Show line-by-line allocation for a function
   - `web` - Generate a graph (requires graphviz)
   - `png` - Save graph as PNG

#### Option 3: Live Interactive Mode

Connect directly to the running server:
```bash
go tool pprof http://localhost:8080/debug/pprof/heap
```

### CPU profiling

To profile CPU usage for 30 seconds:

```bash
go tool pprof -http=:6060 http://localhost:8080/debug/pprof/profile?seconds=30
```

### Investigating high memory usage

If you're seeing unexpectedly high memory usage (~200MB when baseline is <10MB):

1. Take a heap snapshot after model offloading:
   ```bash
   go tool pprof -http=:6060 http://localhost:8080/debug/pprof/heap
   ```

2. In the web UI, check:
   - **View > Top** - See which functions allocate the most memory
   - **View > Flame Graph** - Visual representation of allocation stack traces
   - **Sample > inuse_space** - Currently allocated memory
   - **Sample > alloc_space** - Total allocations (including freed memory)

3. Compare `inuse_space` vs `alloc_space` to see if memory is being held or just allocated frequently

4. Look for:
   - Cached data structures
   - Buffer pools that aren't releasing memory
   - CGo allocations (whisper.cpp model data)
   - Goroutine leaks (check `/debug/pprof/goroutine`)
