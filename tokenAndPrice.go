package rajomon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// SplitTokens splits the tokens left on the request to the downstream services.
// It returns a map, with the downstream service names as keys, and tokens left for them as values.
func (pt *PriceTable) SplitTokens(ctx context.Context, tokenleft int64, methodName string) ([]string, error) {
	downstreamNames := pt.callMap[methodName]
	size := len(downstreamNames)
	if size == 0 {
		return nil, nil
	}

	downstreamTokens := []string{}
	downstreamPriceSum, _ := pt.RetrieveDSPrice(ctx, methodName)
	tokenleftPerDownstream := (tokenleft - downstreamPriceSum) / int64(size)

	logger("[Split tokens]: downstream total price is %d, from %d downstream services for %s, extra token left for each ds is %d\n",
		downstreamPriceSum, size, pt.nodeName, tokenleftPerDownstream)

	for _, downstreamName := range downstreamNames {
		// concatenate the method name with node name to distinguish different downstream services calls.
		downstreamPriceString, _ := pt.priceTableMap.Load(methodName + "-" + downstreamName)
		downstreamPrice := downstreamPriceString.(int64)
		downstreamToken := tokenleftPerDownstream + downstreamPrice
		downstreamTokens = append(downstreamTokens, "tokens-"+downstreamName, strconv.FormatInt(downstreamToken, 10))
		// logger("[Split tokens]:	token for %s is %d + %d\n", downstreamName, tokenleftPerDownstream, downstreamPrice)
	}
	return downstreamTokens, nil
}

func (pt *PriceTable) RetrieveDSPrice(ctx context.Context, methodName string) (int64, error) {
	if len(pt.callMap[methodName]) == 0 {
		logger("[Retrieve DS Price]:	No downstream service for %s\n", methodName)
		return 0, nil
	}
	// load the downstream price from the price table with method name as key.
	downstreamPrice_string, ok := pt.priceTableMap.Load(methodName)
	if !ok || downstreamPrice_string == nil {
		return 0, errors.New("[retrieve ds price] downstream price not found")
	}

	downstreamPrice, ok := downstreamPrice_string.(int64)
	if !ok {
		return 0, errors.New("[retrieve ds price] downstream price wrong type")
	}
	logger("[Retrieve DS Price]:	Downstream price of %s is %d\n", methodName, downstreamPrice)
	return downstreamPrice, nil
}

func (pt *PriceTable) RetrieveTotalPrice(ctx context.Context, methodName string) (string, error) {
	ownPrice_string, _ := pt.priceTableMap.Load("ownprice")
	ownPrice := ownPrice_string.(int64)
	downstreamPrice, _ := pt.RetrieveDSPrice(ctx, methodName)
	var totalPrice int64
	if pt.priceAggregation == "maximal" {
		// totalPrice is the max of ownPrice and downstreamPrice
		if ownPrice > downstreamPrice {
			totalPrice = ownPrice
		} else {
			totalPrice = downstreamPrice
		}
	} else if pt.priceAggregation == "additive" {
		totalPrice = ownPrice + downstreamPrice
	} else if pt.priceAggregation == "mean" {
		totalPrice = (ownPrice + downstreamPrice) / 2
	}
	price_string := strconv.FormatInt(totalPrice, 10)
	logger("[Retrieve Total Price]:	Downstream price of %s is now %d, total price %d \n", methodName, downstreamPrice, totalPrice)
	return price_string, nil
}

// Assume that own price is per microservice and it does not change across different types of requests/interfaces.
func (pt *PriceTable) UpdateOwnPrice(congestion bool) error {

	ownPrice_string, _ := pt.priceTableMap.Load("ownprice")
	ownPrice := ownPrice_string.(int64)
	// The following code has been moved to decrementCounter() for pinpointThroughput.
	logger("[Update OwnPrice]:	congestion is %t, own price %d, step %d\n", congestion, ownPrice, pt.priceStep)
	if congestion {
		ownPrice += pt.priceStep
		if pt.guidePrice > -1 {
			ownPrice = pt.guidePrice
		}
	} else if ownPrice > 0 {
		ownPrice -= 1
	}
	pt.priceTableMap.Store("ownprice", ownPrice)
	logger("[Update OwnPrice]:	Own price updated to %d\n", ownPrice)
	return nil
}

// new powerMetrics with the extra CO₂ fields
type powerMetrics struct {
	CPUPowerW                  float64 `json:"cpu_power_w"`
	DRAMPowerW                 float64 `json:"dram_power_w"`
	CPUUtilizationPercent      float64 `json:"cpu_utilization_percent"`
	CarbonIntensityGPerSec     float64 `json:"carbon_intensity_g_per_sec"`
	GridCarbonIntensityGPerKWh float64 `json:"grid_carbon_intensity_g_per_kWh"`
}

// FetchPrice queries your C program at http://host:port/power?container_id=cid
// and returns the parsed metrics.
func FetchPrice(hostPort string) (*powerMetrics, error) {
	url := fmt.Sprintf("http://%s", hostPort)

	client := &http.Client{Timeout: 2 * time.Second}

	resp, err := client.Get(url)
	if err != nil {
		return nil, fmt.Errorf("http GET %s failed: %w", url, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("unexpected status %d from %s", resp.StatusCode, url)
	}

	var m powerMetrics
	if err := json.NewDecoder(resp.Body).Decode(&m); err != nil {
		return nil, fmt.Errorf("invalid JSON from %s: %w", url, err)
	}
	return &m, nil
}

// fetchExternalPrice calls your C‐program HTTP API and returns the cpu_power_w value (rounded)
func (pt *PriceTable) fetchExternalPrice() (int64, error) {
	pm, err := FetchPrice(pt.externalFetchURL)

	if err != nil {
		log.Printf("Error fetching power: %v", err)
		return 0, err
	}
	logger("system CPU: %.2f W, container CPU: %.2f W, CPU percent: %.2f%%\n",
		pm.CPUPowerW, pm.DRAMPowerW, pm.CPUUtilizationPercent)
	logger("carbon intensity: %.2f g/s, grid carbon intensity: %.2f g/kWh\n",
		pm.CarbonIntensityGPerSec, pm.GridCarbonIntensityGPerKWh)

	// post-process the data to get the price
	price := int64(pm.CPUPowerW * pm.CarbonIntensityGPerSec)
	// ToDo: the counter right now is never updated, so always 0
	// ToDo: to test without container, we use system CPU power
	// later on, we can use container CPU power

	return int64(price), nil
}

// PriceFromCO2 fetches a CO₂‑based price from your external service and
// replaces pt.ownprice with it.
func (pt *PriceTable) PriceFromCO2() error {
	newPrice, err := pt.fetchExternalPrice() // uses the helper we wrote earlier
	if err != nil {
		return fmt.Errorf("PriceFromCO2: %w", err)
	}
	pt.priceTableMap.Store("ownprice", newPrice)
	logger("[PriceFromCO2]: ownprice updated to %d from CO₂ API\n", newPrice)
	return nil
}

// merged function for both linear and exponential price update
func (pt *PriceTable) UpdatePrice(ctx context.Context) error {
	// 1. Retrieve the current ownPrice
	ownPriceInterface, _ := pt.priceTableMap.Load("ownprice")
	ownPrice := ownPriceInterface.(int64)

	var gapLatency float64
	// 2. Extract gapLatency and calculate the latency difference (diff)
	val := ctx.Value(GapLatencyKey)
	if val == nil {
		gapLatency = 0.0
	} else {
		gapLatency = val.(float64)
	}
	diff := int64(gapLatency*1000) - pt.latencyThreshold.Microseconds()

	// proportionally adjust the price based on the latency difference
	adjustment := diff * pt.priceStep / 10000

	// Implement the decay mechanism
	if pt.priceStrategy == "expdecay" {
		if adjustment > 0 {
			if pt.consecutiveIncreases >= 2 {
				// If the counter exceeds the threshold, decay the step size by 1/5 ** counter
				adjustment = int64(float64(adjustment) * math.Pow(pt.decayRate, float64(pt.consecutiveIncreases)))
				logger("[Price Step Decay]: Price step decreased by %f ** %d\n", pt.decayRate, pt.consecutiveIncreases)
			}
			pt.consecutiveIncreases++ // Increment counter for consecutive increases
		} else if adjustment < 0 {

			// Reset counter and step size to non-decay version
			pt.consecutiveIncreases = 0
		}
	}

	logger("[Update Price by Queue Delay]: Own price %d, step %d\n", ownPrice, adjustment)

	ownPrice += adjustment
	// Set reservePrice to the larger of pt.guidePrice and 0
	reservePrice := int64(math.Max(float64(pt.guidePrice), 0))

	if ownPrice <= reservePrice {
		ownPrice = reservePrice
	}

	pt.priceTableMap.Store("ownprice", ownPrice)
	// run the following code every 200 milliseconds
	if pt.lastUpdateTime.Add(200 * time.Millisecond).Before(time.Now()) {
		// merge the log code into a single line
		msgToLog := fmt.Sprintf("[Own price]: %d [Incremental Waiting Time Maximum]:	%.2f ms.\n", ownPrice, gapLatency)
		recordPrice(msgToLog)
		pt.lastUpdateTime = time.Now()
	}

	return nil
}

// UpdateDownstreamPrice incorperates the downstream price table to its own price table.
func (pt *PriceTable) UpdateDownstreamPrice(ctx context.Context, method string, nodeName string, downstreamPrice int64) (int64, error) {
	if pt.priceAggregation == "maximal" || pt.priceAggregation == "mean" {
		// Update the downstream price, but concatenate the method name with node name to distinguish different downstream services calls.
		pt.priceTableMap.Store(method+"-"+nodeName, downstreamPrice)
		logger("[Received Resp]:	Downstream price of %s updated to %d\n", method+"-"+nodeName, downstreamPrice)
		// if the downstream price is greater than the current downstream price, update the downstream price.
		downstreamPrice_old, loaded := pt.priceTableMap.Load(method)
		if !loaded {
			// raise an error if the downstream price is not loaded.
			logger("[Error]:	Cannot find the previous downstream price of %s\n", method)
			// return 0, status.Errorf(codes.Aborted, fmt.Sprintf("Downstream price of %s is not loaded", method+"-"+nodeName))
			downstreamPrice_old = int64(0)
		} else {
			logger("[Previous DS Price]:	The DS price of %s was %d before the update.\n", method, downstreamPrice_old)
		}
		if downstreamPrice > downstreamPrice_old.(int64) {
			// update the downstream price
			pt.priceTableMap.Store(method, downstreamPrice)
			logger("[Updating DS Price]:	Downstream price of %s updated to %d\n", method, downstreamPrice)
			return downstreamPrice, nil
		}
		// if the downstream price is not greater than the current downstream price, store it and calculate the max
		// find the maximum downstream price of the request.

		maxPrice := int64(math.MinInt64) // Smallest int64 value

		pt.priceTableMap.Range(func(key, value interface{}) bool {
			if k, ok := key.(string); ok && strings.HasPrefix(k, method+"-") {
				if v, valid := value.(int64); valid && v > maxPrice {
					maxPrice = v
				}
			}
			return true
		})
		logger("[Updated DS Price]:	The price of %s is now %d\n", method, maxPrice)
		// update the downstream price only for the method involved in the request.
		pt.priceTableMap.Store(method, maxPrice)

	} else if pt.priceAggregation == "additive" {
		// load the downstream price from the price table with method + node name as key.
		downstreamPrice_old, loaded := pt.priceTableMap.Swap(method+"-"+nodeName, downstreamPrice)
		if !loaded {
			// raise an error if the downstream price is not loaded.
			return 0, status.Errorf(codes.Aborted, fmt.Sprintf("Downstream price of %s is not loaded", method+"-"+nodeName))
		}
		// calculate the new diff between the old downstream price and the new downstream price.
		diff := downstreamPrice - downstreamPrice_old.(int64)
		// if the diff is 0, return 0
		if diff == 0 {
			return 0, nil
		}
		// apply the diff to the all methods' whenever the downstream price is part of the method call.
		for methodName, downstreamNames := range pt.callMap {
			for _, downstreamName := range downstreamNames {
				if downstreamName == nodeName {
					// increase the downstream price of the method by the diff.
					// load the downstream price from the price table with method name as key. then update the downstream price. then save it back to the price table.
					methodPrice, _ := pt.priceTableMap.Load(methodName)
					methodPrice = methodPrice.(int64) + diff
					pt.priceTableMap.Store(methodName, methodPrice)
					// log the downstream price of the request.
					logger("[Updated DS Price]:	The price of %s is now %d\n", methodName, methodPrice)
				}
			}
		}

		// Update the downstream price, but concatenate the method name with node name to distinguish different downstream services calls.
		pt.priceTableMap.Store(method+"-"+nodeName, downstreamPrice)
		logger("[Received Resp]:	Downstream price of %s updated to %d\n", method+"-"+nodeName, downstreamPrice)
		// pt.SaveDSPrice(ctx, method)
	}
	return downstreamPrice, nil
}
