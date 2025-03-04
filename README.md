
This is the official repository for [Rajomon](https://www.usenix.org/conference/nsdi25/presentation/xing). It contains a go-gRPC interceptor package. Please find [other overload control packages here](https://github.com/orgs/pennsail/repositories). 

# Rajomon: Overload Control for Microservices
This document explains the design and implementation of Rajomon, a *Decentralized and Coordinated Overload Control for Latency-Sensitive Microservice*. 

Overload control is an important feature of modern cloud applications. As these applications grow increasingly complex, designing efficient overload control schemes at scale is tedious. In this paper we argue part of the challenge is a lack of first principles mechanisms one can use to design scalable and verifiable policies.

We present Rajomon, a market-based scheme for large scale service graphs. Unsurprisingly, Rajomon relies on tokens to negotiate the acquisition of compute resources. However, unlike existing receiver-driven systems, Rajomon decouples the mechanisms used to generate and value tokens and proposes efficient price propagation mechanisms. We motivate Rajomon with a set of representative system conditions that existing frameworks cannot handle well, detail an example policy one can build using the proposed mechanisms, and discuss open research challenges our framework exposes.


### Motivation
Exisitng overload control systems are not designed for microservices graphs. Overload control for microservices is a difficult problem because microservices serve heterogeneous requests over diverse topologies.

### Design
Rajomon attach tokens to each requests and sub requests to indicate the importance of the req. Rajomon also attach a price to each RPC method to indicate the cost of the req based on the congestion level. 
Req are supposed to be dropped at services when the token is less than the price of the API it calls. Clients are supposed to hold back req when the token is less than the price of the API it calls.
Rajomon adjusts the prices based on the performance of the microservice.

Token and price are piggybacked on req and resp, respectively, to allow for distributed overload control.

### Implementation
Rajomon is implemented as a library that can be used by any microservice. It is implemented in Go and uses gRPC for communication between microservices. 

## rajomon.go
the main implementation of Rajomon via interceptors, there's 3 interceptors: UnaryInterceptorClient, UnaryInterceptorEnduser, and UnaryInterceptor. Among which the first two are for client side and the last one is for server side. 
- In UnaryInterceptorClient, it intercepts the outgoing request and incoming response, it reads the price from resp and store it. 
- In UnaryInterceptorEnduser, it intercepts the outgoing request and incoming response, it checks the price for ratelimiting, if not enough token, it either waits for more tokens or just aborts immediately, and it also updates the server price after hear back.
- In UnaryInterceptor, it intercepts both incoming and request and outgoing response. First, it performs the load shedding, if the token is not enough, it droppes the request. Then, it sends back the updated price to the caller.

## initOptions.go
the implementation of the options for Rajomon, including all the configurations e.g., the token update rate, the price update criteria, etc.

## tokenAndPrice.go
the code for saving and retrieving the price from price table.

## overloadDetection.go
it is used to detect the overload of the server, it runs periodically and checks for queueing delay or throughputs. If the server is overloaded, it will increase the price.

## queuingDelay.go
it is used to calculate the queuing delay of the server, it provides the max queue delay and the average queuing delay.
