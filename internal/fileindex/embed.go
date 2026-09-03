// This file implements the semantic-tier support code (Task 7 of
// docs/superpowers/plans/2026-09-03-file-intelligence-index.md): an IEEE
// half-precision (float16) codec for compact vector storage, cosine
// similarity, a content-hash helper, and the SQLite vector cache CRUD
// (PutVector/GetVector/AllVectors). The BGE ONNX embedder lives in bge.go;
// this file is pure Go (no ONNX runtime) so its tests always run.
package fileindex

import (
	"crypto/sha256"
	"database/sql"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"math"
)

// float16Encode packs a slice of float32 into little-endian IEEE half-precision
// (2 bytes per value). Used to store 384-d BGE vectors at 768 B each.
func float16Encode(v []float32) []byte {
	out := make([]byte, len(v)*2)
	for i, f := range v {
		binary.LittleEndian.PutUint16(out[i*2:], float32ToFloat16bits(f))
	}
	return out
}

// float16Decode is the inverse of float16Encode. A trailing odd byte (should
// never happen for well-formed blobs) is ignored.
func float16Decode(b []byte) []float32 {
	n := len(b) / 2
	out := make([]float32, n)
	for i := 0; i < n; i++ {
		out[i] = float16bitsToFloat32(binary.LittleEndian.Uint16(b[i*2:]))
	}
	return out
}

// float32ToFloat16bits converts an IEEE-754 single to the bit pattern of an
// IEEE-754 half, round-to-nearest. Subnormals, Inf and NaN are handled.
func float32ToFloat16bits(f float32) uint16 {
	x := math.Float32bits(f)
	sign := uint16((x >> 16) & 0x8000)
	mant := x & 0x007fffff
	exp := int32((x >> 23) & 0xff)

	if exp == 0xff { // Inf or NaN
		if mant != 0 {
			return sign | 0x7e00 // NaN
		}
		return sign | 0x7c00 // Inf
	}

	newExp := exp - 127 + 15
	if newExp >= 0x1f {
		return sign | 0x7c00 // overflow -> Inf
	}
	if newExp <= 0 {
		// Subnormal half, or underflow to zero.
		if newExp < -10 {
			return sign
		}
		mant |= 0x00800000 // restore implicit leading 1
		shift := uint32(14 - newExp)
		half := mant >> shift
		if (mant>>(shift-1))&1 == 1 { // round half up
			half++
		}
		return sign | uint16(half)
	}
	// Normal.
	half := sign | uint16(newExp<<10) | uint16(mant>>13)
	if mant&0x00001000 != 0 { // round half up (may carry into exponent, which is fine)
		half++
	}
	return half
}

// float16bitsToFloat32 converts the bit pattern of an IEEE-754 half to an
// IEEE-754 single.
func float16bitsToFloat32(h uint16) float32 {
	sign := uint32(h&0x8000) << 16
	exp := uint32(h>>10) & 0x1f
	mant := uint32(h & 0x03ff)

	switch exp {
	case 0:
		if mant == 0 {
			return math.Float32frombits(sign) // signed zero
		}
		// Subnormal: value = (-1)^s * 2^-14 * (mant/1024).
		val := float32(mant) / 1024.0 * float32(math.Exp2(-14))
		if sign != 0 {
			return -val
		}
		return val
	case 0x1f:
		if mant == 0 {
			return math.Float32frombits(sign | 0x7f800000) // Inf
		}
		return math.Float32frombits(sign | 0x7f800000 | (mant << 13)) // NaN
	default:
		newExp := (exp - 15 + 127) << 23
		return math.Float32frombits(sign | newExp | (mant << 13))
	}
}

// cosine returns the cosine similarity of a and b (0 if either is zero-length,
// mismatched, or has zero magnitude). Range [-1, 1].
func cosine(a, b []float32) float64 {
	if len(a) == 0 || len(a) != len(b) {
		return 0
	}
	var dot, na, nb float64
	for i := range a {
		x, y := float64(a[i]), float64(b[i])
		dot += x * y
		na += x * x
		nb += y * y
	}
	if na == 0 || nb == 0 {
		return 0
	}
	return dot / (math.Sqrt(na) * math.Sqrt(nb))
}

// contentHash returns a stable hex SHA-256 of text, used as the embedding cache
// key so identical file contents (copies) share one vector.
func contentHash(text string) string {
	sum := sha256.Sum256([]byte(text))
	return hex.EncodeToString(sum[:])
}

// PutVector stores (or replaces) the embedding for a content hash as a float16
// blob keyed by content_hash.
func (s *Store) PutVector(hash string, dims int, vec []float32, model string) error {
	if hash == "" || len(vec) == 0 {
		return fmt.Errorf("fileindex: put vector needs a hash and a vector")
	}
	blob := float16Encode(vec)
	if _, err := s.db.Exec(`
		INSERT INTO embeddings (content_hash, dims, vec, model, created_at)
		VALUES (?, ?, ?, ?, ?)
		ON CONFLICT(content_hash) DO UPDATE SET
			dims=excluded.dims,
			vec=excluded.vec,
			model=excluded.model,
			created_at=excluded.created_at`,
		hash, dims, blob, model, nowUnix()); err != nil {
		return fmt.Errorf("fileindex: put vector: %w", err)
	}
	return nil
}

// GetVector returns the cached embedding for a content hash, decoded from
// float16, if present.
func (s *Store) GetVector(hash string) ([]float32, bool, error) {
	var blob []byte
	err := s.db.QueryRow(`SELECT vec FROM embeddings WHERE content_hash = ?`, hash).Scan(&blob)
	if err == sql.ErrNoRows {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, fmt.Errorf("fileindex: get vector: %w", err)
	}
	return float16Decode(blob), true, nil
}

// AllVectors returns every cached embedding as content_hash -> vector, for the
// brute-force cosine pass in the semantic tier.
func (s *Store) AllVectors() (map[string][]float32, error) {
	rows, err := s.db.Query(`SELECT content_hash, vec FROM embeddings`)
	if err != nil {
		return nil, fmt.Errorf("fileindex: all vectors: %w", err)
	}
	defer rows.Close()

	out := make(map[string][]float32)
	for rows.Next() {
		var hash string
		var blob []byte
		if err := rows.Scan(&hash, &blob); err != nil {
			return nil, fmt.Errorf("fileindex: scan vector: %w", err)
		}
		out[hash] = float16Decode(blob)
	}
	return out, rows.Err()
}

// SetContentHash records the content hash for a file path (set lazily when the
// file's content is first read/embedded), so future embeds dedup by hash.
func (s *Store) SetContentHash(path, hash string) error {
	if _, err := s.db.Exec(`UPDATE files SET content_hash = ? WHERE path = ?`, hash, path); err != nil {
		return fmt.Errorf("fileindex: set content hash: %w", err)
	}
	return nil
}

// RecentFiles returns up to limit non-directory files ordered most-recently-
// modified first, used to bound the set of likely candidates the semantic tier
// lazily embeds on a metadata miss.
func (s *Store) RecentFiles(limit int) ([]File, error) {
	rows, err := s.db.Query(`
		SELECT id, path, name, ext, parent, is_dir, size, created_at, modified_at, last_accessed, content_hash, usage_score
		FROM files
		WHERE is_dir = 0
		ORDER BY usage_score DESC, modified_at DESC
		LIMIT ?`, limit)
	if err != nil {
		return nil, fmt.Errorf("fileindex: recent files: %w", err)
	}
	defer rows.Close()

	var out []File
	for rows.Next() {
		f, serr := scanFile(rows)
		if serr != nil {
			return nil, serr
		}
		out = append(out, f)
	}
	return out, rows.Err()
}
