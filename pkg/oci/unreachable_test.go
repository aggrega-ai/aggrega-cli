// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package oci_test

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"syscall"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/oci"
)

// closedLoopbackAddress returns a loopback address nothing listens on: a port
// that was bound and released, so a connection to it is refused by this machine
// rather than sent anywhere.
func closedLoopbackAddress(t *testing.T) string {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("bind a loopback port: %v", err)
	}
	addr := l.Addr().String()
	if err := l.Close(); err != nil {
		t.Fatalf("release the loopback port: %v", err)
	}
	return addr
}

// asAClientSeesIt wraps a transport failure the way the HTTP client and the
// registry operations above it do, so each row is read through the chain a
// caller actually holds rather than as a bare value.
func asAClientSeesIt(cause error) error {
	return fmt.Errorf("search registry: %w",
		&url.Error{Op: "Get", URL: "https://registry.example.test/v2/_catalog", Err: cause})
}

// TestUnreachableReadsTheTransportFailureNotItsWords holds the one reading of
// "could not be reached" to the transport's own values.
//
// Every row carries one failure. The rows that report true are the three ways a
// transport cannot be established — a name that did not resolve, a connection
// that could not be opened, a secure connection that could not be set up — and
// two of them are produced by a real loopback connection rather than built by
// hand, so the shape the table models is the shape the standard library returns
// on this machine. The rows that report false are the near misses: an answered
// failure, the very words of a refused connection with no value behind them, a
// timeout that carries no record of which step it interrupted, a failure after
// the connection was open, a caller that withdrew, and the network-unreachable
// sentinel standing alone, which is a classification and not a transport value.
func TestUnreachableReadsTheTransportFailureNotItsWords(t *testing.T) {
	t.Parallel()

	for _, row := range []struct {
		name string
		err  func(t *testing.T) error
		want bool
	}{
		{
			name: "control: no failure at all is not unreachable",
			err:  func(*testing.T) error { return nil },
			want: false,
		},
		{
			name: "a registry that answered with a server error was reached",
			err: func(*testing.T) error {
				return errors.New("registry.example.test answered the request for its trust policy with HTTP 500")
			},
			want: false,
		},
		{
			name: "a name that did not resolve could not be reached",
			err: func(*testing.T) error {
				return asAClientSeesIt(&net.OpError{Op: "dial", Net: "tcp", Err: &net.DNSError{
					Err: "no such host", Name: "registry.example.test", IsNotFound: true,
				}})
			},
			want: true,
		},
		{
			name: "a resolver failure on its own could not be reached",
			err: func(*testing.T) error {
				return &net.DNSError{Err: "server misbehaving", Name: "registry.example.test", IsTemporary: true}
			},
			want: true,
		},
		{
			name: "a connection refused by a real loopback port could not be reached",
			err: func(t *testing.T) error {
				resp, err := (&http.Client{}).Get("http://" + closedLoopbackAddress(t) + "/v2/_catalog")
				if resp != nil {
					_ = resp.Body.Close()
				}
				if err == nil {
					t.Fatal("a request to a released loopback port was answered, so this row has nothing to read")
				}
				return fmt.Errorf("search registry: %w", err)
			},
			want: true,
		},
		{
			name: "a connection through a proxy that could not be opened could not be reached",
			err: func(*testing.T) error {
				return asAClientSeesIt(&net.OpError{Op: "proxyconnect", Net: "tcp", Err: syscall.ECONNREFUSED})
			},
			want: true,
		},
		{
			name: "a certificate this machine does not trust, met on a real loopback handshake, could not be reached",
			err: func(t *testing.T) error {
				srv := httptest.NewTLSServer(http.NotFoundHandler())
				t.Cleanup(srv.Close)
				resp, err := (&http.Client{}).Get(srv.URL + "/v2/_catalog")
				if resp != nil {
					_ = resp.Body.Close()
				}
				if err == nil {
					t.Fatal("a handshake against an untrusted certificate completed, so this row has nothing to read")
				}
				return err
			},
			want: true,
		},
		{
			name: "a first record that is not a handshake could not be reached",
			err: func(*testing.T) error {
				return asAClientSeesIt(tls.RecordHeaderError{Msg: "first record does not look like a TLS handshake"})
			},
			want: true,
		},
		{
			name: "an alert the registry sent to refuse the handshake could not be reached",
			err: func(*testing.T) error {
				return asAClientSeesIt(&net.OpError{Op: "remote error", Err: errors.New("tls: handshake failure")})
			},
			want: true,
		},
		{
			name: "a certificate refused for another host could not be reached",
			err: func(*testing.T) error {
				return asAClientSeesIt(&tls.CertificateVerificationError{Err: x509.HostnameError{Host: "registry.example.test"}})
			},
			want: true,
		},
		{
			name: "the words of a refused connection with no transport value behind them are not read",
			err: func(*testing.T) error {
				return errors.New(`search registry: Get "https://registry.example.test/v2/_catalog": dial tcp 127.0.0.1:443: connect: connection refused`)
			},
			want: false,
		},
		{
			name: "a client deadline that names no step it interrupted is not claimed",
			err: func(*testing.T) error {
				return &url.Error{Op: "Get", URL: "https://registry.example.test/.well-known/aggrega-trust",
					Err: errors.New("context deadline exceeded (Client.Timeout exceeded while awaiting headers)")}
			},
			want: false,
		},
		{
			name: "a connection reset after it was open was reached",
			err: func(*testing.T) error {
				return asAClientSeesIt(&net.OpError{Op: "read", Net: "tcp", Err: syscall.ECONNRESET})
			},
			want: false,
		},
		{
			name: "a dial the caller withdrew is not unreachable",
			err: func(*testing.T) error {
				return asAClientSeesIt(&net.OpError{Op: "dial", Net: "tcp", Err: context.Canceled})
			},
			want: false,
		},
		{
			name: "the network-unreachable sentinel standing alone is a classification, not a transport value",
			err: func(*testing.T) error {
				return fmt.Errorf("fetch: %w", oci.ErrFetchNetworkUnreachable())
			},
			want: false,
		},
	} {
		t.Run(row.name, func(t *testing.T) {
			t.Parallel()
			err := row.err(t)
			if got := oci.Unreachable(err); got != row.want {
				t.Errorf("Unreachable(%v) = %v; want %v", err, got, row.want)
			}
		})
	}
}
