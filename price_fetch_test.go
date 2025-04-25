package rajomon

import (
	"testing"
	"time"
)

// TestFetchPrice verifies that FetchPrice correctly parses JSON from the HTTP endpoint.
func TestFetchPrice(t *testing.T) {
	// call FetchPrice
	pm, err := FetchPrice("host.docker.internal:8080")
	if err != nil {
		t.Fatalf("FetchPrice error: %v", err)
	}
	// compare values
	// non of the pm values should be 0. check all powerMetrics attributes
	if pm.CPUPowerW == 0 || pm.DRAMPowerW == 0 || pm.CPUUtilizationPercent == 0 || pm.CarbonIntensityGPerSec == 0 || pm.GridCarbonIntensityGPerKWh == 0 {
		t.Errorf("FetchPrice returned zero values: %v", pm)
	}

}

var (
	CO2opts = map[string]interface{}{
		"priceUpdateRate":  5000 * time.Microsecond,
		"tokenUpdateRate":  100000 * time.Microsecond,
		"latencyThreshold": 500 * time.Microsecond,
		"priceStep":        int64(180),
		"priceStrategy":    "co2",
		"priceAggregation": "additive",
		"lazyResponse":     false,
		"rateLimiting":     true,
		"loadShedding":     true,
		"pinpointQueuing":  true,
		"debug":            false,
		"externalFetchURL": "host.docker.internal:8080",
	}
)

// TestPriceFromCO2 checks that PriceFromCO2 updates ownprice based on ContainerCPU/(counter+1).
func TestPriceFromCO2(t *testing.T) {
	// Prepare a PriceTable with externalFetchURL pointing to our server
	callMap := map[string][]string{"Greeting": {}}

	pt := NewRajomon("raj-test", callMap, CO2opts)

	// Case1: throughputCounter=0 => denom=1 => price = int(9.0/1)=9
	pt.throughputCounter = 0
	if err := pt.PriceFromCO2(); err != nil {
		t.Fatalf("PriceFromCO2 failed: %v", err)
	}
	own1, _ := pt.priceTableMap.Load("ownprice")
	// print own1
	t.Logf("Price from Sys CO2: %v\n", own1)

	if own1.(int64) == 0 {
		t.Errorf("carbon price = %d; want non-zero", own1.(int64))
	}
}
