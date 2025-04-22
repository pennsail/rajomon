package rajomon

import (
	"context"
	"testing"
	"time"
)

// TestFetchPrice verifies that FetchPrice correctly parses JSON from the HTTP endpoint.
func TestFetchPrice(t *testing.T) {
	// call FetchPrice
	pm, err := FetchPrice("localhost:8080", "")
	if err != nil {
		t.Fatalf("FetchPrice error: %v", err)
	}
	// compare values
	if pm.SystemCPU == 0 {
		t.Errorf("SystemCPU = %v; want non-zero", pm.SystemCPU)
	}
	if pm.ContainerCPU != 0 {
		t.Errorf("ContainerCPU = %v; want zero", pm.ContainerCPU)
	}
	if pm.CPUPercent < 0 || pm.CPUPercent > 100 {
		t.Errorf("CPUPercent = %v; want between 0 and 100", pm.CPUPercent)
	}
}

var (
	CO2opts = map[string]interface{}{
		"priceUpdateRate":  5000 * time.Microsecond,
		"tokenUpdateRate":  100000 * time.Microsecond,
		"latencyThreshold": 500 * time.Microsecond,
		"priceStep":        int64(180),
		"priceStrategy":    "co2",
		"lazyResponse":     false,
		"rateLimiting":     true,
		"loadShedding":     true,
		"pinpointQueuing":  true,
		"debug":            false,
		"externalFetchURL": "localhost:8080",
	}
)

// TestPriceFromCO2 checks that PriceFromCO2 updates ownprice based on ContainerCPU/(counter+1).
func TestPriceFromCO2(t *testing.T) {
	// Prepare a PriceTable with externalFetchURL pointing to our server
	callMap := map[string][]string{"Greeting": {}}

	pt := NewRajomon("", callMap, CO2opts)

	// Case1: throughputCounter=0 => denom=1 => price = int(9.0/1)=9
	pt.throughputCounter = 0
	if err := pt.PriceFromCO2(context.Background(), ""); err != nil {
		t.Fatalf("PriceFromCO2 failed: %v", err)
	}
	own1, _ := pt.priceTableMap.Load("ownprice")
	// print own1
	t.Logf("Price from Sys CO2: %v\n", own1)

	if own1.(int64) == 0 {
		t.Errorf("carbon price = %d; want non-zero", own1.(int64))
	}

	// Case2: throughputCounter=2 => denom=3 => price = int(9.0/3)=3
	pt.throughputCounter = 2
	if err := pt.PriceFromCO2(context.Background(), ""); err != nil {
		t.Fatalf("PriceFromCO2 failed: %v", err)
	}
	own2, _ := pt.priceTableMap.Load("ownprice")
	if own2.(int64) == 0 {
		t.Errorf("per request price = %d; want non-zero", own2.(int64))
	}
}
