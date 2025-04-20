// server_test.go
package rajomon

import (
	"context"
	"strconv"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

var (
	defaultOpts = map[string]interface{}{
		"priceUpdateRate":  5000 * time.Microsecond,
		"tokenUpdateRate":  100000 * time.Microsecond,
		"latencyThreshold": 500 * time.Microsecond,
		"priceStep":        int64(180),
		"priceStrategy":    "expdecay",
		"lazyResponse":     false,
		"rateLimiting":     true,
		"loadShedding":     true,
	}

	defaultCallMap = map[string][]string{
		"Foo": {}, // no downstreams
	}
)

// helper to make a fresh PriceTable per test
func newTestRajomon() *PriceTable {
	return NewRajomon("node-1", defaultCallMap, defaultOpts)
}

func TestUnaryInterceptor_RejectsLowTokens(t *testing.T) {
	// Build the config exactly as specified
	pt := newTestRajomon()

	// Manually seed the price table: ownprice=100, method price=50
	pt.priceTableMap.Store("ownprice", int64(100))
	// pt.priceTableMap.Store("Foo", int64(50))

	// Create a context as if a client sent tokens=10 < price(100,50)->max=100
	md := metadata.Pairs(
		"tokens", "10",
		"method", "Foo",
	)
	ctx := metadata.NewIncomingContext(context.Background(), md)

	// A dummy handler that should never be called on rejection
	called := false
	handler := func(ctx context.Context, req interface{}) (interface{}, error) {
		called = true
		return "SHOULD_NOT_REACH", nil
	}

	resp, err := pt.UnaryInterceptor(
		ctx,
		nil,
		&grpc.UnaryServerInfo{FullMethod: "Foo"},
		handler,
	)
	if err == nil {
		t.Fatalf("expected error; got resp=%v", resp)
	}
	if status.Code(err) != codes.ResourceExhausted {
		t.Fatalf("expected ResourceExhausted; got %v", err)
	}
	if called {
		t.Error("handler was called despite insufficient tokens")
	}
}

func TestUnaryInterceptor_AllowsHighTokens(t *testing.T) {

	pt := newTestRajomon()

	// ownprice=10, method price=5 so max=10
	pt.priceTableMap.Store("ownprice", int64(10))
	// pt.priceTableMap.Store("Foo", int64(5))

	// tokens=20 >= price(10,5)->max=10
	md := metadata.Pairs(
		"tokens", "20",
		"method", "Foo",
	)
	ctx := metadata.NewIncomingContext(context.Background(), md)

	// Dummy handler should be called and return its value
	handler := func(ctx context.Context, req interface{}) (interface{}, error) {
		return "OKAY", nil
	}

	resp, err := pt.UnaryInterceptor(
		ctx,
		nil,
		&grpc.UnaryServerInfo{FullMethod: "Foo"},
		handler,
	)
	if err != nil {
		t.Fatalf("expected no error; got %v", err)
	}
	if resp != "OKAY" {
		t.Errorf("unexpected resp: %v; want %q", resp, "OKAY")
	}
}

func TestUnaryInterceptor_MixedTokens(t *testing.T) {
	pt := newTestRajomon()
	pt.priceTableMap.Store("ownprice", int64(10))
	// pt.priceTableMap.Store("Foo", int64(2))

	var rejected, accepted int
	for i := 0; i < 10; i++ {
		tokenVal := 3
		if i >= 5 {
			tokenVal = 20
		}
		md := metadata.Pairs("tokens", strconv.Itoa(tokenVal), "method", "Foo")
		ctx := metadata.NewIncomingContext(context.Background(), md)

		called := false
		handler := func(ctx context.Context, req interface{}) (interface{}, error) {
			called = true
			return "OK", nil
		}

		resp, err := pt.UnaryInterceptor(
			ctx, nil, &grpc.UnaryServerInfo{FullMethod: "Foo"}, handler,
		)
		if i < 5 {
			if status.Code(err) != codes.ResourceExhausted {
				t.Errorf("iteration %d: expected rejection; got resp=%v, err=%v", i, resp, err)
			}
			rejected++
		} else {
			if err != nil {
				t.Errorf("iteration %d: expected acceptance; got err=%v", i, err)
			}
			if resp != "OK" {
				t.Errorf("iteration %d: unexpected resp=%v; want %q", i, resp, "OK")
			}
			if !called {
				t.Errorf("iteration %d: handler was not called for high token", i)
			}
			accepted++
		}
	}
	if rejected != 5 || accepted != 5 {
		t.Errorf("mixed tokens: got rejected=%d, accepted=%d; want 5 and 5", rejected, accepted)
	}
}

// TestDownstreamPrice_StorageAndRetrieval tests storing and retrieving downstream prices
func TestDownstreamPrice_StorageAndRetrieval(t *testing.T) {
	// Define a callMap with downstream services
	callMap := map[string][]string{
		"Bar": {"X", "Y"},
	}
	pt := NewRajomon("node-2", callMap, defaultOpts)

	// Initially downstream price not set
	downstreamPrice, err := pt.RetrieveDSPrice(context.Background(), "Bar")
	if downstreamPrice != 0 {
		t.Errorf("expected 0 downstream price; got %d", downstreamPrice)
	}
	if err != nil {
		t.Fatalf("RetrieveDSPrice error: %v", err)
	}

	// Update downstream price for service Bar->X
	newPriceX := int64(15)
	price, err := pt.UpdateDownstreamPrice(context.Background(), "Bar", "X", newPriceX)
	if err != nil {
		t.Fatalf("UpdateDownstreamPrice error: %v", err)
	}
	if price != newPriceX {
		t.Errorf("returned price = %d; want %d", price, newPriceX)
	}

	// RetrieveDSPrice should now reflect the updated price
	got, err := pt.RetrieveDSPrice(context.Background(), "Bar")
	if err != nil {
		t.Fatalf("RetrieveDSPrice error: %v", err)
	}
	if got != newPriceX {
		t.Errorf("RetrieveDSPrice = %d; want %d", got, newPriceX)
	}

	// Update downstream price smaller than existing (for Y), should keep max
	// First set Y lower
	_, _ = pt.UpdateDownstreamPrice(context.Background(), "Bar", "Y", int64(5))
	got2, err := pt.RetrieveDSPrice(context.Background(), "Bar")
	if got2 != newPriceX {
		t.Errorf("after lower update, DSPrice = %d; want %d (max)", got2, newPriceX)
	}
	if err != nil {
		t.Fatalf("RetrieveDSPrice error: %v", err)
	}
	// Now set Y higher
	_, _ = pt.UpdateDownstreamPrice(context.Background(), "Bar", "Y", int64(20))
	got3, err := pt.RetrieveDSPrice(context.Background(), "Bar")
	if got3 != int64(20) {
		t.Errorf("after higher update, DSPrice = %d; want %d (max)", got3, int64(20))
	}
	if err != nil {
		t.Fatalf("RetrieveDSPrice error: %v", err)
	}
}

// TestLoadShedding_ReturnsCorrectPrice verifies LoadShedding returns the correct price string
func TestLoadShedding_ReturnsCorrectPrice(t *testing.T) {
	pt := newTestRajomon()
	pt.priceTableMap.Store("ownprice", int64(7))

	tokens, price, err := pt.LoadShedding(context.Background(), 20, "Foo")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if price != "7" {
		t.Errorf("price = %q; want '7'", price)
	}
	// tokens left = 20 - price(7)
	if tokens != 13 {
		t.Errorf("tokens left = %d; want 13", tokens)
	}
}
