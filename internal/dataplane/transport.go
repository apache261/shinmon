package dataplane

import (
	"crypto/tls"
	"crypto/x509"
	"errors"
	"net"
	"net/http"
	"os"
	"time"
)

// NewTransport creates the shared proxy and health-check transport. HTTPS
// always verifies the upstream certificate; private roots may be appended.
func NewTransport(caFile string) (*http.Transport, error) {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = nil
	transport.DialContext = (&net.Dialer{Timeout: 5 * time.Second, KeepAlive: 30 * time.Second}).DialContext
	transport.MaxIdleConns = 256
	transport.MaxIdleConnsPerHost = 32
	transport.IdleConnTimeout = 90 * time.Second
	transport.ResponseHeaderTimeout = 30 * time.Second
	transport.TLSClientConfig = &tls.Config{MinVersion: tls.VersionTLS12}
	if caFile == "" {
		return transport, nil
	}
	roots, err := x509.SystemCertPool()
	if err != nil {
		return nil, errors.New("load system certificate roots")
	}
	encoded, err := os.ReadFile(caFile)
	if err != nil || !roots.AppendCertsFromPEM(encoded) {
		return nil, errors.New("load upstream TLS CA bundle")
	}
	transport.TLSClientConfig.RootCAs = roots
	return transport, nil
}
