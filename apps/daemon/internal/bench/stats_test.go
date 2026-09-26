package bench

import (
	"math"
	"testing"
)

// Reference values from scipy.stats.beta.ppf(0.95, k+1, n-k), the exact Clopper-Pearson bound.
func TestUpperBoundMatchesTheExactClopperPearsonBound(t *testing.T) {
	for _, tt := range []struct {
		k, n int
		want float64
	}{
		{0, 30, 0.0950338529},
		{0, 10, 0.2588655509},
		{1, 10, 0.3941633024},
		{0, 60, 0.0487029133},
		{0, 150, 0.0197734382},
		{0, 300, 0.0099360819},
		{5, 100, 0.1022533776},
		{3, 32, 0.2248161086},
		{29, 30, 0.9982916844},
		{0, 1, 0.95},
	} {
		if got := UpperBound(tt.k, tt.n, Confidence); math.Abs(got-tt.want) > 1e-8 {
			t.Errorf("UpperBound(%d, %d) = %.10f, want %.10f", tt.k, tt.n, got, tt.want)
		}
	}
}

func TestUpperBoundEdges(t *testing.T) {
	if got := UpperBound(0, 0, Confidence); got != 1 {
		t.Errorf("no trials bound nothing: got %v, want 1", got)
	}
	if got := UpperBound(4, 4, Confidence); got != 1 {
		t.Errorf("all events: got %v, want 1", got)
	}
	// ADR 0025's sizes: 30, 60, 150 and 300 broken cases with no false pass.
	for n, most := range map[int]float64{30: 0.10, 60: 0.05, 150: 0.02, 300: 0.01} {
		if got := UpperBound(0, n, Confidence); got > most {
			t.Errorf("UpperBound(0, %d) = %.4f, above the ADR's %.2f", n, got, most)
		}
	}
}

func TestPercentileIsNearestRank(t *testing.T) {
	xs := []float64{5, 1, 4, 2, 3}
	for q, want := range map[float64]float64{50: 3, 95: 5, 20: 1, 100: 5, 1: 1} {
		if got := Percentile(xs, q); got != want {
			t.Errorf("p%v = %v, want %v", q, got, want)
		}
	}
	if Percentile(nil, 50) != 0 {
		t.Error("an empty list has percentile 0")
	}
	if xs[0] != 5 {
		t.Error("Percentile sorted its input in place")
	}
}
