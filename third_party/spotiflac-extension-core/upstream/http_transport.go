package gobackend

import (
	"crypto/tls"
	"net"
	"net/http"
	"time"
)

var transportDialer = &net.Dialer{
	Timeout:   10 * time.Second,
	KeepAlive: 30 * time.Second,
}

var sharedTransport = &http.Transport{
	DialContext:           transportDialer.DialContext,
	MaxIdleConns:          100,
	MaxIdleConnsPerHost:   10,
	MaxConnsPerHost:       20,
	IdleConnTimeout:       60 * time.Second,
	TLSHandshakeTimeout:   10 * time.Second,
	ResponseHeaderTimeout: 120 * time.Second,
	ExpectContinueTimeout: 1 * time.Second,
	DisableKeepAlives:     false,
	ForceAttemptHTTP2:     true,
	WriteBufferSize:       64 * 1024,
	ReadBufferSize:        64 * 1024,
	DisableCompression:    true,
	TLSClientConfig: &tls.Config{
		MinVersion: tls.VersionTLS12,
	},
}

var extensionAPITransport = &http.Transport{
	DialContext:           transportDialer.DialContext,
	MaxIdleConns:          100,
	MaxIdleConnsPerHost:   10,
	MaxConnsPerHost:       20,
	IdleConnTimeout:       60 * time.Second,
	TLSHandshakeTimeout:   10 * time.Second,
	ResponseHeaderTimeout: 45 * time.Second,
	ExpectContinueTimeout: 1 * time.Second,
	DisableKeepAlives:     false,
	ForceAttemptHTTP2:     true,
	WriteBufferSize:       64 * 1024,
	ReadBufferSize:        64 * 1024,
	DisableCompression:    false,
	TLSClientConfig: &tls.Config{
		MinVersion: tls.VersionTLS12,
	},
}
