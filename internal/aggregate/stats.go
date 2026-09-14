package aggregate

import (
	"math"
	"slices"
)

// Percentile uses the nearest-rank method on an ascending slice.
func Percentile(sorted []float64, p float64) float64 {
	if len(sorted) == 0 {
		return 0
	}
	idx := int(math.Ceil(p*float64(len(sorted)))) - 1
	return sorted[max(0, min(idx, len(sorted)-1))]
}

func Median(vals []float64) float64 {
	s := slices.Clone(vals)
	slices.Sort(s)
	return Percentile(s, 0.5)
}

// Jitter is the mean absolute difference between consecutive samples.
func Jitter(seq []float64) float64 {
	if len(seq) < 2 {
		return 0
	}
	var sum float64
	for i := 1; i < len(seq); i++ {
		sum += math.Abs(seq[i] - seq[i-1])
	}
	return sum / float64(len(seq)-1)
}
