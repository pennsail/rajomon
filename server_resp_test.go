// server_concurrency_test.go
package rajomon

import (
	"context"
	"log"
	"net"
	"sync"
	"testing"
	"time"

	pb "github.com/Jiali-Xing/protobuf" // replace with your actual import path
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

func busyLoop(c chan<- int, quit chan bool) {
	for {
		if <-quit {
			return
		}
	}
}

func computation(duration int) {
	// Jiali: the following block implements the fake computation
	quit := make(chan bool)
	busyChan := make(chan int)
	go busyLoop(busyChan, quit)
	select {
	case busyResult := <-busyChan:
		log.Println(busyResult)
	case <-time.After(time.Duration(duration) * time.Millisecond):
		// log.Println("timed out")
	}
	quit <- true
	return
}

// greetingServer is the minimal server with Rajomon AQM applied.
type greetingServer struct {
	pb.UnimplementedGreetingServiceServer
	pt *PriceTable
}

func (s *greetingServer) Greeting(ctx context.Context, req *pb.GreetingRequest) (*pb.GreetingResponse, error) {
	// simulate ~5ms of work per request
	computation(5000)

	// grab the incoming greeting
	incoming := req.GetGreeting()

	// create your new one
	serverGreet := &pb.Greeting{
		Service: "server", // or s.name, whatever
		// fill in any additional fields here
	}

	// combine them into the response slice
	resp := &pb.GreetingResponse{
		Greeting: []*pb.Greeting{
			incoming,
			serverGreet,
		},
	}
	return resp, nil
}

var (
	Opts = map[string]interface{}{
		"priceUpdateRate":  5000 * time.Microsecond,
		"tokenUpdateRate":  100000 * time.Microsecond,
		"latencyThreshold": 500 * time.Microsecond,
		"priceStep":        int64(180),
		"priceStrategy":    "expdecay",
		"lazyResponse":     false,
		"rateLimiting":     true,
		"loadShedding":     true,
		"pinpointQueuing":  true,
		"debug":            false,
	}
)

func TestHighConcurrencyPriceIncrease(t *testing.T) {
	// 1) Prepare Rajomon
	callMap := map[string][]string{"Greeting": {}}
	pt := NewRajomon("node-1", callMap, Opts)

	// 2) Start gRPC server on :50051
	lis, err := net.Listen("tcp", ":50051")
	if err != nil {
		t.Fatalf("listen failed: %v", err)
	}
	grpcServer := grpc.NewServer(
		grpc.UnaryInterceptor(pt.UnaryInterceptor),
	)
	pb.RegisterGreetingServiceServer(grpcServer, &greetingServer{pt: pt})
	go func() {
		if err := grpcServer.Serve(lis); err != nil {
			log.Fatalf("grpc serve error: %v", err)
		}
	}()
	defer grpcServer.Stop()

	// let server warm up
	time.Sleep(1 * time.Second)

	// 4) Fire N clients that loop until stop
	const concurrency = 1000
	const testDuration = 10 * time.Second

	stop := make(chan struct{})
	// 3) Dial clients and fire 100 concurrent RPCs
	var wg sync.WaitGroup
	for i := 0; i < concurrency; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			conn, err := grpc.Dial("localhost:50051", grpc.WithInsecure())
			if err != nil {
				t.Errorf("dial error: %v", err)
				return
			}
			defer conn.Close()

			client := pb.NewGreetingServiceClient(conn)
			// attach metadata so interceptor sees method name
			ctx := metadataAppend(context.Background(), "method", "Greeting", "tokens", "1000")
			// each client sends req indefinitely
			// until the end of the test
			for {
				select {
				case <-stop:
					return
				default:
					resp, err := client.Greeting(ctx, &pb.GreetingRequest{Greeting: &pb.Greeting{Service: "client"}})
					if err != nil {
						// we allow both success or ResourceExhausted here
						if status.Code(err) != codes.ResourceExhausted {
							t.Errorf("unexpected error: %v", err)
						}
					} else {
						// resp should not be nil
						if resp == nil {
							t.Errorf("Greeting response: nil")
						}
					}
				}
			}
		}()
	}

	time.Sleep(testDuration / 2)
	// print the price table
	// pt.priceTableMap.Range(func(key, value interface{}) bool {
	// 	if k, ok := key.(string); ok {
	// 		if v, valid := value.(int64); valid {
	// 			t.Logf("PriceTable: %s = %d", k, v)
	// 		} else {
	// 			t.Errorf("PriceTable: %s = %v (not int64)", k, value)
	// 		}
	// 	} else {
	// 		t.Errorf("PriceTable: %v (not string)", key)
	// 	}
	// 	return true
	// })

	// 5) Check that price > 0
	priceStr, err := pt.RetrieveTotalPrice(context.Background(), "Greeting")
	if err != nil {
		t.Fatalf("RetrieveTotalPrice error: %v", err)
	}
	if priceStr == "0" {
		t.Errorf("price did not increase under load, still %s", priceStr)
	} else {
		t.Logf("observed price after load: %s", priceStr)
	}
	// 4) Give Rajomon a moment to update price
	time.Sleep(testDuration / 2)
	// stop the clients
	close(stop)
	wg.Wait()

}

// metadataAppend is a small helper to add k/v pairs into context metadata
func metadataAppend(ctx context.Context, kv ...string) context.Context {
	md := metadata.Pairs(kv...)
	return metadata.NewOutgoingContext(ctx, md)
}
