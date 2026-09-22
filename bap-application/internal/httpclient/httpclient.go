// Package httpclient provides an *http.Client with HTTP/2 explicitly
// disabled, for outbound calls to the onix adapter. Go's default transport
// auto-negotiates HTTP/2 over TLS, and its multiplexed persistent
// connections have hit real connection-lifecycle edge cases against Cloud
// Run's front-end (a pooled connection surviving past the point its peer
// instance scaled to zero and got recycled, then failing instantly on
// reuse instead of being re-dialed) -- surfacing as a stream-level
// protocol error with nothing logged server-side, rather than a clean
// retry. These are discrete, low-throughput calls -- HTTP/1.1 has no
// meaningful downside here and doesn't have this failure mode; a broken
// HTTP/1.1 connection just fails cleanly and Go's transport transparently
// opens a new one.
package httpclient

import (
	"crypto/tls"
	"net/http"
	"time"
)

func New(timeout time.Duration) *http.Client {
	return &http.Client{
		Timeout: timeout,
		Transport: &http.Transport{
			TLSNextProto: make(map[string]func(authority string, c *tls.Conn) http.RoundTripper),
		},
	}
}
