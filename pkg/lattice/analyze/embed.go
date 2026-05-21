package analyze

import (
	"math"
	"sort"
	"strings"
)

// Embedder turns text into a vector for similarity comparison.
//
// The production design embeds with a bundled ONNX sentence-transformer
// model. LexicalEmbedder is the dependency-free default: it needs no native
// runtime and no model file, and it is fully deterministic. An ONNX-backed
// Embedder can be slotted in without changing the semantic checks.
type Embedder interface {
	Embed(text string) []float64
}

// LexicalEmbedder produces a normalized term-frequency vector over a fixed
// hashed dimension. Similarity is cosine over these vectors.
type LexicalEmbedder struct {
	dims int
}

// NewLexicalEmbedder returns a lexical embedder.
func NewLexicalEmbedder() *LexicalEmbedder { return &LexicalEmbedder{dims: 512} }

// Embed returns a normalized hashed term-frequency vector.
func (e *LexicalEmbedder) Embed(text string) []float64 {
	vec := make([]float64, e.dims)
	for _, tok := range tokenize(text) {
		vec[hashToken(tok)%uint32(e.dims)]++
	}
	norm := 0.0
	for _, v := range vec {
		norm += v * v
	}
	if norm == 0 {
		return vec
	}
	norm = math.Sqrt(norm)
	for i := range vec {
		vec[i] /= norm
	}
	return vec
}

// Cosine returns the cosine similarity of two equal-length vectors.
func Cosine(a, b []float64) float64 {
	if len(a) != len(b) {
		return 0
	}
	var dot float64
	for i := range a {
		dot += a[i] * b[i]
	}
	if dot < 0 {
		return 0
	}
	if dot > 1 {
		return 1
	}
	return dot
}

// stopwords are common terms excluded from lexical embedding.
var stopwords = map[string]bool{
	"the": true, "a": true, "an": true, "of": true, "to": true, "and": true,
	"or": true, "is": true, "are": true, "be": true, "for": true, "in": true,
	"on": true, "with": true, "that": true, "this": true, "it": true, "as": true,
	"by": true, "from": true, "must": true, "not": true, "can": true, "will": true,
}

// tokenize lowercases, splits on non-alphanumerics, and drops stopwords.
func tokenize(text string) []string {
	var toks []string
	var cur strings.Builder
	flush := func() {
		if cur.Len() == 0 {
			return
		}
		t := cur.String()
		cur.Reset()
		if len(t) > 1 && !stopwords[t] {
			toks = append(toks, t)
		}
	}
	for _, r := range strings.ToLower(text) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			cur.WriteRune(r)
		} else {
			flush()
		}
	}
	flush()
	sort.Strings(toks)
	return toks
}

// hashToken is a stable FNV-1a hash of a token.
func hashToken(s string) uint32 {
	var h uint32 = 2166136261
	for i := 0; i < len(s); i++ {
		h ^= uint32(s[i])
		h *= 16777619
	}
	return h
}
