package rajomon

import (
	"context"
	"runtime/metrics"
	"sync/atomic"
	"time"
)

// define a package‐wide key type and key constant
type ctxKey string

const GapLatencyKey ctxKey = "gapLatency"

func (pt *PriceTable) Increment() {
	atomic.AddInt64(&pt.throughputCounter, 1)
}

func (pt *PriceTable) Decrement(step int64) {
	atomic.AddInt64(&pt.throughputCounter, -step)
}

func (pt *PriceTable) GetCount() int64 {
	// return atomic.LoadInt64(&cc.throughtputCounter)
	return atomic.SwapInt64(&pt.throughputCounter, 0)
}

func (pt *PriceTable) latencyCheck() {
	for range time.Tick(pt.priceUpdateRate) {
		// create a new incoming context with the "request-id" as "0"
		// ctx := metadata.NewIncomingContext(context.Background(), metadata.Pairs("request-id", "0"))

		// change to using the average latency
		pt.UpdateOwnPrice(pt.observedDelay.Milliseconds() > pt.latencyThreshold.Milliseconds()*pt.GetCount())
		pt.observedDelay = time.Duration(0)
	}
}

// queuingCheck checks if the queuing delay of go routine is greater than the latency SLO.
func (pt *PriceTable) queuingCheck() {
	// define a custom type for context key

	// init a null histogram
	var prevHist *metrics.Float64Histogram
	for range time.Tick(pt.priceUpdateRate) {
		// start a timer to measure the query latency
		start := time.Now()
		// get the current histogram
		currHist := readHistogram()

		if prevHist == nil {
			// directly go to next iteration
			prevHist = currHist
			continue
		}
		gapLatency := maximumQueuingDelayms(prevHist, currHist)

		ctx := context.Background()

		logger("[Incremental Waiting Time Maximum]:	%f ms.\n", gapLatency)
		// store the gapLatency in the context ctx
		ctx = context.WithValue(ctx, GapLatencyKey, gapLatency)

		if pt.priceStrategy == "step" {
			pt.UpdateOwnPrice(pt.overloadDetection(ctx))
		} else if pt.priceStrategy == "co2" {
			pt.PriceFromCO2(ctx, pt.nodeName)
		} else {
			pt.UpdatePrice(ctx)
		}

		// copy the content of current histogram to the previous histogram
		prevHist = currHist
		// log the time elapsed for the query
		logger("[Query Latency]:	Overhead is %.2f milliseconds\n", float64(time.Since(start).Microseconds())/1000)
	}
}

// throughputCheck decrements the counter by 2x every x milliseconds.
func (pt *PriceTable) throughputCheck() {
	for range time.Tick(pt.priceUpdateRate) {
		// pt.Decrement(pt.throughputThreshold)
		// Create an empty context
		// ctx := metadata.NewIncomingContext(context.Background(), metadata.Pairs("request-id", "0"))
		logger("[Throughput Counter]:	The throughtput counter is %d\n", pt.throughputCounter)
		// pt.UpdateOwnPrice(ctx, pt.GetCount() > 0)
		// update own price if getCounter is greater than the threshold
		pt.UpdateOwnPrice(pt.GetCount() > pt.throughputThreshold)
	}
}

// checkBoth checks both throughput and latency.
func (pt *PriceTable) checkBoth() {
	var prevHist *metrics.Float64Histogram
	for range time.Tick(pt.priceUpdateRate) {
		logger("[Throughput Counter]:	The throughtput counter is %d\n", pt.throughputCounter)

		// get the current histogram
		currHist := readHistogram()

		// calculate the differernce between the two histograms prevHist and currHist
		diff := metrics.Float64Histogram{}
		// if preHist is empty pointer, return currHist
		if prevHist == nil {
			diff = *currHist
		} else {
			diff = GetHistogramDifference(*prevHist, *currHist)
		}
		// maxLatency is the max of the histogram in milliseconds.
		gapLatency := maximumBucket(&diff)

		cumulativeLat := medianBucket(currHist)
		// printHistogram(currHist)
		logger("[Cumulative Waiting Time Median]:	%f ms.\n", cumulativeLat)
		// printHistogram(&diff)
		logger("[Incremental Waiting Time 90-tile]:	%f ms.\n", percentileBucket(&diff, 90))
		logger("[Incremental Waiting Time Median]:	%f ms.\n", medianBucket(&diff))
		logger("[Incremental Waiting Time Maximum]:	%f ms.\n", maximumBucket(&diff))

		pt.UpdateOwnPrice(pt.GetCount() > pt.throughputThreshold && int64(gapLatency*1000) > pt.latencyThreshold.Microseconds())
		// copy the content of current histogram to the previous histogram
		prevHist = currHist
	}
}

// overloadDetection takes signals as input, (either pinpointLatency or throughputCounter)
// and compares them with the threshold. If the signal is greater than the threshold,
// then the overload flag is set to true. If the signal is less than the threshold,
// then the overload flag is set to false. The overload flag is then used to update
// the price table.
func (pt *PriceTable) overloadDetection(ctx context.Context) bool {
	if pt.pinpointQueuing {
		// declare a variable to store the gapLatency
		var gapLatency float64

		// read the gapLatency from context ctx
		val := ctx.Value(GapLatencyKey)
		if val == nil {
			gapLatency = 0.0
		} else {
			gapLatency = val.(float64)
		}
		if int64(gapLatency*1000) > pt.latencyThreshold.Microseconds() {
			return true
		}
	}
	return false
}
