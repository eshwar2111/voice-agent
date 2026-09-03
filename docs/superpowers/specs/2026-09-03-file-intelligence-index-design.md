# Local File Intelligence Index (Design Spec)

**Date:** 2026-09-03
**Status:** Approved (design), pending implementation plan
**Builds on / replaces:** `internal/search` (the current in-memory name→files map).

---

## Program context

A Jarvis-style desktop agent should already *know* where the user's files are, not
start searching the disk when asked. Today `internal/search` builds an in-memory
name→`FileRecord` map by a one-time startup walk (no persistence, no live updates,
no ranking, no aliases, no usage learning, no semantic). This subsystem replaces
it with a **metadata-first, tiered file index** that resolves "open my latest
resume" in ~100–500 ms from a prepared index, using AI (embeddings) only as a
last-resort fallback.

**Guiding principle (from the approved design):** *do not treat every file like a
RAG document.* Metadata + FTS answers 80–90% of queries instantly; semantic is a
lazy, cached, quantized fallback over a small subset of files — never the first
hop, never the whole PC.

---

## Goals / success criteria

1. **Prepared, not on-demand:** an alias/metadata query returns in ~100–500 ms
   from the index; the filesystem is never walked at query time.
2. **Live:** file create/rename/delete/modify update the index incrementally
   (single-file, hash-compared) within a couple seconds — never a full rebuild.
3. **Persistent:** the index survives restarts (SQLite on disk); startup does a
   cheap reconcile, not a full re-embed.
4. **Ranked:** "latest resume" returns `Resume_2026.pdf` over `Resume_old.pdf`
   via `0.40·text + 0.25·recency + 0.20·usage + 0.15·alias`.
5. **Learns:** repeatedly opening a file raises its usage score; "this is my
   latest resume" pins an explicit alias that resolves instantly thereafter.
6. **Lazy/cheap semantic, fully local:** embeddings come from an in-process
   **BGE-small (bge-small-en-v1.5, 384-dim, MIT)** ONNX model — no cloud, no
   quota, private (content never leaves the machine). One file-level embedding per
   *content hash*, generated only on interaction or on a metadata miss, cached
   quantized, dedup'd across identical copies, over whitelisted types in configured
   folders only. Semantic auto-disables when the model/runtime is unavailable —
   everything else still works.
7. **Non-breaking:** `resolveFile`/`resolveFolder` keep working; the old in-memory
   `internal/search` is retired without regressing `open_file`/`open_explorer`.

---

## Architecture — three tiers

```
                 FILE SYSTEM
                      │
                File Watcher (fsnotify)  ── initial scan + live events
                      │
                      ▼
   Tier 1  HOT CACHE (RAM)     aliases + recent/frequent + explicit memory   ← checked first
                      │ miss
                      ▼
   Tier 2  SQLite + FTS5       metadata + filename/keyword full-text          ← 80–90% of queries
                      │ miss / low confidence
                      ▼
   Tier 3  SEMANTIC INDEX      lazy, quantized, file-level embeddings (cache) ← fallback only
                      │
                      ▼
                 Ranked candidates → top path
```

### New package `internal/fileindex`

| File | Responsibility |
|---|---|
| `store.go` | SQLite schema (open/migrate) + CRUD for files/aliases/usage/memory/embeddings; content-hash helpers. |
| `watcher.go` | Initial scan of configured roots (with excludes) + fsnotify live events → incremental upsert/delete. |
| `rank.go` | **Pure** scoring pipeline (the 0.40/0.25/0.20/0.15 blend) — the tested seam. |
| `alias.go` | Heuristic alias derivation (filename tokens + keyword rules) + explicit user memory. |
| `hotcache.go` | Tier-1 in-process LRU + alias map (recent/frequent/explicit), rebuilt from SQLite on start. |
| `content.go` | Lazy text extraction: txt/md/code/json/csv (stdlib); pdf (Go lib); docx/pptx/xlsx (archive/zip + XML). Whitelisted types only. |
| `embed.go` | `Embedder` interface + float16 quantization, cosine, SQLite vector cache keyed by content_hash, lazy backfill. |
| `bge.go` | In-process **ONNX BGE-small** embedder: WordPiece tokenizer + onnxruntime inference → 384-d vectors, mean-pooled + L2-normalized. |
| `fileindex.go` | The public API: `Search`, `Resolve(file/folder)`, `RecordOpen`, `Remember`, `Start(ctx)`. |

`internal/fileindex` imports only stdlib + `mattn/go-sqlite3`, `fsnotify`, the pdf
lib, and the ONNX runtime binding. Embeddings are produced by an injected
`Embedder` (the BGE impl is wired in from `main`), so semantic can be swapped or
disabled without touching the index — and the index never imports `internal/llm`.

---

## Tier 2 — SQLite schema (`fileindex.db`)

Kept tiny; the file on disk is the source of truth, the DB only points at it.

```sql
CREATE TABLE files (
  id            INTEGER PRIMARY KEY,
  path          TEXT UNIQUE NOT NULL,
  name          TEXT NOT NULL,
  ext           TEXT,
  parent        TEXT,          -- parent folder name
  is_dir        INTEGER NOT NULL DEFAULT 0,
  size          INTEGER,
  created_at    INTEGER,       -- unix
  modified_at   INTEGER,
  last_accessed INTEGER,
  content_hash  TEXT,          -- set lazily (only when content is read/embedded)
  usage_score   REAL NOT NULL DEFAULT 0
);
CREATE INDEX idx_files_name ON files(name);
CREATE INDEX idx_files_hash ON files(content_hash);

-- Full-text over the searchable text (name + parent + derived keywords).
CREATE VIRTUAL TABLE files_fts USING fts5(
  name, parent, keywords, content=''  -- external-content-free; we feed rows directly
);

CREATE TABLE aliases (          -- heuristic + user aliases → a file
  file_id INTEGER NOT NULL REFERENCES files(id) ON DELETE CASCADE,
  alias   TEXT NOT NULL,
  source  TEXT NOT NULL,        -- 'heuristic' | 'user'
  UNIQUE(file_id, alias)
);
CREATE INDEX idx_aliases_alias ON aliases(alias);

CREATE TABLE file_usage (
  file_id     INTEGER PRIMARY KEY REFERENCES files(id) ON DELETE CASCADE,
  open_count  INTEGER NOT NULL DEFAULT 0,
  last_opened INTEGER
);

CREATE TABLE file_memory (      -- explicit "latest_resume" → path
  key         TEXT PRIMARY KEY, -- normalized, e.g. "latest resume"
  path        TEXT NOT NULL,
  created_at  INTEGER
);

CREATE TABLE embeddings (       -- one vector per CONTENT HASH (dedups copies)
  content_hash TEXT PRIMARY KEY,
  dims         INTEGER NOT NULL,
  vec          BLOB NOT NULL,   -- float16 (2 bytes/dim)
  model        TEXT,
  created_at   INTEGER
);
```

**FTS5 requires the `sqlite_fts5` build tag** on the CGO sqlite build. Both build
commands gain it: `-tags "whisper sqlite_fts5"` (voice) and `-tags "sqlite_fts5"`
(lean). If FTS5 proves unavailable in the toolchain, the fallback is a normalized
token column + `LIKE` (still fast for short filenames) — but FTS5 is the target.

---

## Search pipeline & ranking

`Search(query, kind)` where `kind ∈ {any, file, folder}`:

1. **Tier 1 — alias cache:** normalize query; exact hit in the hot alias/memory
   map → return that path (after `os.Stat` existence check). Instant.
2. **Tier 2 — FTS5 + fuzzy:** query `files_fts` (name/parent/keywords) plus a
   filename-substring/fuzzy pass; collect candidates.
3. **Rank** (`rank.go`, pure): for each candidate,
   `score = 0.40·textMatch + 0.25·recency + 0.20·usage + 0.15·aliasMatch`
   - `textMatch`: normalized FTS/fuzzy score (exact name > prefix > token > fuzzy).
   - `recency`: exp-decay on `modified_at`/`last_accessed`.
   - `usage`: normalized `open_count` (from `file_usage`).
   - `aliasMatch`: 1.0 if the query matches a stored alias, else 0.
   If the top candidate clears a confidence threshold → return it.
4. **Tier 3 — semantic fallback** (only if 2–3 found nothing confident **and** an
   embedder is configured): embed the query, brute-force cosine over cached file
   vectors (lazily embedding a bounded set of likely candidates first), rerank,
   return top candidates.

`Resolve(kind)` is `Search` returning the single best path (what
`resolveFile`/`resolveFolder` need). `Search` can return a ranked list for the
disambiguation UI.

---

## Aliases, usage, explicit memory

- **Heuristic aliases** (`alias.go`, at index time, no LLM): tokenize the filename
  (split camelCase/snake/kebab/spaces, drop dates/version noise), plus a small
  keyword map (`resume`→{cv, resume, job}, `invoice`→{invoice, bill}, …). Stored
  `source='heuristic'`. No LLM calls → no quota cost.
- **Usage learning:** `RecordOpen(path)` bumps `file_usage.open_count`, sets
  `last_opened`, recomputes `usage_score`. Called wherever a file is opened
  (`open_file`, and `find_file`).
- **Explicit memory:** a `remember_file` tool — "this is my latest resume" →
  `file_memory["latest resume"] = <path>`; thereafter "open my latest resume"
  resolves via Tier 1 with zero search. `Remember(key, path)`; existence re-checked
  on use.

---

## Tier 3 — lazy, cheap semantic

- **What gets embedded:** whitelisted extensions (`pdf, docx, pptx, xlsx, txt, md,
  py, js, ts, go, json, csv, …`) inside configured folders only. Never
  `exe/dll/sys`, caches, `node_modules`, `.git`, images.
- **Model:** **bge-small-en-v1.5** (384-dim, MIT), run **in-process via ONNX**
  (onnxruntime). Query text is embedded the same way — all local, no network, no
  quota. `bge.go` does WordPiece tokenization (from the model's `vocab.txt`),
  onnxruntime inference, mean-pooling over token states, and L2 normalization →
  a 384-float vector. BGE convention: the query is prefixed with the model's
  retrieval instruction ("Represent this sentence for searching relevant
  passages:"); documents are embedded as-is.
- **When:** lazily — (a) when the user *interacts* with a file (open), or (b) on a
  metadata **miss**, embed a bounded set of likely candidates. Never a blanket
  embed-the-whole-PC pass. A slow, throttled background backfill may embed
  recently-used files during idle (throttled to avoid a CPU spike, not a quota).
  Nothing blocks a query on embedding.
- **Granularity:** **one file-level embedding** first (mean of the first N content
  chunks). Chunk-level embeddings are deferred until a specific file needs deep
  in-document search (out of this spec's first cut).
- **Dedup:** vectors keyed by `content_hash` — identical copies (`Resume.pdf`,
  `Resume - Copy.pdf`) share one embedding; multiple `files` rows point at the same
  hash.
- **Storage:** **float16** quantized blobs — 384 dims × 2 bytes = **768 B/vector**
  (~75 MB even at 100k embedded files, and far fewer in practice since only
  whitelisted+interacted files are embedded). Cosine similarity is brute-force in Go
  over the cached set; no FAISS/HNSW dependency in the first cut.
- **Embedder:** an injected interface, `Embed(ctx, texts []string) ([][]float32,
  error)`, implemented by `bge.go` (in-process ONNX). nil (model/runtime absent) →
  semantic disabled cleanly. The interface leaves room to slot a cloud or Ollama
  embedder behind it later without touching the index.

---

## Watcher & incremental updates

- **Initial scan** (`watcher.go`, background): walk configured roots, upsert
  metadata (path/name/ext/dates/size/parent), skipping the exclude list. This is
  the same cheap walk as today, but writing to SQLite and reconciling (delete rows
  whose path no longer exists).
- **Live** (fsnotify on the roots): create → upsert; rename/move → update path;
  delete → remove row + its aliases/usage (cascade) (embeddings kept if another
  path shares the hash); modify → if `modified_at`/hash changed, re-derive
  keywords/aliases and invalidate that file's embedding.
- **Incremental only:** one file per event; never rebuild.

**Config:**
```jsonc
"index_roots":   ["Documents","Desktop","Downloads","Projects"], // resolved under home; absolute ok
"index_exclude": ["node_modules",".git","build","dist","AppData","Windows","Program Files"],
"semantic_search": true,                 // default true; auto-off if the model isn't found
"bge_model_path": "models/bge-small-en-v1.5.onnx", // ONNX model; empty = default path
"bge_vocab_path": "models/bge-vocab.txt"           // WordPiece vocab; empty = default path
```
Roots default to the common user folders + the working directory; excludes have
sensible defaults merged with user additions. If the ONNX model/vocab or the
onnxruntime library isn't present, semantic silently disables and Tiers 1–2 carry
on.

---

## Integration

- **Retire `internal/search`'s in-memory map.** Keep the `FileRecord`/`SearchFiles`
  surface as a thin shim over `fileindex` (so nothing else breaks), or point the
  two callers directly at `fileindex`.
- **`resolveFile`/`resolveFolder`** (open_file.go / open_explorer.go) call
  `fileindex.Resolve`. `open_file` also calls `RecordOpen` on success.
- **New tools:** `find_file` (query → best path, opens or returns it) and
  `remember_file` (pin an explicit alias). Registered in `registry.go`.
- **`main.go`:** construct the index (`fileindex.New(dbPath, cfg, embedder)`),
  `Start(rootCtx)` (initial scan + watcher), inject the embedder (built from the
  provider layer, nil when semantic off). Replaces the current
  `search.InitIndexer(...)` call.

---

## Build considerations

- **`sqlite_fts5` build tag** added to both build commands (documented in
  CLAUDE.md/README/BUILD-VOICE.md).
- **New deps:** a pure-Go PDF text lib (e.g. `github.com/ledongthuc/pdf`);
  docx/pptx/xlsx via `archive/zip` + `encoding/xml` (no new dep); the **ONNX
  runtime Go binding** (e.g. `github.com/yalue/onnxruntime_go`) for BGE. Vetted for
  license/size in the plan.
- **ONNX runtime native lib + model assets:** `onnxruntime.dll` must be shippable /
  discoverable at runtime (same pattern as the vendored pvrecorder DLLs), and the
  `bge-small-en-v1.5.onnx` (~130 MB) + `vocab.txt` placed under `models/` (shipped
  or downloaded on first run; kept out of git via `.gitignore`, like the whisper
  model). Absence → semantic disabled, no crash.
- **WordPiece tokenizer:** a small in-repo WordPiece implementation reading the
  BGE `vocab.txt` (avoids a heavy tokenizer dependency); table-tested against known
  token ids.
- All go commands still prefixed with the w64devkit PATH; `internal/fileindex`
  links CGO (sqlite + onnxruntime) so its tests run under the toolchain. Tests that
  need the model are skipped when the model/runtime is absent (`t.Skip`), so the
  suite stays green without the ~130 MB asset.

---

## Phased implementation (one spec, sequenced tasks)

1. **Metadata core:** `store.go` (schema + CRUD) + `watcher.go` initial scan → SQLite; retire in-memory search behind the shim.
2. **Live watcher:** fsnotify incremental create/rename/delete/modify.
3. **Ranked search:** `rank.go` (pure, tested) + `fileindex.Search`/`Resolve`; wire `resolveFile`/`resolveFolder`.
4. **Aliases + usage + explicit memory:** `alias.go`, `hotcache.go`, `RecordOpen`, `Remember`, `find_file` + `remember_file` tools.
5. **Lazy semantic:** `content.go` (extraction) + `bge.go` (WordPiece + ONNX BGE-small → 384-d) + `embed.go` (float16 cache, dedup, cosine, throttled lazy embedding) + the Tier-3 fallback in `Search`. Model/runtime absent → the whole tier no-ops.

Each phase is independently testable and leaves the app working.

---

## Testing

- **rank.go** (pure): table tests — "latest resume" ranks 2026 over old; recency
  vs usage vs alias weighting; alias exact-match wins.
- **store.go**: temp-DB CRUD; upsert idempotency; delete cascade; FTS query returns
  expected rows; content-hash dedup.
- **alias.go**: filename tokenization + keyword map (pure, table-tested).
- **hotcache**: LRU eviction + alias/memory hit; rebuild from SQLite.
- **content.go**: extract text from a small sample txt/md and a tiny fixture
  pdf/docx; whitelist rejects binaries.
- **embed.go**: float16 round-trip within tolerance; cosine correctness; dedup by
  hash; nil-embedder → semantic cleanly skipped.
- **watcher**: create/delete/rename against a temp dir reflect in the store
  (fsnotify glue verified with a temp dir; excludes honored).
- **Manual:** "open my latest resume", "remember this as my resume", "open the
  voice agent project", "open the E folder", plus a semantic miss ("the document
  about my startup idea").

---

## Non-goals (first cut)

- Chunk-level in-document semantic search (file-level embedding only for now).
- FAISS/HNSW ANN index (brute-force cosine suffices at this scale).
- int8 quantization (float16 first; int8 only if scale demands).
- Cloud / Ollama embedders (local ONNX BGE chosen); either can slot into the same
  `Embedder` interface later without touching the index.
- GPU inference for BGE (CPU ONNX is the target; the model is small enough).
- OCR / image content understanding.
- A full personal knowledge graph UI (the data supports it; the graph view is later).
