package llm

import (
	"net"
	"net/http"
	"time"
)

// newProviderTransport builds the HTTP transport every LLM provider uses.
//
// The deadlines here are the only thing standing between a flaky network and a
// permanently wedged agent. The failure they guard against is NOT a clean
// refusal — a refused connection returns an error immediately and the pipeline
// recovers. It's the silent one: a captive portal, a DNS black-hole, or Wi-Fi
// dropping without sending a RST. The socket opens (or appears to) and then
// nothing ever arrives. IdleConnTimeout does not help, because a connection
// blocked waiting on a response is not idle.
//
// Deliberately NOT set: http.Client.Timeout. That caps the entire exchange
// including the response body read, which would truncate a legitimate SSE
// stream (StreamGenerateIntent) the moment it ran long. ResponseHeaderTimeout
// covers the same hang without ever applying to a stream that is already
// flowing; callers additionally bound the whole call with a context deadline.
func newProviderTransport() *http.Transport {
	return &http.Transport{
		DialContext: (&net.Dialer{
			Timeout:   10 * time.Second,
			KeepAlive: 30 * time.Second,
		}).DialContext,
		TLSHandshakeTimeout:   10 * time.Second,
		ResponseHeaderTimeout: 60 * time.Second,
		MaxIdleConns:          10,
		MaxIdleConnsPerHost:   2,
		IdleConnTimeout:       90 * time.Second,
	}
}
