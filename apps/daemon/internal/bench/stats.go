package bench

import (
	"math"
	"sort"
)

// Confidence is the level of every bound the report prints.
const Confidence = 0.95

// UpperBound is the exact (Clopper-Pearson) one-sided upper confidence bound on a rate after k
// events in n trials: the p at which seeing k or fewer would happen only 1-conf of the time. It
// is the upper end of the two-sided Clopper-Pearson interval at 2*conf-1 (90% for 95%), equal
// to the Beta(k+1, n-k) quantile at conf. At k = 0 it is 1-(1-conf)^(1/n), the exact form of the
// rule of three that ADR 0025's sizes come from (30 trials: 9.5%). n = 0 bounds nothing: 1.
func UpperBound(k, n int, conf float64) float64 {
	if n <= 0 || k >= n {
		return 1
	}
	if k < 0 {
		k = 0
	}
	alpha := 1 - conf
	if k == 0 {
		return 1 - math.Pow(alpha, 1/float64(n))
	}
	// binomCDF falls as p rises, so bisect for binomCDF(k; n, p) = alpha.
	lo, hi := float64(k)/float64(n), 1.0
	for i := 0; i < 200 && hi-lo > 1e-13; i++ {
		mid := (lo + hi) / 2
		if binomCDF(k, n, mid) > alpha {
			lo = mid
		} else {
			hi = mid
		}
	}
	return (lo + hi) / 2
}

// binomCDF is P(X <= k) for X ~ Binomial(n, p), summed in log space.
func binomCDF(k, n int, p float64) float64 {
	if p <= 0 {
		return 1
	}
	if p >= 1 {
		if k >= n {
			return 1
		}
		return 0
	}
	lp, lq := math.Log(p), math.Log1p(-p)
	lgN, _ := math.Lgamma(float64(n + 1))
	sum := 0.0
	for i := 0; i <= k; i++ {
		lgI, _ := math.Lgamma(float64(i + 1))
		lgNI, _ := math.Lgamma(float64(n - i + 1))
		sum += math.Exp(lgN - lgI - lgNI + float64(i)*lp + float64(n-i)*lq)
	}
	return math.Min(sum, 1)
}

// Percentile is the nearest-rank percentile (0 < q <= 100) of xs; 0 for none.
func Percentile(xs []float64, q float64) float64 {
	if len(xs) == 0 {
		return 0
	}
	s := append([]float64(nil), xs...)
	sort.Float64s(s)
	rank := int(math.Ceil(q / 100 * float64(len(s))))
	if rank < 1 {
		rank = 1
	}
	if rank > len(s) {
		rank = len(s)
	}
	return s[rank-1]
}
