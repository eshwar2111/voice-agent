// This file implements the in-process ONNX BGE-small embedder (Task 7 of
// docs/superpowers/plans/2026-09-03-file-intelligence-index.md): a small
// WordPiece tokenizer reading the model's vocab.txt, an onnxruntime session
// producing last_hidden_state, mean-pooling over the attention mask, and L2
// normalization -> 384-d unit vectors.
//
// NewBGEEmbedder returns (nil, err) whenever the model file, the vocab file,
// or the onnxruntime shared library is absent — it never panics — so the
// semantic tier disables cleanly and Tiers 1-2 keep working.
package fileindex

import (
	"bufio"
	"context"
	"fmt"
	"math"
	"os"
	"strings"
	"sync"
	"unicode"

	ort "github.com/yalue/onnxruntime_go"
)

// bgeQueryPrefix is the retrieval instruction bge-small-en-v1.5 expects on the
// query side (documents are embedded as-is). Applied by the semantic tier
// before embedding a query.
const bgeQueryPrefix = "Represent this sentence for searching relevant passages: "

// bgeMaxTokens caps sequence length (model context is 512; filenames/snippets
// are short, so this is plenty and bounds inference cost).
const bgeMaxTokens = 512

// ortEnvOnce guards one-time onnxruntime environment initialization across all
// embedder instances.
var ortEnvOnce sync.Once
var ortEnvErr error

// BGEEmbedder is an in-process ONNX embedder implementing the Embedder
// interface. All inference is serialized by mu (the ORT session is not assumed
// goroutine-safe).
type BGEEmbedder struct {
	mu      sync.Mutex
	session *ort.DynamicAdvancedSession
	vocab   map[string]int64
	unkID   int64
	clsID   int64
	sepID   int64
	dims    int
	model   string
}

// NewBGEEmbedder loads the WordPiece vocab and the ONNX model, initializing the
// onnxruntime environment on first use. If the vocab, the model, or the runtime
// shared library is missing it returns (nil, err) — callers treat a nil
// embedder as "semantic disabled".
func NewBGEEmbedder(modelPath, vocabPath string) (*BGEEmbedder, error) {
	if strings.TrimSpace(modelPath) == "" || strings.TrimSpace(vocabPath) == "" {
		return nil, fmt.Errorf("fileindex: BGE model/vocab path not configured")
	}
	if _, err := os.Stat(modelPath); err != nil {
		return nil, fmt.Errorf("fileindex: BGE model not found: %w", err)
	}
	vocab, err := loadVocab(vocabPath)
	if err != nil {
		return nil, err
	}

	ortEnvOnce.Do(func() {
		// SetSharedLibraryPath honors an explicit override; otherwise the
		// binding's platform default is used. InitializeEnvironment fails
		// cleanly if the native library is absent.
		if p := os.Getenv("ONNXRUNTIME_LIB_PATH"); p != "" {
			ort.SetSharedLibraryPath(p)
		}
		ortEnvErr = ort.InitializeEnvironment()
	})
	if ortEnvErr != nil {
		return nil, fmt.Errorf("fileindex: onnxruntime unavailable: %w", ortEnvErr)
	}

	session, err := ort.NewDynamicAdvancedSession(
		modelPath,
		[]string{"input_ids", "attention_mask", "token_type_ids"},
		[]string{"last_hidden_state"},
		nil,
	)
	if err != nil {
		return nil, fmt.Errorf("fileindex: create ONNX session: %w", err)
	}

	e := &BGEEmbedder{
		session: session,
		vocab:   vocab,
		unkID:   vocabID(vocab, "[UNK]", 100),
		clsID:   vocabID(vocab, "[CLS]", 101),
		sepID:   vocabID(vocab, "[SEP]", 102),
		dims:    384,
		model:   "bge-small-en-v1.5",
	}
	return e, nil
}

// Close releases the ONNX session.
func (e *BGEEmbedder) Close() error {
	if e == nil || e.session == nil {
		return nil
	}
	return e.session.Destroy()
}

// Embed tokenizes each text, runs the model, mean-pools last_hidden_state over
// the attention mask, and L2-normalizes, returning one 384-d unit vector per
// input. Inputs are padded to the longest sequence in the batch.
func (e *BGEEmbedder) Embed(ctx context.Context, texts []string) ([][]float32, error) {
	if e == nil || e.session == nil {
		return nil, fmt.Errorf("fileindex: embedder not initialized")
	}
	if len(texts) == 0 {
		return nil, nil
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	e.mu.Lock()
	defer e.mu.Unlock()

	// Tokenize and find the padded length.
	tokenized := make([][]int64, len(texts))
	maxLen := 1
	for i, t := range texts {
		ids := e.encode(t)
		tokenized[i] = ids
		if len(ids) > maxLen {
			maxLen = len(ids)
		}
	}

	batch := int64(len(texts))
	seq := int64(maxLen)
	inputIDs := make([]int64, batch*seq)
	attnMask := make([]int64, batch*seq)
	tokenTypes := make([]int64, batch*seq) // all zeros

	for i, ids := range tokenized {
		off := int64(i) * seq
		for j, id := range ids {
			inputIDs[off+int64(j)] = id
			attnMask[off+int64(j)] = 1
		}
	}

	shape := ort.NewShape(batch, seq)
	idsTensor, err := ort.NewTensor(shape, inputIDs)
	if err != nil {
		return nil, fmt.Errorf("fileindex: input_ids tensor: %w", err)
	}
	defer idsTensor.Destroy()
	maskTensor, err := ort.NewTensor(shape, attnMask)
	if err != nil {
		return nil, fmt.Errorf("fileindex: attention_mask tensor: %w", err)
	}
	defer maskTensor.Destroy()
	typeTensor, err := ort.NewTensor(shape, tokenTypes)
	if err != nil {
		return nil, fmt.Errorf("fileindex: token_type_ids tensor: %w", err)
	}
	defer typeTensor.Destroy()

	// Output: [batch, seq, hidden]. Let the binding allocate it.
	outShape := ort.NewShape(batch, seq, int64(e.dims))
	outTensor, err := ort.NewEmptyTensor[float32](outShape)
	if err != nil {
		return nil, fmt.Errorf("fileindex: output tensor: %w", err)
	}
	defer outTensor.Destroy()

	if err := e.session.Run(
		[]ort.Value{idsTensor, maskTensor, typeTensor},
		[]ort.Value{outTensor},
	); err != nil {
		return nil, fmt.Errorf("fileindex: ONNX run: %w", err)
	}

	hidden := outTensor.GetData() // len = batch*seq*dims
	out := make([][]float32, batch)
	for i := int64(0); i < batch; i++ {
		vec := make([]float32, e.dims)
		var count float64
		for j := int64(0); j < seq; j++ {
			if attnMask[i*seq+j] == 0 {
				continue
			}
			count++
			base := (i*seq + j) * int64(e.dims)
			for d := 0; d < e.dims; d++ {
				vec[d] += hidden[base+int64(d)]
			}
		}
		if count > 0 {
			inv := float32(1.0 / count)
			for d := 0; d < e.dims; d++ {
				vec[d] *= inv
			}
		}
		l2Normalize(vec)
		out[i] = vec
	}
	return out, nil
}

// l2Normalize scales v to unit length in place (no-op for a zero vector).
func l2Normalize(v []float32) {
	var sum float64
	for _, x := range v {
		sum += float64(x) * float64(x)
	}
	if sum == 0 {
		return
	}
	inv := float32(1.0 / math.Sqrt(sum))
	for i := range v {
		v[i] *= inv
	}
}

// encode turns text into WordPiece token ids wrapped with [CLS]/[SEP], capped
// to bgeMaxTokens.
func (e *BGEEmbedder) encode(text string) []int64 {
	pieces := wordPieceTokenize(basicTokenize(text), e.vocab, e.unkID)
	// Reserve room for [CLS] and [SEP].
	if len(pieces) > bgeMaxTokens-2 {
		pieces = pieces[:bgeMaxTokens-2]
	}
	ids := make([]int64, 0, len(pieces)+2)
	ids = append(ids, e.clsID)
	ids = append(ids, pieces...)
	ids = append(ids, e.sepID)
	return ids
}

// basicTokenize lowercases and splits text into word/punctuation tokens
// (BERT-style: whitespace split, then each punctuation char is its own token).
func basicTokenize(text string) []string {
	text = strings.ToLower(text)
	var tokens []string
	var cur strings.Builder
	flush := func() {
		if cur.Len() > 0 {
			tokens = append(tokens, cur.String())
			cur.Reset()
		}
	}
	for _, r := range text {
		switch {
		case unicode.IsSpace(r):
			flush()
		case isPunct(r):
			flush()
			tokens = append(tokens, string(r))
		default:
			cur.WriteRune(r)
		}
	}
	flush()
	return tokens
}

func isPunct(r rune) bool {
	if unicode.IsPunct(r) || unicode.IsSymbol(r) {
		return true
	}
	// ASCII punctuation ranges not always flagged by unicode.IsPunct.
	switch {
	case r >= '!' && r <= '/':
		return true
	case r >= ':' && r <= '@':
		return true
	case r >= '[' && r <= '`':
		return true
	case r >= '{' && r <= '~':
		return true
	}
	return false
}

// wordPieceTokenize applies greedy longest-match-first WordPiece to each basic
// token, mapping to ids from vocab, emitting unkID for tokens no prefix covers.
func wordPieceTokenize(tokens []string, vocab map[string]int64, unkID int64) []int64 {
	var out []int64
	for _, tok := range tokens {
		runes := []rune(tok)
		if len(runes) == 0 {
			continue
		}
		start := 0
		var subIDs []int64
		bad := false
		for start < len(runes) {
			end := len(runes)
			var curID int64 = -1
			for end > start {
				sub := string(runes[start:end])
				if start > 0 {
					sub = "##" + sub
				}
				if id, ok := vocab[sub]; ok {
					curID = id
					break
				}
				end--
			}
			if curID < 0 {
				bad = true
				break
			}
			subIDs = append(subIDs, curID)
			start = end
		}
		if bad {
			out = append(out, unkID)
			continue
		}
		out = append(out, subIDs...)
	}
	return out
}

// loadVocab reads a WordPiece vocab.txt (one token per line; line index is the
// token id).
func loadVocab(path string) (map[string]int64, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("fileindex: BGE vocab not found: %w", err)
	}
	defer f.Close()

	vocab := make(map[string]int64)
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	var id int64
	for sc.Scan() {
		tok := strings.TrimRight(sc.Text(), "\r\n")
		if _, exists := vocab[tok]; !exists {
			vocab[tok] = id
		}
		id++
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("fileindex: read vocab: %w", err)
	}
	if len(vocab) == 0 {
		return nil, fmt.Errorf("fileindex: empty vocab %s", path)
	}
	return vocab, nil
}

// vocabID looks up tok, returning def if absent.
func vocabID(vocab map[string]int64, tok string, def int64) int64 {
	if id, ok := vocab[tok]; ok {
		return id
	}
	return def
}
