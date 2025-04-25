package rajomon

import (
	"context"
	"errors"
	"fmt"
	"log"

	"strconv"
	"sync"
	"time"

	"github.com/bytedance/gopkg/lang/fastrand"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

var (
	// InsufficientTokens is returned by the Limiter in case the number of requests overflows the capacity of a Limiter.
	InsufficientTokens = errors.New("Received insufficient tokens, trigger load shedding.")
	RateLimited        = errors.New("Insufficient tokens to send, trigger rate limit.")
	errMissingMetadata = status.Errorf(codes.InvalidArgument, "missing metadata")
	debug              = false
	atomicTokens       = false
	trackPrice         = false
)

// PriceTable implements the Rajomon price table
type PriceTable struct {
	// The following lockfree hashmap should contain total price, selfprice and downstream price
	// initprice is the price table's initprice.
	initprice          int64
	nodeName           string
	externalFetchURL   string
	callMap            map[string][]string
	priceTableMap      sync.Map
	rateLimiting       bool
	rateLimitWaiting   bool
	loadShedding       bool
	pinpointThroughput bool
	pinpointLatency    bool
	pinpointQueuing    bool
	rateLimiter        chan int64
	fakeInvoker        bool
	skipPrice          bool
	priceFreq          int64
	// updateRate is the rate at which price should be updated at least once.
	tokensLeft           int64
	tokenUpdateRate      time.Duration
	lastUpdateTime       time.Time
	lastRateLimitedTime  time.Time
	tokenUpdateStep      int64
	tokenRefillDist      string
	tokenStrategy        string
	priceStrategy        string
	throughputCounter    int64
	sampleDiv            uint32
	priceUpdateRate      time.Duration
	observedDelay        time.Duration
	clientTimeOut        time.Duration
	clientBackoff        time.Duration
	randomRateLimit      int64
	throughputThreshold  int64
	latencyThreshold     time.Duration
	priceStep            int64
	priceAggregation     string
	guidePrice           int64
	consecutiveIncreases int64
	decayRate            float64
	maxToken             int64
}

/*
Unblocks rateLimiter channel.
*/
func (pt *PriceTable) unblockRateLimiter() {
	select {
	case pt.rateLimiter <- 1:
		return
	default:
		return
	}
}

// RateLimiting is for the end user (human client) to check the price and ratelimit their calls when tokens < prices.
func (pt *PriceTable) RateLimiting(ctx context.Context, tokens int64, methodName string) error {
	servicePrice, _ := pt.RetrieveDSPrice(ctx, methodName)
	extratoken := tokens - servicePrice
	logger("[Ratelimiting]: Checking Request. Token is %d, %s price is %d\n", tokens, methodName, servicePrice)

	if extratoken < 0 {
		logger("[Prepare Req]: Request blocked for lack of tokens.")
		return RateLimited
	}
	return nil
}

// LoadShedding takes tokens from the request according to the price table,
// then it updates the price table according to the tokens on the req.
// It returns #token left from ownprice, and a nil error if the request has sufficient amount of tokens.
// and the total price of the request.
// It returns ErrLimitExhausted if the amount of available tokens is less than requested.
func (pt *PriceTable) LoadShedding(ctx context.Context, tokens int64, methodName string) (int64, string, error) {
	// if pt.loadShedding is false, then return tokens and nil error
	if !pt.loadShedding {
		totalPrice, _ := pt.RetrieveTotalPrice(ctx, methodName)
		return tokens, totalPrice, nil
	}

	ownPrice_string, ok := pt.priceTableMap.Load("ownprice")
	if !ok {
		return 0, "", status.Errorf(codes.Internal, "[loadshedding]: own price not found for %s", methodName)
	}
	ownPrice := ownPrice_string.(int64)
	downstreamPrice, err := pt.RetrieveDSPrice(ctx, methodName)
	if err != nil {
		logger("[LoadShedding]: Downstream price not found for %s with error %v\n", methodName, err)
		return 0, "", err
	}

	if pt.priceAggregation == "maximal" {

		logger("[Received Req]: Method %s, ownPrice is %d, downstreamPrice is %d.\n", methodName, ownPrice, downstreamPrice)

		// take the max of ownPrice and downstreamPrice
		if ownPrice < downstreamPrice {
			ownPrice = downstreamPrice
		}
		if tokens >= ownPrice {
			logger("[Performing AQM]: Request accepted. Token is %d, but price is %d\n", tokens, ownPrice)
			return tokens - ownPrice, strconv.FormatInt(ownPrice, 10), nil
		} else {
			logger("[Performing AQM]: Request rejected for lack of tokens. Token is %d, but price is %d\n", tokens, ownPrice)
			return 0, strconv.FormatInt(ownPrice, 10), InsufficientTokens
		}
	} else if pt.priceAggregation == "mean" {
		// use the mean of ownPrice and downstreamPrice as the final price
		totalPrice := (ownPrice + downstreamPrice) / 2
		logger("[Received Req]:	Total price is %d, ownPrice is %d downstream price is %d\n", totalPrice, ownPrice, downstreamPrice)
		if tokens >= totalPrice {
			logger("[Received Req]: Request accepted. Token is %d, but price is %d\n", tokens, totalPrice)
			return tokens - totalPrice, strconv.FormatInt(totalPrice, 10), nil
		} else {
			logger("[Received Req]: Request rejected for lack of tokens. Token is %d, but price is %d\n", tokens, totalPrice)
			return 0, strconv.FormatInt(totalPrice, 10), InsufficientTokens
		}
	} else if pt.priceAggregation == "additive" {
		totalPrice := ownPrice + downstreamPrice

		extratoken := tokens - totalPrice

		logger("[Received Req]:	Total price is %d, ownPrice is %d downstream price is %d\n", totalPrice, ownPrice, downstreamPrice)

		if extratoken < 0 {
			logger("[Received Req]: Request rejected for lack of tokens. ownPrice is %d downstream price is %d\n", ownPrice, downstreamPrice)
			return 0, strconv.FormatInt(totalPrice, 10), InsufficientTokens
		}

		if pt.pinpointThroughput || pt.priceStrategy == "co2" {
			pt.Increment()
		}

		// Take the tokens from the req.
		tokenleft := tokens - ownPrice

		return tokenleft, strconv.FormatInt(totalPrice, 10), nil
	}
	// raise error if the price aggregation is not supported
	return 0, "", status.Error(codes.Unimplemented, "price aggregation method not supported")
}

// unaryInterceptor is an example unary interceptor.
func (pt *PriceTable) UnaryInterceptorClient(ctx context.Context, method string, req, reply interface{}, cc *grpc.ClientConn, invoker grpc.UnaryInvoker, opts ...grpc.CallOption) error {
	// before sending. check the price, calculate the #tokens to add to request, update the total tokens
	// overwrite rather than append to the header with the node name of this client
	var header metadata.MD // variable to store header and trailer
	err := invoker(ctx, method, req, reply, cc, grpc.Header(&header))

	// run the following code asynchorously, without blocking the main thread.
	// after replied. update and store the price info for future
	if len(header["price"]) > 0 {
		priceDownstream, _ := strconv.ParseInt(header["price"][0], 10, 64)
		md, _ := metadata.FromOutgoingContext(ctx)
		methodName := md["method"][0]
		pt.UpdateDownstreamPrice(ctx, methodName, header["name"][0], priceDownstream)
		logger("[After Resp]:	The price table is from %s\n", header["name"])
	} else {
		logger("[After Resp]:	No price table received\n")
	}

	return err
}

// unaryInterceptor is an example unary interceptor.
func (pt *PriceTable) UnaryInterceptorEnduser(ctx context.Context, method string, req, reply interface{}, cc *grpc.ClientConn, invoker grpc.UnaryInvoker, opts ...grpc.CallOption) error {

	md, ok := metadata.FromOutgoingContext(ctx)
	if !ok {
		return errMissingMetadata
	}

	methodName := md["method"][0]
	// print all the k-v pairs in the metadata md
	if debug {
		logger("[Before Req]:	Node %s calling %s\n", pt.nodeName, methodName)
		var metadataLog string
		for k, v := range md {
			metadataLog += fmt.Sprintf("%s: %s, ", k, v)
		}
		if metadataLog != "" {
			logger("[Sending Req Enduser]: The metadata for request is %s\n", metadataLog)
		}
	}

	// If `randomRateLimit` is greater than 0, then we randomly drop requests based on the last digit of `request-id` in md
	// This functionality is used for debugging purposes mostly, not recommended for production use and not enabled by default.
	if pt.randomRateLimit > 0 {
		// get the request-id from the metadata
		if requestIDs, found := md["request-id"]; found && len(requestIDs) > 0 {
			reqid, err := strconv.ParseInt(requestIDs[0], 10, 64)
			if err != nil {
				// Error parsing request ID, handle accordingly
				panic(err)
			}
			// take the last two digits of the requestIDs
			lastDigit := reqid % 100
			// if the last digit is smaller than the randomRateLimit, then drop the request
			if lastDigit < pt.randomRateLimit {
				logger("[Random Drop]:	The request is dropped randomly.")
				if pt.fakeInvoker {
					opts = append(opts, grpc.MaxCallSendMsgSize(0))
					_ = invoker(ctx, method, req, reply, cc, opts...)
				}
				return status.Error(codes.ResourceExhausted, "Client is rate limited, req dropped randomly.")
			}
		}
	}

	// If clientbackoff is greater than 0, then we check the time duration since the last RateLimited error
	// If the time duration is less than clientbackoff, then we drop the request without waiting.
	// This functionality is unnecessary because the price update of Rajomon rate limits the enduser already, so it is not enabled by default.
	if pt.clientBackoff > 0 && time.Since(pt.lastRateLimitedTime) < pt.clientBackoff {
		if !pt.rateLimitWaiting {
			logger("[Backoff Triggered]:	Client is rate limited, req dropped without waiting.")
			// the request is dropped without waiting in this scenario, but we want to return an error to the client
			// to do this, we use a fake invoker without actually sending the request to the server
			// Invoke the gRPC method with the new callOptions: MaxCallSendMsgSize as 0 append to opts.
			if pt.fakeInvoker {
				// this fake invoker has very high overhead, so it is not recommended to use it in production, and not enabled by default.
				opts = append(opts, grpc.MaxCallSendMsgSize(0))
				_ = invoker(ctx, method, req, reply, cc, opts...)
			}
			return status.Error(codes.ResourceExhausted, "Client is rate limited, req dropped without waiting.")
		}
	}

	var tok int64
	// Set a timer for the client to timeout if it has been waiting for too long.
	startTime := time.Now()
	// before sending. check the price, calculate the #tokens to add to request, update the total tokens
	for {
		// if waiting for longer than ClientTimeout, return error RateLimited
		if pt.rateLimiting && pt.rateLimitWaiting && time.Since(startTime) > pt.clientTimeOut {
			logger("[Client Timeout]:	Client timeout waiting for tokens.\n")
			// Invoke the gRPC method with the new callOptions: MaxCallSendMsgSize as 0
			// append to opts
			if pt.fakeInvoker {
				opts = append(opts, grpc.MaxCallSendMsgSize(0))
				_ = invoker(ctx, method, req, reply, cc, opts...)
			}
			return status.Errorf(codes.DeadlineExceeded, "Client timeout waiting for tokens.")
		}

		tok = pt.GetTokensLeft()

		if pt.tokenStrategy == "uniform" {
			// set the tok to be a uniform random number between 0 and tokensLeft
			if tok > 0 {
				// set the tok to be a uniform random number between 0 and tokensLeft-1
				tok = fastrand.Int63n(tok)
			}
		}

		if !pt.rateLimiting {
			break
		}
		ratelimit := pt.RateLimiting(ctx, tok, methodName)
		// if clientBackoff is greater than 0, update the lastRateLimitedTime
		if pt.clientBackoff > 0 {
			if ratelimit == RateLimited && time.Since(pt.lastRateLimitedTime) > pt.clientBackoff {
				logger("[Backoff Started]:	Client has been rate limited, backoff started.\n")
				pt.lastRateLimitedTime = time.Now()
			}
		}

		if ratelimit == RateLimited {
			if !pt.rateLimitWaiting {
				logger("[Rate Limited]:	Client is rate limited, req dropped without waiting.\n")
				// the request is dropped without waiting in this scenario, but we want to return an error to the client
				// to do this, we use a fake invoker without actually sending the request to the server
				// Invoke the gRPC method with the new callOptions: MaxCallSendMsgSize as 0
				// append to opts
				if pt.fakeInvoker {
					opts = append(opts, grpc.MaxCallSendMsgSize(0))
					_ = invoker(ctx, method, req, reply, cc, opts...)
				}
				return status.Error(codes.ResourceExhausted, "Client is rate limited, req dropped without waiting.")
			}
			<-pt.rateLimiter
			// log the waiting time so far for client and how long until timeout
			logger("[Rate Limited]:	Client has been rate limited for %d ms, %d ms left until timeout.\n",
				time.Since(startTime).Milliseconds(), (pt.clientTimeOut - time.Since(startTime)).Milliseconds())
		} else {
			break
		}

		if pt.DeductTokens(tok) {
			logger("[Prepare Req]:	%d tokens deducted from client.\n", tok)
			break
		} else {
			logger("[Prepare Req]:	not enough tokens left for tok %d, no tokens deducted from client.\n", tok)
			if !pt.rateLimitWaiting {
				logger("[Rate Limited]:	Client is rate limited, req dropped without waiting.\n")
				if pt.fakeInvoker {
					opts = append(opts, grpc.MaxCallSendMsgSize(0))
					_ = invoker(ctx, method, req, reply, cc, opts...)
				}
				return status.Error(codes.ResourceExhausted, "Client is rate limited, req dropped without waiting.")
			}
			<-pt.rateLimiter
			logger("[Rate Limited]:	Client has been rate limited for %d ms, %d ms left until timeout.\n",
				time.Since(startTime).Milliseconds(), (pt.clientTimeOut - time.Since(startTime)).Milliseconds())
		}
	}

	tok_string := strconv.FormatInt(tok, 10)
	ctx = metadata.AppendToOutgoingContext(ctx, "tokens", tok_string, "name", pt.nodeName)

	var header metadata.MD // variable to store header and trailer
	err := invoker(ctx, method, req, reply, cc, grpc.Header(&header))

	// after replied. update and store the price info for future
	if len(header["price"]) > 0 {
		priceDownstream, _ := strconv.ParseInt(header["price"][0], 10, 64)
		pt.UpdateDownstreamPrice(ctx, methodName, header["name"][0], priceDownstream)
		logger("[After Resp]:	The price table is from %s\n", header["name"])
	} else {
		logger("[After Resp]:	No price table received\n")
	}
	// }()
	return err
}

func (pt *PriceTable) UnaryInterceptor(ctx context.Context, req interface{}, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (interface{}, error) {
	// This is the server side interceptor, it should check tokens, update price, do overload handling and attach price to response

	md, ok := metadata.FromIncomingContext(ctx)
	if !ok {
		return nil, errMissingMetadata
	}

	if debug {
		var metadataLog string
		for k, v := range md {
			metadataLog += fmt.Sprintf("%s: %s, ", k, v)
		}
		if metadataLog != "" {
			logger("[Received Req]: The metadata for request is %s\n", metadataLog)
		}
	}

	// overload handler, do AQM, deduct the tokens on the request, update price info
	var tok int64
	var err error
	// if the price are additive, then the tokens are stored in the "tokens" or tokens-nodeName field of the metadata
	if pt.priceAggregation == "additive" {
		if val, ok := md["tokens-"+pt.nodeName]; ok {
			// logger("[Received Req]:	tokens for %s are %s\n", pt.nodeName, val)
			// raise error if the val length is not 1
			if len(val) > 1 {
				return nil, status.Errorf(codes.InvalidArgument, "duplicated tokens")
			} else if len(val) == 0 {
				return nil, errMissingMetadata
			}
			tok, _ = strconv.ParseInt(val[0], 10, 64)
		} else {
			logger("[Received Req]:	tokens are %s\n", md["tokens"])
			// raise error if the tokens length is not 1
			if len(md["tokens"]) > 1 {
				return nil, status.Errorf(codes.InvalidArgument, "duplicated tokens")
			} else if len(md["tokens"]) == 0 {
				return nil, errMissingMetadata
			}
			tok, _ = strconv.ParseInt(md["tokens"][0], 10, 64)
		}
	} else if pt.priceAggregation == "maximal" || pt.priceAggregation == "mean" {
		// if the price are maximal, then the tokens are stored in the "tokens" field of the metadata
		if val, ok := md["tokens"]; ok {
			// raise error if the val length is not 1
			if len(val) > 1 {
				return nil, status.Errorf(codes.InvalidArgument, "duplicated tokens")
			} else if len(val) == 0 {
				return nil, errMissingMetadata
			}
			tok, _ = strconv.ParseInt(val[0], 10, 64)
		}
	}

	// overload handler:
	methodName := md["method"][0]
	tokenleft, price_string, err := pt.LoadShedding(ctx, tok, methodName)
	if err == InsufficientTokens && pt.loadShedding {
		// Trying to send the header to the client stochastically, to avoid abrupt over rate limiting that may cause goodput to drop.
		// with a probability of 1/5, send the price on error response to the client.
		if tok%pt.priceFreq == 0 {
			logger("[Sending Error Resp]:	Total price is %s\n", price_string)
			header := metadata.Pairs("price", price_string, "name", pt.nodeName)
			grpc.SendHeader(ctx, header)
		}

		return nil, status.Errorf(codes.ResourceExhausted, "%s req dropped by %s. Try again later.", methodName, pt.nodeName)
	}
	if err != nil && err != InsufficientTokens {
		// The limiter failed. This error should be logged and examined.
		log.Println(err)
		return nil, err
	}
	if pt.priceAggregation == "additive" {
		// we need to attach the token info to the context, so that the downstream can retrieve it.
		// we actually need multiple kv pairs for the token information, because one context is sent to multiple downstreams.
		downstreamTokens, _ := pt.SplitTokens(ctx, tokenleft, methodName)
		ctx = metadata.AppendToOutgoingContext(ctx, downstreamTokens...)

	}

	m, err := handler(ctx, req)

	// Attach the price info to response before sending
	// right now let's just propagate the corresponding price of the RPC method rather than a whole pricetable.
	// if not pt.skipPrice, then attach the price to the response with a probability of 1/5
	if !pt.skipPrice {
		if tok%pt.priceFreq == 0 {
			header := metadata.Pairs("price", price_string, "name", pt.nodeName)
			logger("[Preparing Resp]:	Total price of %s is %s\n", methodName, price_string)
			grpc.SendHeader(ctx, header)
		}
	} else {
		logger("[Preparing Resp]:	Lazy response is enabled, no price attached to response.\n")
	}

	if err != nil {
		logger("RPC failed with error %v", err)
	}
	return m, err
}
