package seo

import (
	"fmt"
	"hash/fnv"
	"math"
)

// ImpressionsPerArm is a two-sided two-proportion sample size, alpha .05,
// power .8. Invalid inputs cannot accidentally start a zero-sample experiment.
func ImpressionsPerArm(p1, p2 float64) int64 {
	if math.IsNaN(p1) || math.IsNaN(p2) || p1 <= 0 || p2 <= p1 || p2 >= 1 {
		return 0
	}
	pbar := (p1 + p2) / 2
	n := math.Pow(1.959964*math.Sqrt(2*pbar*(1-pbar))+.841621*math.Sqrt(p1*(1-p1)+p2*(1-p2)), 2) / ((p2 - p1) * (p2 - p1))
	if math.IsInf(n, 0) || n > float64(math.MaxInt64/2) {
		return 0
	}
	return int64(math.Ceil(n))
}

func ExperimentHash(salt, stratum string, urlID int64) uint64 {
	h := fnv.New64a()
	_, _ = fmt.Fprintf(h, "%s:%s:%d", salt, stratum, urlID)
	return h.Sum64()
}

// ExperimentOutcome is evaluated only at predeclared checkpoints. Bonferroni
// alpha .025 at each of two looks avoids treating repeated .05 peeks as .05.
func ExperimentOutcome(c0, n0, c1, n1, required int64) string {
	if n0 < required || n1 < required || c0 < 0 || c1 < 0 || c0 > n0 || c1 > n1 {
		return "continue"
	}
	p0, p1 := float64(c0)/float64(n0), float64(c1)/float64(n1)
	p := float64(c0+c1) / float64(n0+n1)
	se := math.Sqrt(p * (1 - p) * (1/float64(n0) + 1/float64(n1)))
	if se == 0 {
		return "continue"
	}
	z := (p1 - p0) / se
	if z >= 2.241403 {
		return "won"
	}
	if z <= -2.241403 {
		return "lost"
	}
	return "continue"
}
