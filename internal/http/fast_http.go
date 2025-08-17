package http

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"time"

	"github.com/valyala/fasthttp"
)

type HostType string

const (
	NilHost      = "nil"
	DefaultHost  = "default"
	FallbackHost = "fallback"
)

var (
	ErrAlreadyProcessed    = errors.New("Request has already been processed")
	ErrInternalServerError = errors.New("Server responded with status 500")
)

type HTTPHost struct {
	client  *fasthttp.HostClient
	postURI *fasthttp.URI
}

type FastHTTPClient struct {
	defaultHost  *HTTPHost
	fallbackHost *HTTPHost
}

type HostCfg struct {
	Addr     string
	Endpoint string
}

func NewFastHTTPClient(defaultCfg *HostCfg, fallbackCfg *HostCfg) *FastHTTPClient {
	defaultPostURI := fasthttp.AcquireURI()
	defaultPostURI.Parse(nil, []byte(defaultCfg.Endpoint))

	fallbackPostURI := fasthttp.AcquireURI()
	fallbackPostURI.Parse(nil, []byte(fallbackCfg.Endpoint))

	return &FastHTTPClient{
		// docs: https://github.com/valyala/fasthttp/blob/dab027680cc57d7c2749ba018a72f8b943f473cc/client.go#L265
		defaultHost: &HTTPHost{
			client:  &fasthttp.HostClient{Addr: defaultCfg.Addr, MaxConns: 2048},
			postURI: defaultPostURI,
		},
		fallbackHost: &HTTPHost{
			client:  &fasthttp.HostClient{Addr: fallbackCfg.Addr, MaxConns: 2048},
			postURI: fallbackPostURI,
		},
	}
}

func doWithContext(ctx context.Context, host *HTTPHost, req *fasthttp.Request, resp *fasthttp.Response) error {
	done := make(chan error, 1)

	go func() {
		done <- host.client.Do(req, resp)
	}()

	select {
	case err := <-done:
		if err != nil {
			log.Printf("HTTP client error: %s\n", err.Error())
		}
		return err
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (c *FastHTTPClient) getHost(hostType HostType) (*HTTPHost, error) {
	switch hostType {
	case DefaultHost:
		return c.defaultHost, nil
	case FallbackHost:
		return c.fallbackHost, nil
	default:
		return nil, fmt.Errorf("invalid host type: %v", hostType)
	}
}

func (c *FastHTTPClient) Close() {
	if c.defaultHost != nil {
		if c.defaultHost.postURI != nil {
			fasthttp.ReleaseURI(c.defaultHost.postURI)
		}
	}

	if c.fallbackHost != nil {
		if c.fallbackHost.postURI != nil {
			fasthttp.ReleaseURI(c.fallbackHost.postURI)
		}
	}
}

func (c *FastHTTPClient) POST(ctx context.Context, host HostType, body []byte) (int64, error) {
	httpHost, err := c.getHost(host)
	if err != nil {
		return 0, err
	}

	req := fasthttp.AcquireRequest()
	req.SetURI(httpHost.postURI)
	req.Header.SetMethod(fasthttp.MethodPost)
	req.Header.SetContentType("application/json; charset=utf-8")
	req.SetBody(body)

	res := fasthttp.AcquireResponse()
	defer fasthttp.ReleaseResponse(res)

	start := time.Now().UnixNano()
	err = doWithContext(ctx, httpHost, req, res)
	responseTime := time.Now().UnixNano() - start

	fasthttp.ReleaseRequest(req)

	if err != nil {
		return 0, err
	}

	respStatus := res.StatusCode()
	if respStatus >= 200 && respStatus < 300 {
		return responseTime, nil
	}

	if res.StatusCode() == http.StatusUnprocessableEntity {
		log.Println("***************ALREADY PROCESSED***************")
		return 0, ErrAlreadyProcessed
	}

	if respStatus == http.StatusInternalServerError {
		return 0, ErrInternalServerError
	}

	log.Printf("RESPONSE ERROR STATUS: %v\n", respStatus)
	return 0, fmt.Errorf("POST request failed with status %v", respStatus)
}
