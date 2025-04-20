package rajomon

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// TestFetchPrice verifies that FetchPrice correctly parses JSON from the HTTP endpoint.
func TestFetchPrice(t *testing.T) {
	// sample JSON response
	sample := powerMetrics{
		SystemCPU:     10.5,
		SystemDRAM:    5.5,
		ContainerCPU:  3.3,
		ContainerDRAM: 2.2,
		CPUPercent:    25.0,
	}
	// start a test HTTP server
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// verify the path and query
		if r.URL.Path != "/power" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		q := r.URL.Query().Get("container_id")
		if q != "test-id" {
			t.Errorf("unexpected container_id: %s", q)
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(sample)
	}))
	defer srv.Close()

	// call FetchPrice
	pm, err := FetchPrice(srv.Listener.Addr().String(), "test-id")
	if err != nil {
		t.Fatalf("FetchPrice error: %v", err)
	}
	// compare values
	if pm.SystemCPU != sample.SystemCPU {
		t.Errorf("SystemCPU = %v; want %v", pm.SystemCPU, sample.SystemCPU)
	}
	if pm.ContainerCPU != sample.ContainerCPU {
		t.Errorf("ContainerCPU = %v; want %v", pm.ContainerCPU, sample.ContainerCPU)
	}
	if pm.CPUPercent != sample.CPUPercent {
		t.Errorf("CPUPercent = %v; want %v", pm.CPUPercent, sample.CPUPercent)
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
		// "debug":            true,
		"externalFetchURL": "localhost:8080",
	}
)

// TestPriceFromCO2 checks that PriceFromCO2 updates ownprice based on ContainerCPU/(counter+1).
func TestPriceFromCO2(t *testing.T) {
	// sample JSON for ContainerCPU=9.0
	sample := powerMetrics{ContainerCPU: 9.0}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(sample)
	}))
	defer srv.Close()

	// Prepare a PriceTable with externalFetchURL pointing to our server
	callMap := map[string][]string{"Greeting": {}}

	pt := NewRajomon("test-container", callMap, CO2opts)

	// Case1: throughputCounter=0 => denom=1 => price = int(9.0/1)=9
	pt.throughputCounter = 0
	if err := pt.PriceFromCO2(context.Background(), "test-container"); err != nil {
		t.Fatalf("PriceFromCO2 failed: %v", err)
	}
	own1, _ := pt.priceTableMap.Load("ownprice")
	if own1.(int64) != 9 {
		t.Errorf("ownprice = %d; want 9", own1.(int64))
	}

	// Case2: throughputCounter=2 => denom=3 => price = int(9.0/3)=3
	pt.throughputCounter = 2
	if err := pt.PriceFromCO2(context.Background(), "test-container"); err != nil {
		t.Fatalf("PriceFromCO2 failed: %v", err)
	}
	own2, _ := pt.priceTableMap.Load("ownprice")
	if own2.(int64) != 3 {
		t.Errorf("ownprice = %d; want 3", own2.(int64))
	}
}
