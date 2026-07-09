# Building the voice-enabled binary

Voice (local Whisper speech-to-text) is **opt-in** via the `whisper` build tag. The default
`go build` produces a smaller, voice-free binary (see `internal/asr/stub.go`). This doc records
how to produce the voice-enabled build, including the one-time whisper.cpp rebuild.

## Why a rebuild is needed

The Go binding (`whisper.cpp/bindings/go`) links prebuilt static libs from
`whisper.cpp/build/{src,ggml/src}`. Those libs **must be compiled with the same GCC** as the Go
build's `CC`. A toolchain mismatch surfaces at link time as `undefined reference to std::...`
(libstdc++ ABI skew). This repo's toolchain is **w64devkit GCC 14.1** (`go env CC`), so the
whisper libs must be built with it.

## One-time: rebuild whisper.cpp with the current toolchain

Requires `cmake` (portable is fine) and w64devkit on `PATH`.

```bash
cd whisper.cpp
export PATH="$PATH:/c/w64devkit/bin"

# 1) Configure (static libs, no examples/tests, portable CPU flags)
cmake -B build -G "MinGW Makefiles" \
  -DCMAKE_BUILD_TYPE=Release \
  -DCMAKE_C_COMPILER="C:/w64devkit/bin/gcc.exe" \
  -DCMAKE_CXX_COMPILER="C:/w64devkit/bin/g++.exe" \
  -DCMAKE_MAKE_PROGRAM="C:/w64devkit/bin/mingw32-make.exe" \
  -DWHISPER_BUILD_TESTS=OFF -DWHISPER_BUILD_EXAMPLES=OFF \
  -DGGML_NATIVE=OFF -DBUILD_SHARED_LIBS=OFF

# 2) Apply the mingw header patch (see below) BEFORE building, then:
cmake --build build --config Release -j 4

# 3) The linker wants lib-prefixed names; cmake emits ggml*.a without the prefix:
cd build/ggml/src
for b in ggml ggml-base ggml-cpu; do cp -f "$b.a" "lib$b.a"; done
cd ../../..
```

### The mingw header patch (`docs/whisper-mingw-ggml-cpu.patch`)

w64devkit's (older) Windows headers lack `THREAD_POWER_THROTTLING_STATE` even though
`_WIN32_WINNT >= 0x0602`, so `ggml/src/ggml-cpu/ggml-cpu.c` fails to compile. Change the guard
around that block (~line 2483) from `#if _WIN32_WINNT >= 0x0602` to:

```c
#if defined(THREAD_POWER_THROTTLING_CURRENT_VERSION)
```

This skips a non-essential Win11 thread-throttling tweak when the header lacks the symbol.
Apply with: `git -C whisper.cpp apply ../docs/whisper-mingw-ggml-cpu.patch` (or edit by hand).
(This edit lives in the whisper.cpp submodule working tree; re-apply it if the submodule is
reset or re-cloned.)

## Get a model

The runtime needs a ggml Whisper model at the `whisper_model` path in `config.json`
(default `C:/whisper-local/models/ggml-base.en.bin`):

```bash
mkdir -p /c/whisper-local/models
curl -L -o /c/whisper-local/models/ggml-base.en.bin \
  https://huggingface.co/ggerganov/whisper.cpp/resolve/main/ggml-base.en.bin
```

## Build + enable

```bash
export PATH="$PATH:/c/w64devkit/bin"
go build -tags whisper -ldflags="-s -w -H windowsgui" -o voice-agent.exe ./cmd/app
```

Set `"enable_voice": true` in `config.json`. On startup you should see
`✅ Whisper model loaded successfully.`

## Using voice

Click the pill overlay (or wire a wake word) to start listening. Speech is transcribed locally
by Whisper, then routed through the same tiered dispatch as typed commands (Tier 0 local resolver
→ Tier 1 cloud fallback).

## Wake word (Porcupine)

The wake word ("Porcupine" built-in keyword) is included in the `-tags whisper` voice build and
starts automatically when `enable_voice: true` **and** `porcupine_access_key` is set in
`config.json`. Get a free access key at https://console.picovoice.ai. Without a valid key,
Porcupine init fails with `ACTIVATION_REFUSED` — this is logged (`wake word stopped: …`) and the
app keeps running; voice via the pill still works, just not hands-free activation.

### pvrecorder patch (already applied)

The upstream `pvrecorder` v1.2.4 Go binding has a copy-paste bug: it tries to load
`libpv_cheetah.dll` (from Picovoice's Cheetah engine) instead of its own `libpv_recorder.dll`,
and `log.Fatal`s at package init — crashing the app when the wake-word loop starts. This repo
vendors a corrected copy under `third_party/pvrecorder/` (DLL name fixed) wired via a `go.mod`
`replace` directive. No action needed; just don't remove the replace.
