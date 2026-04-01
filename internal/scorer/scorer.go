// Package scorer ranks memories by blending semantic similarity with
// temporal decay, importance weighting, and access frequency.
package scorer

import (
	"math"
	"sort"
	"time"

	"github.com/ramborogers/cyber-memory/internal/store"
)

const (
	// decayLambda controls how fast memories decay with age.
	// 0.01 gives a ~100-day half-life: score × exp(-0.01 × days).
	decayLambda = 0.01

	// accessWeight is the coefficient for the log-scaled access boost.
	accessWeight = 0.1
)

// Result is a memory with its composite score.
type Result struct {
	Memory *store.Memory
	Score  float64
}

// Score computes the composite score for a memory given a query embedding.
//
//	score = cosine_sim × recency × importance × access_boost
func Score(m *store.Memory, queryVec []float32, now time.Time) float64 {
	if len(m.Embedding) == 0 || len(queryVec) == 0 {
		return 0
	}
	cos := cosine(queryVec, m.Embedding)
	if cos <= 0 {
		return 0
	}
	rec := recency(m.CreatedAt, now)
	acc := accessBoost(m.AccessCount)
	return cos * rec * m.Importance * acc
}

// Rank scores all candidates, filters by minScore, and returns the top limit results.
func Rank(candidates []*store.Memory, queryVec []float32, limit int, minScore float64) []Result {
	now := time.Now().UTC()
	scored := make([]Result, 0, len(candidates))
	for _, m := range candidates {
		s := Score(m, queryVec, now)
		if s >= minScore {
			scored = append(scored, Result{Memory: m, Score: s})
		}
	}
	sort.Slice(scored, func(i, j int) bool {
		return scored[i].Score > scored[j].Score
	})
	if limit > 0 && len(scored) > limit {
		scored = scored[:limit]
	}
	return scored
}

// ---- math ----

// cosine computes the cosine similarity between two float32 vectors.
// Both vectors are assumed to be L2-normalised (from the embedding engine),
// so this reduces to a dot product — but we guard with the full formula anyway.
func cosine(a, b []float32) float64 {
	n := len(a)
	if len(b) < n {
		n = len(b)
	}
	var dot, normA, normB float64
	for i := 0; i < n; i++ {
		ai, bi := float64(a[i]), float64(b[i])
		dot += ai * bi
		normA += ai * ai
		normB += bi * bi
	}
	if normA == 0 || normB == 0 {
		return 0
	}
	return dot / (math.Sqrt(normA) * math.Sqrt(normB))
}

// recency returns exp(-λ × daysSince(t)).
func recency(t, now time.Time) float64 {
	days := now.Sub(t).Hours() / 24
	if days < 0 {
		days = 0
	}
	return math.Exp(-decayLambda * days)
}

// accessBoost returns 1 + log1p(n) × weight, a mild bump for frequently-accessed memories.
func accessBoost(n int64) float64 {
	return 1.0 + math.Log1p(float64(n))*accessWeight
}
