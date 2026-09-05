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

The voice build carries **two** build tags: `whisper` (local ASR) and
`sqlite_fts5` (the file-intelligence index's SQLite FTS5 full-text search). The
`sqlite_fts5` tag is required whenever `internal/fileindex` is compiled — leaving
it off breaks file search — so it belongs in every build, test, and vet command
that touches the index.

```bash
export PATH="$PATH:/c/w64devkit/bin"
go build -tags "whisper sqlite_fts5" -ldflags="-s -w -H windowsgui" -o voice-agent.exe ./cmd/app

# File-index tests (also need the tag):
go test -tags sqlite_fts5 ./internal/fileindex/... ./config/...
```

Set `"enable_voice": true` in `config.json`. On startup you should see
`✅ Whisper model loaded successfully.`

## File-intelligence index (SQLite + FTS5 + optional local BGE)

`internal/fileindex` is a persistent file/folder index: a SQLite+FTS5 metadata
store scanned + watched (fsnotify) over the roots in `config.json`'s
`index_roots` (default: the user's `Documents`, `Desktop`, `Downloads`,
`Projects`, plus the working directory), with alias/usage/memory tiers and a
`0.40·text + 0.25·recency + 0.20·usage + 0.15·alias` rank. It replaces the old
in-memory `search.InitIndexer` walk. The DB lives at `fileindex.db` in the
working directory (gitignored).

Relevant `config.json` fields (all optional, sensible defaults applied):

```jsonc
{
  "index_roots":     ["C:/Users/you/Documents", "C:/Users/you/Projects"],
  "index_exclude":   ["node_modules", "AppData"], // merged with built-in excludes
  "semantic_search": true,                          // default true
  "bge_model_path":  "models/bge-small-en-v1.5/model.onnx",
  "bge_vocab_path":  "models/bge-small-en-v1.5/vocab.txt"
}
```

### Optional: local semantic fallback (BGE-small + onnxruntime)

Tier 3 is a **lazy, local, in-process** ONNX embedder — BGE-small-en-v1.5
(384-dim, MIT). It is entirely optional: when `semantic_search` is false, or the
model/vocab is missing, or the onnxruntime shared library cannot be loaded,
`fileindex.NewBGEEmbedder` returns an error, `main` logs it, and the index runs
on Tiers 1–2 (in-RAM alias/hot cache + SQLite/FTS5) only. No network, ever — all
embedding is local.

To enable it:

1. Fetch the BGE-small ONNX model + `vocab.txt` (~130 MB) into
   `models/bge-small-en-v1.5/` (the `models/` folder is gitignored):

   ```bash
   mkdir -p models/bge-small-en-v1.5
   curl -L -o models/bge-small-en-v1.5/model.onnx \
     https://huggingface.co/BAAI/bge-small-en-v1.5/resolve/main/onnx/model.onnx
   curl -L -o models/bge-small-en-v1.5/vocab.txt \
     https://huggingface.co/BAAI/bge-small-en-v1.5/resolve/main/vocab.txt
   ```

2. Make the ONNX Runtime shared library loadable (`onnxruntime.dll` on `PATH`,
   or next to `voice-agent.exe`). Download the matching Windows x64 release from
   https://github.com/microsoft/onnxruntime/releases. `github.com/yalue/onnxruntime_go`
   loads it dynamically at runtime, so it is not needed at link time — the build
   links without it; semantic search simply stays off until the DLL is present.

On success startup logs `[fileindex] local BGE semantic search enabled`;
otherwise `[fileindex] semantic search disabled (...)`.

## Using voice

Click the pill overlay (or wire a wake word) to start listening. Speech is transcribed locally
by Whisper, then routed through the same tiered dispatch as typed commands (Tier 0 local resolver
→ Tier 1 cloud fallback).

## Wake word (KWS) model

The wake word is a **local, keyless** sherpa-onnx Keyword Spotter — no account, no access key,
no network. It is included in the `-tags whisper` voice build and starts automatically when
`enable_voice: true` **and** `wake_word_enabled` is true (the default) and the KWS model is
present under `kws_model_path` (default `models/kws/`). Missing model → the wake word simply
disables (logged, `wake word off; pill + typing still work`); the app keeps running.

1. Download a sherpa-onnx **streaming KWS transducer** model (e.g.
   `sherpa-onnx-kws-zipformer-gigaspeech-3.3M-2024-01-01`) and place its
   `encoder.onnx`, `decoder.onnx`, `joiner.onnx`, and `tokens.txt` under `models/kws/`.
   These files are gitignored — only the curated `models/kws/keywords.txt` is checked in.

   ```bash
   mkdir -p models/kws
   # extract encoder.onnx, decoder.onnx, joiner.onnx, tokens.txt into models/kws/
   ```

2. Copy the sherpa runtime DLLs next to `voice-agent.exe`:
   `sherpa-onnx-c-api.dll`, `sherpa-onnx-cxx-api.dll`, and `onnxruntime.dll`. They live in the
   `sherpa-onnx-go-windows` module's `lib/x86_64-pc-windows-gnu` directory in the Go module
   cache:

   ```bash
   GP=$(go env GOMODCACHE)
   LIB="$GP/github.com/k2-fsa/sherpa-onnx-go-windows@v1.13.7/lib/x86_64-pc-windows-gnu"
   cp "$LIB/sherpa-onnx-c-api.dll" "$LIB/sherpa-onnx-cxx-api.dll" "$LIB/onnxruntime.dll" .
   ```

   (This `onnxruntime.dll` also serves the BGE semantic file index — one copy covers both.)

3. Set the wake word via `wake_word` in `config.json` — it must be one of the phrases in
   `models/kws/keywords.txt` (defaults to `hey jarvis`).

> **Tokenizer note:** the checked-in `keywords.txt` is pre-tokenized for the standard gigaspeech
> BPE model. If the model you download uses a different tokenizer, regenerate `keywords.txt` with
> sherpa's `text2token.py` (`--tokens ... --tokens-type bpe --bpe-model ...`), keeping the
> `@label` suffixes so the configured `wake_word` still resolves to a phrase.
