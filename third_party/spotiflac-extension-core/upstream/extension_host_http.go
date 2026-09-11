package gobackend

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"syscall"
	"time"
)

func userAgentForURL(u *url.URL) string {
	if u != nil && strings.EqualFold(strings.TrimSpace(u.Hostname()), "api.zarz.moe") {
		return appUserAgent()
	}
	return getRandomUserAgent()
}

func newCoreHTTPClient(transport *http.Transport, timeout time.Duration) *http.Client {
	client := &http.Client{Transport: transport, Timeout: timeout}
	client.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		if req.URL == nil || req.URL.User != nil || req.URL.Scheme != "https" {
			return fmt.Errorf("redirect blocked: only https URLs without embedded credentials are allowed")
		}
		host := req.URL.Hostname()
		if host == "" {
			return fmt.Errorf("redirect blocked: hostname is required")
		}
		if isPrivateIP(host) {
			return fmt.Errorf("redirect blocked: private/local host '%s' is not allowed", host)
		}
		if len(via) >= 10 {
			return http.ErrUseLastResponse
		}
		return nil
	}
	return client
}

func NewHTTPClientWithTimeout(timeout time.Duration) *http.Client {
	return newCoreHTTPClient(sharedTransport, timeout)
}

func NewMetadataHTTPClient(timeout time.Duration) *http.Client {
	return newCoreHTTPClient(extensionAPITransport, timeout)
}

func isTransientNetworkError(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
		return true
	}
	var netErr net.Error
	return errors.As(err, &netErr) && (netErr.Timeout() || netErr.Temporary())
}

func isConnectivityFailure(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, io.ErrUnexpectedEOF) {
		return true
	}
	var urlErr *url.Error
	if errors.As(err, &urlErr) {
		if urlErr.Timeout() || (urlErr.Err != nil && isConnectivityFailure(urlErr.Err)) {
			return true
		}
	}
	var dnsErr *net.DNSError
	if errors.As(err, &dnsErr) && (dnsErr.IsNotFound || dnsErr.IsTimeout || dnsErr.IsTemporary) {
		return true
	}
	var opErr *net.OpError
	if errors.As(err, &opErr) && opErr.Timeout() {
		return true
	}
	var errno syscall.Errno
	return errors.As(err, &errno) && (errno == syscall.ECONNRESET || errno == syscall.ECONNREFUSED || errno == syscall.ETIMEDOUT)
}

