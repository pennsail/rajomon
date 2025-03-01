package rajomon

import (
	"fmt"
	"math"
	"runtime/metrics"
)

func medianBucket(h *metrics.Float64Histogram) float64 {
	total := uint64(0)
	for _, count := range h.Counts {
		total += count
	}

	// round up thresh if total is odd
	thresh := (total + 1) / 2
	total = 0
	// if count is 0, return the 2nd bucket
	// for i, count := range h.Counts, but skip the first bucket
	for i := 1; i < len(h.Counts); i++ {
		total += h.Counts[i]
		if total >= thresh {
			return h.Buckets[i] * 1000
		}
	}
	panic("should not happen")
}

func percentileBucket(h *metrics.Float64Histogram, percentile float64) float64 {
	total := uint64(0)
	for _, count := range h.Counts {
		total += count
	}

	thresh := uint64(math.Ceil(float64(total) * (percentile / 100.0)))
	total = 0

	// Iterate through the histogram counts, starting from the second bucket
	// and find the bucket that surpasses the threshold count.
	for i := 1; i < len(h.Counts); i++ {
		total += h.Counts[i]
		if total >= thresh {
			return h.Buckets[i] * 1000 // Convert to milliseconds
		}
	}

	panic("should not happen")
}

// similarly, maximumBucket returns the maximum bucket
func maximumBucket(h *metrics.Float64Histogram) float64 {
	for i := len(h.Counts) - 1; i >= 0; i-- {
		if h.Counts[i] != 0 {
			return h.Buckets[i] * 1000
		}
	}
	return 0
}

// To extract the difference between two Float64Histogram distributions, and return a new Float64Histogram
// you can subtract the corresponding bucket counts of the two histograms.
// If the earlier histogram is from an empty pointer, return the later histogram
// Ensure the two histograms have the same number of buckets
func GetHistogramDifference(earlier, later metrics.Float64Histogram) metrics.Float64Histogram {
	// if the earlier histogram isfrom an empty pointer, return the later histogram
	if len(earlier.Counts) == 0 {
		return later
	}

	// Ensure the two histograms have the same number of buckets
	if len(earlier.Counts) != len(later.Counts) {
		panic("histograms have different number of buckets")
	}

	// if either the earlier or later histogram is empty, panic
	if len(earlier.Counts) == 0 || len(later.Counts) == 0 {
		panic("histogram has no buckets")
		// return &metrics.Float64Histogram{}
	}

	// Calculate the difference between the bucket counts and return the gap histogram
	// diff := metrics.Float64Histogram{}

	// Create a new histogram for the difference
	diff := metrics.Float64Histogram{
		Counts:  make([]uint64, len(earlier.Counts)),
		Buckets: earlier.Buckets, // Assuming Buckets are the same for both histograms
	}

	for i := range earlier.Counts {
		diff.Counts[i] = later.Counts[i] - earlier.Counts[i]
	}
	return diff
}

// we should be able to avoid the GetHistogramDifference function by using the following function
// Find the maximum bucket between two Float64Histogram distributions
func maximumQueuingDelayms(earlier, later *metrics.Float64Histogram) float64 {
	for i := len(earlier.Counts) - 1; i >= 0; i-- {
		if later.Counts[i] > earlier.Counts[i] {
			return later.Buckets[i] * 1000
		}
	}
	return 0
}

// this function reads the currHist from metrics
func readHistogram() *metrics.Float64Histogram {
	// Create a sample for metric /sched/latencies:seconds and /sync/mutex/wait/total:seconds
	const queueingDelay = "/sched/latencies:seconds"
	measureMutexWait := false

	// Create a sample for the metric.
	sample := make([]metrics.Sample, 1)
	sample[0].Name = queueingDelay
	if measureMutexWait {
		const mutexWait = "/sync/mutex/wait/total:seconds"
		sample[1].Name = mutexWait
	}

	// Sample the metric.
	metrics.Read(sample)

	// Check if the metric is actually supported.
	// If it's not, the resulting value will always have
	// kind KindBad.
	if sample[0].Value.Kind() == metrics.KindBad {
		panic(fmt.Sprintf("metric %q no longer supported", queueingDelay))
	}

	// get the current histogram
	currHist := sample[0].Value.Float64Histogram()

	return currHist
}

// // func printHistogram(h *metrics.Float64Histogram) prints the content of histogram h
// func printHistogram(h *metrics.Float64Histogram) {
// 	// fmt.Printf("Histogram: %v\n", h)
// 	fmt.Printf("Buckets: %v\n", h.Buckets)
// 	fmt.Printf("Counts: %v\n", h.Counts)
// }
