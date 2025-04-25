package rajomon

import (
	"math/rand"
	"sync"
	"sync/atomic"
	"time"
)

func NewRajomon(nodeName string, callmap map[string][]string, options map[string]interface{}) *PriceTable {
	priceTable := &PriceTable{
		initprice:            0,
		nodeName:             nodeName,
		callMap:              callmap,
		priceTableMap:        sync.Map{},
		rateLimiting:         false,
		rateLimitWaiting:     false,
		loadShedding:         false,
		pinpointThroughput:   false,
		pinpointLatency:      false,
		pinpointQueuing:      false,
		rateLimiter:          make(chan int64, 1),
		fakeInvoker:          false,
		skipPrice:            false,
		priceFreq:            5,
		tokensLeft:           10,
		tokenUpdateRate:      time.Millisecond * 10,
		lastUpdateTime:       time.Now(),
		lastRateLimitedTime:  time.Now().Add(-time.Second),
		tokenUpdateStep:      1,
		tokenRefillDist:      "fixed",
		tokenStrategy:        "all",
		priceStrategy:        "step",
		throughputCounter:    0,
		sampleDiv:            10,
		priceUpdateRate:      time.Millisecond * 10,
		observedDelay:        time.Duration(0),
		clientTimeOut:        time.Duration(0),
		clientBackoff:        time.Duration(0),
		randomRateLimit:      -1,
		throughputThreshold:  0,
		latencyThreshold:     time.Duration(0),
		priceStep:            1,
		priceAggregation:     "maximal",
		guidePrice:           -1,
		consecutiveIncreases: 0,
		decayRate:            0.8,
		maxToken:             10,
	}

	// create a new incoming context with the "request-id" as "0"
	// ctx := metadata.NewIncomingContext(context.Background(), metadata.Pairs("request-id", "0"))

	if debugOpt, ok := options["debug"].(bool); ok {
		debug = debugOpt
	}

	if externalFetchURL, ok := options["externalFetchURL"].(string); ok {
		priceTable.externalFetchURL = externalFetchURL
		logger("externalFetchURL	of %s set to %s\n", nodeName, externalFetchURL)
	}

	if trackingPrice, ok := options["recordPrice"].(bool); ok {
		trackPrice = trackingPrice
	}

	if initprice, ok := options["initprice"].(int64); ok {
		priceTable.initprice = initprice
		// print the initprice of the node if the name is not client
		logger("initprice of %s set to %d\n", nodeName, priceTable.initprice)
	}

	if rateLimiting, ok := options["rateLimiting"].(bool); ok {
		priceTable.rateLimiting = rateLimiting
		logger("rateLimiting 		of %s set to %v\n", nodeName, rateLimiting)
	}

	if loadShedding, ok := options["loadShedding"].(bool); ok {
		priceTable.loadShedding = loadShedding
		logger("loadShedding 		of %s set to %v\n", nodeName, loadShedding)
	}

	if pinpointThroughput, ok := options["pinpointThroughput"].(bool); ok {
		priceTable.pinpointThroughput = pinpointThroughput
		logger("pinpointThroughput	of %s set to %v\n", nodeName, pinpointThroughput)
	}

	if pinpointLatency, ok := options["pinpointLatency"].(bool); ok {
		priceTable.pinpointLatency = pinpointLatency
		logger("pinpointLatency		of %s set to %v\n", nodeName, pinpointLatency)
	}

	if pinpointQueuing, ok := options["pinpointQueuing"].(bool); ok {
		priceTable.pinpointQueuing = pinpointQueuing
		logger("pinpointQueuing		of %s set to %v\n", nodeName, pinpointQueuing)
	}

	if fakeInvoker, ok := options["fakeInvoker"].(bool); ok {
		priceTable.fakeInvoker = fakeInvoker
		logger("fakeInvoker		of %s set to %v\n", nodeName, fakeInvoker)
	}

	if skipPrice, ok := options["lazyResponse"].(bool); ok {
		priceTable.skipPrice = skipPrice
		logger("skipPrice		of %s set to %v\n", nodeName, skipPrice)
	}

	if priceFreq, ok := options["priceFreq"].(int64); ok {
		priceTable.priceFreq = priceFreq
		logger("priceFreq		of %s set to %v\n", nodeName, priceFreq)
	}

	if tokensLeft, ok := options["tokensLeft"].(int64); ok {
		priceTable.tokensLeft = tokensLeft
		logger("tokensLeft		of %s set to %v\n", nodeName, tokensLeft)
	}

	if tokenUpdateRate, ok := options["tokenUpdateRate"].(time.Duration); ok {
		priceTable.tokenUpdateRate = tokenUpdateRate
		logger("tokenUpdateRate		of %s set to %v\n", nodeName, tokenUpdateRate)
	}

	if tokenUpdateStep, ok := options["tokenUpdateStep"].(int64); ok {
		priceTable.tokenUpdateStep = tokenUpdateStep
		logger("tokenUpdateStep		of %s set to %v\n", nodeName, tokenUpdateStep)
	}

	if tokenRefillDist, ok := options["tokenRefillDist"].(string); ok {
		// if the tokenRefillDist is not "fixed" or "uniform", then set it to be "fixed"
		if tokenRefillDist != "fixed" && tokenRefillDist != "uniform" && tokenRefillDist != "poisson" {
			tokenRefillDist = "fixed"
		}
		priceTable.tokenRefillDist = tokenRefillDist
		logger("tokenRefillDist		of %s set to %v\n", nodeName, tokenRefillDist)
	}

	if tokenStrategy, ok := options["tokenStrategy"].(string); ok {
		// if the tokenStrategy is not "all" or "uniform", then set it to be "all"
		if tokenStrategy != "all" && tokenStrategy != "uniform" {
			tokenStrategy = "all"
		}
		priceTable.tokenStrategy = tokenStrategy
		logger("tokenStrategy		of %s set to %v\n", nodeName, tokenStrategy)
	}

	if priceStrategy, ok := options["priceStrategy"].(string); ok {
		priceTable.priceStrategy = priceStrategy
		logger("priceStrategy		of %s set to %v\n", nodeName, priceStrategy)
	}

	if priceUpdateRate, ok := options["priceUpdateRate"].(time.Duration); ok {
		priceTable.priceUpdateRate = priceUpdateRate
		logger("priceUpdateRate		of %s set to %v\n", nodeName, priceUpdateRate)
	}

	if clientTimeOut, ok := options["clientTimeOut"].(time.Duration); ok {
		priceTable.clientTimeOut = clientTimeOut
		logger("clientTimeout		of %s set to %v\n", nodeName, clientTimeOut)
	}

	if clientBackoff, ok := options["clientBackoff"].(time.Duration); ok {
		priceTable.clientBackoff = clientBackoff
		logger("clientBackoff		of %s set to %v\n", nodeName, clientBackoff)
	}

	if randomRateLimit, ok := options["randomRateLimit"].(int64); ok {
		priceTable.randomRateLimit = randomRateLimit
		logger("randomRateLimit		of %s set to %v\n", nodeName, randomRateLimit)
	}

	// priceTable.rateLimitWaiting = true if and only if the clientTimeOut is set to be greater than 0 duration
	if priceTable.clientTimeOut > 0 {
		priceTable.rateLimitWaiting = true
	} else {
		priceTable.rateLimitWaiting = false
	}
	logger("rateLimitWaiting 	of %s set to %v\n", nodeName, priceTable.rateLimitWaiting)

	if throughputThreshold, ok := options["throughputThreshold"].(int64); ok {
		priceTable.throughputThreshold = throughputThreshold
		logger("throughputThreshold	of %s set to %v\n", nodeName, throughputThreshold)
	}

	if latencyThreshold, ok := options["latencyThreshold"].(time.Duration); ok {
		priceTable.latencyThreshold = latencyThreshold
		logger("latencyThreshold	of %s set to %v\n", nodeName, latencyThreshold)
	}

	if priceStep, ok := options["priceStep"].(int64); ok {
		priceTable.priceStep = priceStep
		logger("priceStep		of %s set to %v\n", nodeName, priceStep)
	}

	if priceAggregation, ok := options["priceAggregation"].(string); ok {
		// if the priceAggregation is not "maximal" or "additive", then set it to be "maximal"
		if priceAggregation != "maximal" && priceAggregation != "additive" && priceAggregation != "mean" {
			priceAggregation = "maximal"
		}
		priceTable.priceAggregation = priceAggregation
		logger("priceAggregation	of %s set to %v\n", nodeName, priceAggregation)
	}

	if guidePrice, ok := options["guidePrice"].(int64); ok {
		priceTable.guidePrice = guidePrice
		logger("guidePrice		of %s set to %v\n", nodeName, guidePrice)
	}

	// Rest of the code remains the same
	if priceTable.nodeName == "client" {
		go priceTable.tokenRefill(priceTable.tokenRefillDist, priceTable.tokenUpdateStep, priceTable.tokenUpdateRate)
	} else {
		if priceTable.pinpointQueuing && priceTable.pinpointThroughput {
			go priceTable.checkBoth()
		} else if priceTable.pinpointThroughput {
			go priceTable.throughputCheck()
		} else if priceTable.pinpointLatency {
			go priceTable.latencyCheck()
		} else if priceTable.pinpointQueuing {
			go priceTable.queuingCheck()
		}
	}

	// initialize the price table: Store the initprice to the priceTableMap with key "ownprice", and all method
	// and all method-nodeName pairs to the priceTableMap with key "method-nodeName"
	priceTable.priceTableMap.Store("ownprice", priceTable.initprice)
	for method, nodes := range priceTable.callMap {
		// Store method prices
		priceTable.priceTableMap.Store(method, priceTable.initprice)
		logger("[InitPriceTable]: Method %s price set to %d\n", method, priceTable.initprice)
		for _, node := range nodes {
			priceTable.priceTableMap.Store(method+"-"+node, priceTable.initprice)
			logger("[InitPriceTable]: Method %s-%s price set to %d\n", method, node, priceTable.initprice)
		}
	}
	return priceTable
}

// Atomic read of tokensLeft
func (pt *PriceTable) GetTokensLeft() int64 {
	if !atomicTokens {
		return pt.tokensLeft
	}
	return atomic.LoadInt64(&pt.tokensLeft)
}

// Atomic deduction of tokens
func (pt *PriceTable) DeductTokens(n int64) bool {
	if !atomicTokens {
		if pt.tokensLeft-n < 0 {
			return false
		} else {
			pt.tokensLeft -= n
			return true
		}
	}
	for {
		currentTokens := pt.GetTokensLeft()
		newTokens := currentTokens - n
		if newTokens < 0 {
			// Unsuccessful deduction, handle as needed
			return false
		}
		if atomic.CompareAndSwapInt64(&pt.tokensLeft, currentTokens, newTokens) {
			return true
		}
	}
}

// Atomic addition of tokens
func (pt *PriceTable) AddTokens(n int64) {
	if !atomicTokens {
		pt.tokensLeft += n
		return
	}
	atomic.AddInt64(&pt.tokensLeft, n)
}

// tokenRefill is a goroutine that refills the tokens in the price table.
func (pt *PriceTable) tokenRefill(tokenRefillDist string, tokenUpdateStep int64, tokenUpdateRate time.Duration) {
	if tokenRefillDist == "poisson" {
		// Create a ticker with an initial tick duration
		ticker := time.NewTicker(pt.initialTokenUpdateInterval())
		defer ticker.Stop()
		// lambda is 1 over pt.tokenUpdateRate.Milliseconds(), but make lambda a float64
		lambda := float64(1) / float64(tokenUpdateRate.Milliseconds())

		for range ticker.C {
			// Add tokens to the client deterministically
			pt.AddTokens(tokenUpdateStep)

			if pt.rateLimitWaiting {
				// pt.lastUpdateTime = time.Now()
				pt.unblockRateLimiter()
			}

			// Adjust the tick duration based on the exponential distribution
			ticker.Reset(pt.nextTokenUpdateInterval(lambda))
		}
	} else {
		for range time.Tick(tokenUpdateRate) {
			// add tokens to the client deterministically or randomly, depending on the tokenRefillDist
			if tokenRefillDist == "fixed" {
				pt.AddTokens(tokenUpdateStep)
			} else if tokenRefillDist == "uniform" {
				pt.AddTokens(rand.Int63n(tokenUpdateStep * 2))
			}
			if pt.rateLimitWaiting {
				// pt.lastUpdateTime = time.Now()
				pt.unblockRateLimiter()
			}
		}
	}
}

// initialTokenUpdateInterval returns the initial tick duration for the tokenRefill.
func (pt *PriceTable) initialTokenUpdateInterval() time.Duration {
	// Return the desired initial tick duration
	return pt.tokenUpdateRate
}

// nextTokenUpdateInterval returns the next tick duration for the tokenRefill based on the exponential distribution.
func (pt *PriceTable) nextTokenUpdateInterval(lambda float64) time.Duration {
	// Calculate the next tick duration based on the exponential distribution
	// For example, you can use a lambda value of 0.5 for the exponential distribution
	nextTickDuration := time.Duration(rand.ExpFloat64()/lambda) * time.Millisecond

	if nextTickDuration <= 0 {
		// Handle the case when nextTickDuration is non-positive
		nextTickDuration = time.Millisecond // Set a default positive duration
	}
	// Return the next tick duration
	return time.Duration(nextTickDuration)
}
