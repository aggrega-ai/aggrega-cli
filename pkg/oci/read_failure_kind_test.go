// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package oci

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"testing"

	"oras.land/oras-go/v2/registry/remote/errcode"
)

// timeoutError is a network error that reports a timeout, as a dial or a read
// past its deadline reaches a client.
type timeoutError struct{}

func (timeoutError) Error() string   { return "i/o timeout" }
func (timeoutError) Timeout() bool   { return true }
func (timeoutError) Temporary() bool { return true }

var _ net.Error = timeoutError{}

// TestReadFailureKindNamesTheFailureFromItsValue proves each kind of failure a
// registry read can end in is named from the error value, wrapped as a reader
// wraps it, and that a failure of no such kind is named as none. The status rows
// include a conditional listing's answer, which carries its status as a value so
// it is named like any other read's.
func TestReadFailureKindNamesTheFailureFromItsValue(t *testing.T) {
	t.Parallel()
	listing, err := url.Parse("http://registry.example/v2/component/core/api/tags/list?n=1000")
	if err != nil {
		t.Fatal(err)
	}
	answered := func(status int) error {
		return fmt.Errorf("version 1.0.0: list signature referrers: %w",
			&errcode.ErrorResponse{Method: http.MethodGet, URL: listing, StatusCode: status})
	}
	reset := &url.Error{Op: "Get", URL: listing.String(), Err: &net.OpError{Op: "read", Net: "tcp", Err: connectionResetErrors[0]}}
	rows := []struct {
		name string
		err  error
		want string
	}{
		{"a 503 answer", answered(http.StatusServiceUnavailable), "the registry answered 503 Service Unavailable"},
		{"a 429 answer", answered(http.StatusTooManyRequests), "the registry answered 429 Too Many Requests"},
		{"a 401 answer", answered(http.StatusUnauthorized), "the registry answered 401 Unauthorized"},
		{"a status with no name", answered(599), "the registry answered 599"},
		{"a conditional listing's 502 answer", fmt.Errorf("tags: %w", statusAnswer(&http.Request{Method: http.MethodGet, URL: listing}, &http.Response{StatusCode: http.StatusBadGateway})), "the registry answered 502 Bad Gateway"},
		{"a reset connection", fmt.Errorf("fetch signature: %w", reset), "the connection was reset"},
		{"a body cut short", fmt.Errorf("fetch signature: %w", io.ErrUnexpectedEOF), "the answer was cut short"},
		{"a body cut short by a reset", fmt.Errorf("the response body ended early: %w: %w", io.ErrUnexpectedEOF, reset), "the connection was reset"},
		{"a read past its deadline", fmt.Errorf("tags: %w", &url.Error{Op: "Get", URL: listing.String(), Err: timeoutError{}}), "the read timed out"},
		{"a request deadline", fmt.Errorf("tags: %w", context.DeadlineExceeded), "the read timed out"},
		{"a failure of no such kind", errors.New("live trust-root fetch: failed to persist metadata"), ""},
		{"no failure", nil, ""},
	}
	for _, row := range rows {
		t.Run(row.name, func(t *testing.T) {
			t.Parallel()
			if got := ReadFailureKind(row.err); got != row.want {
				t.Errorf("ReadFailureKind(%v) = %q; want %q", row.err, got, row.want)
			}
		})
	}
}
