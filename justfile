whisper_src   := justfile_directory() + "/third_party/whisper.cpp"
whisper_build := whisper_src + "/build_go"

# cgo finds headers in the whisper.cpp source tree and static libs in the cmake build dirs,
# like third_party/whisper.cpp/bindings/go/Makefile. Metal shaders are embedded in
# libggml-metal.a, so GGML_METAL_PATH_RESOURCES isn't needed.
#
# Why CGO_* and not C_INCLUDE_PATH/LIBRARY_PATH like the Makefile: those are read by cmake too.
# Exported globally, cmake treats C_INCLUDE_PATH dirs as implicit and strips them from -I, which
# breaks the Objective-C Metal sources (clang ignores C_INCLUDE_PATH for .m files).
# Setting CGO_CFLAGS/CGO_LDFLAGS replaces Go's default "-O2 -g", so it's restated here.
export CGO_CFLAGS  := "-O2 -g -I" + whisper_src + "/include -I" + whisper_src + "/ggml/include"
export CGO_LDFLAGS := "-O2 -g -L" + whisper_build + "/src -L" + whisper_build + "/ggml/src -L" + whisper_build + "/ggml/src/ggml-blas -L" + whisper_build + "/ggml/src/ggml-metal"

# NOTE: Go's build cache doesn't notice changes to C libraries (see `go help cache`).
# After bumping the submodule, run `go clean -cache` or build with -a.
#
# Build libwhisper + libparakeet + ggml static libs, and the whisper-cli binary
# that TestTranscribeCLI benchmarks the Go binding against (incremental, no-op
# when nothing changed).
#
# EXAMPLES=ON only generates the example targets; naming them explicitly on the
# build line keeps the heavy ones (talk-llama, server, wchess) from being built.
# The configure step runs every time rather than being guarded on CMakeCache.txt,
# so that editing the flags above actually takes effect on an existing build dir.
whisper:
    cmake -S {{ whisper_src }} -B {{ whisper_build }} \
        -DCMAKE_BUILD_TYPE=Release -DBUILD_SHARED_LIBS=OFF \
        -DWHISPER_BUILD_TESTS=OFF -DWHISPER_BUILD_EXAMPLES=ON -DWHISPER_BUILD_SERVER=OFF \
        -DGGML_CCACHE=OFF
    cmake --build {{ whisper_build }} --target whisper parakeet whisper-cli -j {{ num_cpus() }}

# Build the binary.
build: whisper
    go build -v
    codesign -s - -f --entitlements ~/ent.plist dictation-server

# Run tests. Pass in `-v` if you want verbose output.
# Everything is an integration test here: the transcription tests load real
# models and the DSP benchmark reads a real WAV, so there's no separate tier.
# Tests that need a model or whisper-cli skip themselves when it's absent.
test *flags: whisper
    go test {{ flags }} . ./autocorrect

# Run the HTTP server
serve *args:
    ./dictation-server serve {{ args }}

fmt:
    go fmt

check: build fmt test
    golangci-lint run

# Remove the whisper.cpp cmake build dir (e.g. after changing cmake flags)
clean:
    rm -rf {{ whisper_build }}
    go clean

# Print the cgo env, e.g. for an IDE or running `go` outside of just
print-env:
    @echo "export CGO_CFLAGS='${CGO_CFLAGS}'"
    @echo "export CGO_LDFLAGS='${CGO_LDFLAGS}'"
