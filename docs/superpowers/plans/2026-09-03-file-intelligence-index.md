# Local File Intelligence Index — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace the in-memory `internal/search` with a tiered, persistent file-intelligence index (SQLite+FTS5 metadata, ranked search, aliases/usage/explicit memory, and a lazy local ONNX-BGE semantic fallback) so the agent resolves file/folder requests from a prepared index in ~100–500 ms.

**Architecture:** New `internal/fileindex` package. Tier 1 in-RAM alias/hot cache → Tier 2 SQLite+FTS5 metadata with a `0.40·text+0.25·recency+0.20·usage+0.15·alias` rank → Tier 3 lazy, float16-quantized, content-hash-deduped BGE-small (ONNX, in-process) semantic fallback. fsnotify keeps it live via incremental single-file updates. All embedding is local; semantic auto-disables if the model/runtime is absent.

**Tech Stack:** Go 1.26, `mattn/go-sqlite3` (with `sqlite_fts5` tag), `fsnotify`, `github.com/ledongthuc/pdf` (PDF text), `github.com/yalue/onnxruntime_go` (BGE), stdlib `archive/zip`+`encoding/xml` (docx/pptx/xlsx). CGO.

**Spec:** `docs/superpowers/specs/2026-09-03-file-intelligence-index-design.md`

## Global Constraints

- **New package `internal/fileindex` does NOT import `internal/llm`.** The embedder is an injected interface (`Embedder`), wired from main. It may import stdlib, `mattn/go-sqlite3`, `fsnotify`, the pdf lib, and `onnxruntime_go`.
- **Metadata-first:** the filesystem is never walked at query time; semantic is a fallback only, never the first hop, never the whole PC.
- **Ranking weights (verbatim):** `score = 0.40·textMatch + 0.25·recency + 0.20·usage + 0.15·aliasMatch`.
- **Semantic is local + optional:** BGE-small (bge-small-en-v1.5, 384-dim, MIT), in-process ONNX, float16 vectors, keyed/dedup'd by content_hash. If the model/vocab/runtime is absent, semantic disables cleanly (nil `Embedder`) — Tiers 1–2 still work.
- **FTS5** requires the `sqlite_fts5` build tag; add it to BOTH build commands.
- **Non-breaking:** `resolveFile`/`resolveFolder` (open_file.go/open_explorer.go) keep working; `internal/search`'s `SearchFiles(query) []FileRecord{Path,Name}` stays as a shim over fileindex until callers are migrated.
- Prefix every go command with `export PATH="$PATH:/c/w64devkit/bin"`. `internal/fileindex` links CGO. Model-dependent tests use `t.Skip` when the model/runtime is absent so the suite is green without the ~130 MB asset.
- Explicit `git add <files>` only — never `git add -A` (config.json holds secrets). Commit trailers:
  `Co-Authored-By: Claude Opus 4.8 <noreply@anthropic.com>` and `Claude-Session: https://claude.ai/code/session_01ALqrq9ftKjwzZc1miVW4Nf`.

**Existing surfaces (verbatim):**
```go
// internal/search/file_indexer.go
type FileRecord struct { Path string; Name string }
func SearchFiles(query string) []FileRecord
// SQLite open pattern (cmd/app/main.go): sql.Open("sqlite3", "<file>?_journal=WAL&_busy_timeout=5000")
// consumers: open_file.go resolveFile(query) / open_explorer.go resolveFolder(query)
```

---

## Task 1: SQLite store + schema

**Files:**
- Create: `internal/fileindex/store.go`, `internal/fileindex/store_test.go`

**Interfaces:**
- Produces:
  - `type File struct { ID int64; Path, Name, Ext, Parent string; IsDir bool; Size, CreatedAt, ModifiedAt, LastAccessed int64; ContentHash string; UsageScore float64 }`
  - `type Store struct{ db *sql.DB }`
  - `func OpenStore(path string) (*Store, error)` — opens WAL SQLite, runs migrations (all tables from the spec incl. `files_fts` FTS5).
  - `func (s *Store) Upsert(f File) (int64, error)` — insert/update by path; syncs `files_fts`.
  - `func (s *Store) DeleteByPath(path string) error` (cascade aliases/usage).
  - `func (s *Store) GetByPath(path string) (*File, bool, error)`
  - `func (s *Store) AllPaths() (map[string]int64, error)` — path→id, for reconcile.
  - `func (s *Store) SearchFTS(query string, limit int) ([]File, error)` — FTS5 over name/parent/keywords + LIKE fallback.
  - `func (s *Store) Close() error`

- [ ] **Step 1: Write the failing test** — `store_test.go`

```go
package fileindex

import (
	"path/filepath"
	"testing"
)

func openTestStore(t *testing.T) *Store {
	t.Helper()
	s, err := OpenStore(filepath.Join(t.TempDir(), "idx.db"))
	if err != nil { t.Fatalf("OpenStore: %v", err) }
	t.Cleanup(func() { s.Close() })
	return s
}

func TestUpsertGetSearch(t *testing.T) {
	s := openTestStore(t)
	id, err := s.Upsert(File{Path: `C:\Users\E\Documents\Resume_2026.pdf`, Name: "Resume_2026.pdf", Ext: "pdf", Parent: "Documents"})
	if err != nil || id == 0 { t.Fatalf("Upsert: %v id=%d", err, id) }

	got, ok, err := s.GetByPath(`C:\Users\E\Documents\Resume_2026.pdf`)
	if err != nil || !ok || got.Name != "Resume_2026.pdf" { t.Fatalf("GetByPath: %+v ok=%v err=%v", got, ok, err) }

	res, err := s.SearchFTS("resume", 10)
	if err != nil { t.Fatalf("SearchFTS: %v", err) }
	if len(res) != 1 || res[0].Name != "Resume_2026.pdf" { t.Fatalf("SearchFTS got %+v", res) }
}

func TestUpsertIdempotentAndDelete(t *testing.T) {
	s := openTestStore(t)
	p := `C:\x\a.txt`
	id1, _ := s.Upsert(File{Path: p, Name: "a.txt", Ext: "txt"})
	id2, _ := s.Upsert(File{Path: p, Name: "a.txt", Ext: "txt", Size: 5})
	if id1 != id2 { t.Fatalf("upsert not idempotent: %d != %d", id1, id2) }
	if err := s.DeleteByPath(p); err != nil { t.Fatalf("delete: %v", err) }
	if _, ok, _ := s.GetByPath(p); ok { t.Fatal("row still present after delete") }
}
```

- [ ] **Step 2: Run to verify it fails**

Run: `export PATH="$PATH:/c/w64devkit/bin" && go test -tags sqlite_fts5 ./internal/fileindex/ -run TestUpsert`
Expected: FAIL — `undefined: OpenStore`.

- [ ] **Step 3: Implement `store.go`**

Open with `sql.Open("sqlite3", path+"?_journal=WAL&_busy_timeout=5000")`, `db.SetMaxOpenConns(1)`. In `OpenStore`, `Exec` the schema from the spec (the `CREATE TABLE ... IF NOT EXISTS` set + `files_fts` FTS5 virtual table + indexes). `Upsert`: `INSERT ... ON CONFLICT(path) DO UPDATE SET ...` returning the id (query `SELECT id FROM files WHERE path=?` after), then upsert the matching `files_fts` row (delete+insert keyed by rowid=id; `files_fts` uses explicit rowid = files.id). `SearchFTS`: run `SELECT f.* FROM files_fts JOIN files f ON f.id=files_fts.rowid WHERE files_fts MATCH ?` (escape the query into a prefix token, e.g. `resume*`); on any FTS error, fall back to `SELECT * FROM files WHERE name LIKE '%'||?||'%'`. `keywords` column is populated by the caller (Task 5 aliases) — empty for now.

- [ ] **Step 4: Run to verify it passes**

Run: `export PATH="$PATH:/c/w64devkit/bin" && go test -tags sqlite_fts5 ./internal/fileindex/ -run TestUpsert -v`
Expected: PASS. If FTS5 is unavailable (build error mentioning fts5), the LIKE fallback path still passes `SearchFTS`; note it in the report.

- [ ] **Step 5: Commit**

```bash
git add internal/fileindex/store.go internal/fileindex/store_test.go
git commit -m "feat(fileindex): SQLite store + FTS5 schema (files/aliases/usage/memory/embeddings)"
```

---

## Task 2: Initial scan + API skeleton + retire in-memory search

**Files:**
- Create: `internal/fileindex/watcher.go` (initial scan only in this task), `internal/fileindex/fileindex.go`
- Modify: `internal/search/file_indexer.go` + `internal/search/search_engine.go` (turn `SearchFiles` into a shim over a shared `*Index`)

**Interfaces:**
- Consumes: `Store`, `File` (Task 1).
- Produces:
  - `type Index struct { store *Store; roots, exclude []string; embedder Embedder /* nil ok */ }`
  - `type Embedder interface { Embed(ctx context.Context, texts []string) ([][]float32, error) }` (impl in Task 7; nil allowed).
  - `func New(dbPath string, roots, exclude []string, embedder Embedder) (*Index, error)`
  - `func (ix *Index) Start(ctx context.Context)` — kicks off the initial scan in a goroutine (watcher added in Task 3).
  - `func (ix *Index) scan()` — walk roots (skip excludes + hidden + system), `store.Upsert` each entry (files and dirs), then reconcile-delete rows whose path no longer exists.
  - `func (ix *Index) Search(query string, kind Kind) []FileRecord` (stub returns `store.SearchFTS`-mapped results for now; full pipeline in Task 4). `type Kind int; const (KindAny Kind=iota; KindFile; KindFolder)`.
  - `type FileRecord struct { Path, Name string }` (mirror search.FileRecord).

- [ ] **Step 1: Write the failing test** — add to `internal/fileindex/fileindex_test.go`

```go
package fileindex

import (
	"os"
	"path/filepath"
	"testing"
)

func TestScanIndexesFiles(t *testing.T) {
	root := t.TempDir()
	os.WriteFile(filepath.Join(root, "Resume_2026.pdf"), []byte("x"), 0o644)
	os.MkdirAll(filepath.Join(root, "node_modules"), 0o755)
	os.WriteFile(filepath.Join(root, "node_modules", "junk.js"), []byte("x"), 0o644)

	ix, err := New(filepath.Join(t.TempDir(), "idx.db"), []string{root}, []string{"node_modules"}, nil)
	if err != nil { t.Fatal(err) }
	ix.scan()

	res := ix.Search("resume", KindAny)
	if len(res) == 0 || filepath.Base(res[0].Path) != "Resume_2026.pdf" { t.Fatalf("scan/search: %+v", res) }
	if len(ix.Search("junk", KindAny)) != 0 { t.Fatal("excluded dir was indexed") }
}
```

- [ ] **Step 2: Run to verify it fails** — `... -run TestScanIndexesFiles` → FAIL `undefined: New`.

- [ ] **Step 3: Implement** `fileindex.go` (New/Start/Search stub/Kind/FileRecord) and `watcher.go`'s `scan()` (a `filepath.WalkDir` honoring `exclude` by dir name + the spec's system/hidden skips; upsert each; reconcile using `store.AllPaths()` vs seen). Then rewrite `internal/search`:
  - `search.SetIndex(*fileindex.Index)` package var set from main; `search.SearchFiles(query)` delegates to `ix.Search(query, KindAny)` mapping to `search.FileRecord{Path,Name}`; returns nil if the index isn't set yet.
  - Keep `search.FileRecord`/`SearchFiles`/`Ready()` names so `resolveFile`/`resolveFolder` compile unchanged.

- [ ] **Step 4: Run to verify it passes** — `... -run TestScan -v` PASS; also `go build -tags sqlite_fts5 ./internal/...` compiles (search shim + callers).

- [ ] **Step 5: Commit**

```bash
git add internal/fileindex/fileindex.go internal/fileindex/watcher.go internal/search/file_indexer.go internal/search/search_engine.go
git commit -m "feat(fileindex): initial scan + Index API; search delegates to fileindex"
```

---

## Task 3: Live fsnotify watcher (incremental)

**Files:**
- Modify: `internal/fileindex/watcher.go`; Test: `internal/fileindex/watcher_test.go`

**Interfaces:**
- Consumes: `Index`, `Store`.
- Produces: `func (ix *Index) watch(ctx context.Context)` — a fsnotify watcher over roots (recursively add subdirs), translating events to `store.Upsert` (create/write, if excluded skip), `store.DeleteByPath` (remove/rename-away). Called from `Start` after `scan()`.

- [ ] **Step 1: Write the failing test**

```go
func TestWatchPicksUpNewFile(t *testing.T) {
	root := t.TempDir()
	ix, _ := New(filepath.Join(t.TempDir(), "idx.db"), []string{root}, nil, nil)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ix.scan()
	go ix.watch(ctx)
	time.Sleep(150 * time.Millisecond) // let the watcher register

	os.WriteFile(filepath.Join(root, "notes.md"), []byte("x"), 0o644)
	// poll up to 2s for the event to land
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if len(ix.Search("notes", KindAny)) > 0 { return }
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatal("new file never indexed by watcher")
}
```
(imports: `context`, `os`, `path/filepath`, `testing`, `time`.)

- [ ] **Step 2: Run to verify it fails** — `undefined: watch` (or no indexing) → FAIL.

- [ ] **Step 3: Implement** `watch`: create `fsnotify.NewWatcher()`, add each root and (walk) its subdirs; on `Create` of a dir, `watcher.Add` it; on Create/Write of a file → build a `File` (stat) and `Upsert` unless excluded; on Remove/Rename → `DeleteByPath`. Debounce rapid Write bursts (coalesce ~300 ms per path). Stop on `ctx.Done()`. Wire `Start` to `go ix.watch(ctx)` after `scan()`.

- [ ] **Step 4: Run to verify it passes** — `... -run TestWatch -v` PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/fileindex/watcher.go internal/fileindex/watcher_test.go
git commit -m "feat(fileindex): live fsnotify watcher with incremental upsert/delete"
```

---

## Task 4: Ranking pipeline + Search/Resolve

**Files:**
- Create: `internal/fileindex/rank.go`, `internal/fileindex/rank_test.go`
- Modify: `internal/fileindex/fileindex.go` (real `Search` + `Resolve`); `internal/tools/open_file.go` + `internal/tools/open_explorer.go` (delegate resolve — already via `search.SearchFiles`, verify unchanged behavior)

**Interfaces:**
- Produces:
  - `type Candidate struct { File File; TextMatch, AliasMatch float64 }`
  - `func rankScore(c Candidate, now int64) float64` — `0.40*c.TextMatch + 0.25*recency(c.File.ModifiedAt,now) + 0.20*usageNorm(c.File.UsageScore) + 0.15*c.AliasMatch`, with `recency` = exp-decay (helper `recency(mod, now)` returns 1.0 today → ~0 at >2y) and `usageNorm` = `min(1, score/10)`.
  - `func (ix *Index) Resolve(query string, kind Kind) (string, bool)` — best path or false.

- [ ] **Step 1: Write the failing test** — `rank_test.go`

```go
package fileindex

import "testing"

func TestLatestResumeWins(t *testing.T) {
	now := int64(1_700_000_000)
	day := int64(86400)
	newer := Candidate{File: File{Name: "Resume_2026.pdf", ModifiedAt: now - day}, TextMatch: 0.9}
	older := Candidate{File: File{Name: "Resume_old.pdf",  ModifiedAt: now - 800*day}, TextMatch: 0.9}
	if rankScore(newer, now) <= rankScore(older, now) {
		t.Fatalf("recent resume should outrank old: %f vs %f", rankScore(newer, now), rankScore(older, now))
	}
}

func TestUsageAndAliasBoost(t *testing.T) {
	now := int64(1_700_000_000)
	plain  := Candidate{File: File{Name: "cv.pdf", ModifiedAt: now}, TextMatch: 0.8}
	used   := Candidate{File: File{Name: "cv.pdf", ModifiedAt: now, UsageScore: 10}, TextMatch: 0.8}
	if used2 := rankScore(used, now); used2 <= rankScore(plain, now) { t.Fatalf("usage should boost: %f", used2) }
	aliased := Candidate{File: File{Name: "cv.pdf", ModifiedAt: now}, TextMatch: 0.8, AliasMatch: 1}
	if rankScore(aliased, now) <= rankScore(plain, now) { t.Fatal("alias should boost") }
}
```

- [ ] **Step 2: Run to verify it fails** — `undefined: rankScore` → FAIL.

- [ ] **Step 3: Implement** `rank.go` (pure `rankScore` + `recency`/`usageNorm` helpers) and wire `Index.Search`: get `store.SearchFTS(query, 50)` candidates, compute `TextMatch` (exact name=1.0 > prefix=0.8 > token=0.6 > fuzzy=0.4 via a simple `strings`/Levenshtein-lite helper), set `AliasMatch` (0 until Task 5), filter by `kind` (IsDir), `rankScore`-sort, map to `[]FileRecord`. `Resolve` returns the top path if its score clears `0.35`.

- [ ] **Step 4: Run to verify it passes** — `... -run "TestLatestResume|TestUsageAndAlias" -v` PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/fileindex/rank.go internal/fileindex/rank_test.go internal/fileindex/fileindex.go
git commit -m "feat(fileindex): ranked search pipeline (text+recency+usage+alias) + Resolve"
```

---

## Task 5: Aliases + hot cache + usage + explicit memory + tools

**Files:**
- Create: `internal/fileindex/alias.go`, `internal/fileindex/hotcache.go`, `internal/fileindex/alias_test.go`
- Modify: `internal/fileindex/fileindex.go` (`RecordOpen`, `Remember`, alias-aware Search), `internal/fileindex/watcher.go` (derive keywords/aliases on upsert)
- Create: `internal/tools/find_file.go`, `internal/tools/remember_file.go`
- Modify: `internal/tools/registry.go` (register both), `internal/tools/open_file.go` (call `RecordOpen`)

**Interfaces:**
- Produces:
  - `func deriveAliases(name string) []string` — tokenize filename (split camelCase/snake/kebab/space, strip date/version noise, lowercase) + keyword map (`resume`→{resume,cv,job}, `invoice`→{invoice,bill}, `budget`→{budget,finance}). Returns distinct aliases; also used as the `keywords` FTS column.
  - `func (ix *Index) RecordOpen(path string)` — bump `file_usage`, set `last_opened`, recompute `usage_score`, refresh hot cache.
  - `func (ix *Index) Remember(key, path string) error` — upsert `file_memory`.
  - hot cache: `type hotCache struct{...}` LRU + `alias→path` map; `Search`/`Resolve` check it first (exact normalized key → memory/alias path, existence-checked).
  - tool `FindFileTool` (`find_file`, params `{"query":"string"}`) → returns/opens best path; `RememberFileTool` (`remember_file`, params `{"key":"string","query":"string"}`) → resolves query to a path and `Remember(key,path)`.

- [ ] **Step 1: Write the failing test** — `alias_test.go`

```go
package fileindex

import ("slices"; "testing")

func TestDeriveAliases(t *testing.T) {
	a := deriveAliases("Resume_Eshwar_2026.pdf")
	for _, want := range []string{"resume", "eshwar", "cv"} {
		if !slices.Contains(a, want) { t.Errorf("aliases %v missing %q", a, want) }
	}
	b := deriveAliases("voiceAgentNotes.md")
	if !slices.Contains(b, "voice") || !slices.Contains(b, "agent") { t.Errorf("camelCase split failed: %v", b) }
}
```

- [ ] **Step 2: Run to verify it fails** — `undefined: deriveAliases` → FAIL.

- [ ] **Step 3: Implement** `alias.go` (`deriveAliases`), `hotcache.go` (LRU + alias/memory map, rebuilt from SQLite on `New`), the `RecordOpen`/`Remember` methods, populate `aliases` table + FTS `keywords` in `Upsert` (watcher), and set `AliasMatch=1` in Search when the query hits a stored alias. Create the two tools (mirror an existing tool's struct shape); `find_file` calls `Resolve` then `RecordOpen`; register in `registry.go`; `open_file` calls `ix.RecordOpen(path)` on success (via the `search`/fileindex handle).

- [ ] **Step 4: Run to verify it passes** — `... -run TestDeriveAliases -v` PASS; `go build -tags sqlite_fts5 ./internal/...` OK.

- [ ] **Step 5: Commit**

```bash
git add internal/fileindex/alias.go internal/fileindex/hotcache.go internal/fileindex/alias_test.go internal/fileindex/fileindex.go internal/fileindex/watcher.go internal/tools/find_file.go internal/tools/remember_file.go internal/tools/registry.go internal/tools/open_file.go
git commit -m "feat(fileindex): heuristic aliases, hot cache, usage learning, explicit memory + tools"
```

---

## Task 6: Content extraction

**Files:**
- Create: `internal/fileindex/content.go`, `internal/fileindex/content_test.go`

**Interfaces:**
- Produces: `func extractText(path string) (string, bool)` — returns text + ok. txt/md/code/json/csv: read + cap (~1 MB). pdf: `github.com/ledongthuc/pdf`. docx/pptx/xlsx: `archive/zip` → read `word/document.xml`/`ppt/slides/*.xml`/`xl/sharedStrings.xml`, strip tags via `encoding/xml`. Non-whitelisted ext → `("", false)`. Also `func isEmbeddable(ext string) bool` (the whitelist).

- [ ] **Step 1: Write the failing test** — `content_test.go`

```go
package fileindex

import ("os"; "path/filepath"; "strings"; "testing")

func TestExtractTextPlain(t *testing.T) {
	p := filepath.Join(t.TempDir(), "note.md")
	os.WriteFile(p, []byte("# Startup idea\nA voice assistant business."), 0o644)
	txt, ok := extractText(p)
	if !ok || !strings.Contains(txt, "Startup idea") { t.Fatalf("extract md: %q ok=%v", txt, ok) }
}
func TestExtractRejectsBinary(t *testing.T) {
	p := filepath.Join(t.TempDir(), "a.exe")
	os.WriteFile(p, []byte{0,1,2}, 0o644)
	if _, ok := extractText(p); ok { t.Fatal("exe should not be embeddable") }
	if isEmbeddable("dll") { t.Fatal("dll should not be embeddable") }
}
```

- [ ] **Step 2: Run to verify it fails** — `undefined: extractText` → FAIL.

- [ ] **Step 3: Implement** `content.go` (whitelist + per-type extraction; `go get github.com/ledongthuc/pdf` for pdf). docx/pptx/xlsx via zip+xml. Cap output length.

- [ ] **Step 4: Run to verify it passes** — `... -run TestExtract -v` PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/fileindex/content.go internal/fileindex/content_test.go go.mod go.sum
git commit -m "feat(fileindex): lazy text extraction (txt/md/code, pdf, docx/pptx/xlsx)"
```

---

## Task 7: BGE ONNX embedder + embed cache + semantic fallback

**Files:**
- Create: `internal/fileindex/embed.go`, `internal/fileindex/bge.go`, `internal/fileindex/embed_test.go`
- Modify: `internal/fileindex/fileindex.go` (Tier-3 fallback in `Search`; lazy embed on `RecordOpen`)

**Interfaces:**
- Produces:
  - `func float16Encode([]float32) []byte` / `func float16Decode([]byte) []float32` — IEEE half round-trip.
  - `func cosine(a, b []float32) float64`
  - `func (s *Store) PutVector(hash string, dims int, vec []float32, model string) error` / `GetVector(hash) ([]float32, bool, error)` / `AllVectors() (map[string][]float32, error)` (float16 in the `vec` blob).
  - `type BGEEmbedder struct{...}`; `func NewBGEEmbedder(modelPath, vocabPath string) (*BGEEmbedder, error)` (nil,err if files/runtime absent); `Embed(ctx, texts) ([][]float32, error)` — WordPiece tokenize (from `vocab.txt`), onnxruntime session run, mean-pool, L2-normalize; query texts get the BGE retrieval prefix.

- [ ] **Step 1: Write the failing test** — `embed_test.go` (pure parts only; ONNX skipped when model absent)

```go
package fileindex

import ("math"; "testing")

func TestFloat16RoundTrip(t *testing.T) {
	in := []float32{0, 1, -1, 0.5, 0.123, -2.75}
	out := float16Decode(float16Encode(in))
	for i := range in {
		if math.Abs(float64(in[i]-out[i])) > 0.01 { t.Fatalf("f16 drift at %d: %v vs %v", i, in[i], out[i]) }
	}
}
func TestCosine(t *testing.T) {
	if c := cosine([]float32{1,0}, []float32{1,0}); math.Abs(c-1) > 1e-6 { t.Fatalf("parallel=%f", c) }
	if c := cosine([]float32{1,0}, []float32{0,1}); math.Abs(c) > 1e-6 { t.Fatalf("orthogonal=%f", c) }
}
```

- [ ] **Step 2: Run to verify it fails** — `undefined: float16Encode` → FAIL.

- [ ] **Step 3: Implement** `embed.go` (float16 codec, cosine, Store vector CRUD) and `bge.go` (onnxruntime_go: `go get github.com/yalue/onnxruntime_go`; a minimal WordPiece tokenizer reading `vocab.txt`; session run with input ids/mask; mean-pool over last_hidden_state using the mask; L2-normalize). Wire Tier-3 into `Search`: only if Tiers 1–2 return nothing confident AND `ix.embedder != nil` — embed the query, load candidate vectors (`store.AllVectors`), cosine-rank; lazily embed a bounded set of likely files first (extract text via Task 6, embed, `PutVector` keyed by content_hash). `RecordOpen` triggers a lazy embed of that file (throttled goroutine). Guard everything so a nil embedder / missing model is a clean no-op.

- [ ] **Step 4: Run to verify it passes** — `... -run "TestFloat16|TestCosine" -v` PASS. (BGE inference verified manually with the model present.)

- [ ] **Step 5: Commit**

```bash
git add internal/fileindex/embed.go internal/fileindex/bge.go internal/fileindex/embed_test.go internal/fileindex/fileindex.go go.mod go.sum
git commit -m "feat(fileindex): local ONNX BGE-small embedder + float16 vector cache + semantic fallback"
```

---

## Task 8: Config, main wiring, build tags, docs

**Files:**
- Modify: `config/config.go` (fields + defaults), `cmd/app/main.go` (construct + Start the index, build embedder, replace `search.InitIndexer`), `internal/search/*` (SetIndex call site), `.gitignore` (models/, fileindex.db), `README.md` / `CLAUDE.md` / `docs/BUILD-VOICE.md` (build tag + model setup)

**Interfaces:**
- Consumes: everything above.

- [ ] **Step 1: Write the failing test** — `config/config_test.go` (defaults)

```go
func TestFileIndexDefaults(t *testing.T) {
	cfg, err := loadFromBytes([]byte(`{}`))
	if err != nil { t.Fatal(err) }
	if !cfg.SemanticSearch { t.Error("semantic_search should default true") }
	if len(cfg.IndexRoots) == 0 { t.Error("index_roots should have defaults") }
}
```

- [ ] **Step 2: Run to verify it fails** — undefined field → FAIL.

- [ ] **Step 3: Implement** config fields (`IndexRoots []string json:"index_roots"`, `IndexExclude []string json:"index_exclude"`, `SemanticSearch bool json:"semantic_search"` default true, `BGEModelPath`, `BGEVocabPath`) with defaults (roots: Documents/Desktop/Downloads/Projects; excludes merged with built-ins). In `main.go`: resolve roots under home, build `emb, _ := fileindex.NewBGEEmbedder(cfg.BGEModelPath, cfg.BGEVocabPath)` (nil on error when `!SemanticSearch` or files missing), `ix, _ := fileindex.New("fileindex.db", roots, exclude, emb)`, `search.SetIndex(ix)`, `ix.Start(rootCtx)` — replacing `search.InitIndexer(...)`. Add `models/` and `fileindex.db*` to `.gitignore`. Document the `sqlite_fts5` tag in both build commands and the BGE model/onnxruntime setup.

- [ ] **Step 4: Verify build + tests + full voice build**

Run: `export PATH="$PATH:/c/w64devkit/bin" && go test -tags sqlite_fts5 ./internal/fileindex/... ./config/... && go build -tags "whisper sqlite_fts5" -ldflags="-s -w -H windowsgui" -o voice-agent.exe ./cmd/app`
Expected: tests PASS; voice build links.

- [ ] **Step 5: Commit**

```bash
git add config/config.go config/config_test.go cmd/app/main.go internal/search/file_indexer.go .gitignore README.md CLAUDE.md docs/BUILD-VOICE.md
git commit -m "feat(fileindex): config + main wiring + sqlite_fts5 build tag + docs"
```

---

## Self-Review notes (author)

- **Spec coverage:** Tier2 store+FTS (T1), scan+retire search (T2), watcher (T3), rank pipeline (T4), aliases/hotcache/usage/memory/tools (T5), content extraction (T6), BGE ONNX + vector cache + semantic fallback (T7), config/wiring/build/docs (T8). Success criteria 1–7 map to tasks.
- **Type consistency:** `File`, `FileRecord{Path,Name}`, `Kind`, `Candidate`, `rankScore`, `Embedder.Embed`, `deriveAliases`, `RecordOpen`, `Remember`, `extractText`/`isEmbeddable`, `float16Encode/Decode`, `cosine`, `PutVector/GetVector/AllVectors`, `NewBGEEmbedder`, `New`/`Start`/`Search`/`Resolve` — used consistently across tasks.
- **No placeholders:** pure seams (store CRUD, scan, rank, aliases, content, float16/cosine) have real test code + implementations; ONNX inference and fsnotify are glue with concrete library calls and are manual/temp-dir verified. Every go command carries `-tags sqlite_fts5`.
- **Parallelism note (for the executor):** T1→T2→T3/T4 are somewhat sequential (shared `fileindex.go`); T6 (content) is independent of T3–T5 and can be authored in parallel; T7 depends on T6; T8 last. Files within `internal/fileindex` share the package (compile-coupled), so parallel authoring must not run `go test` concurrently.
```
