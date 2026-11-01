// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package registryops

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"oras.land/oras-go/v2/registry/remote/errcode"

	"github.com/aggrega-ai/aggrega-cli/internal/retry"
	"github.com/aggrega-ai/aggrega-cli/pkg/egress"
	"github.com/aggrega-ai/aggrega-cli/pkg/oci"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// indexReadBodyLimit is how much of a successful read's body the walk's transport
// takes in before handing the response on. Everything an index walk reads — a
// listing, a manifest, a signature, a statement — is far smaller, so in practice
// every body is read through, and one that ends early is asked for again. A body
// larger than this is handed on as it streams, and an early end there reaches the
// reader as a failed read without another attempt.
const indexReadBodyLimit = 4 << 20

// readRetryingTransport is the transport every request of a registry index walk
// travels through. It asks a read again where the registry's answer, or the lack
// of one, says a later attempt can succeed: a status the one registry-failure
// classifier calls transient (oci.IsTransientRegistryError: a 5xx, 408 or 429), a
// connection failure it calls transient (a timeout, a reset, an unexpected end),
// and a successful answer whose body ended before it was complete. It asks under
// the module's one retry budget (retry.MaxAttempts) and backoff, and where the
// registry said how long to wait — a Retry-After header — it waits that long.
//
// It retries reads only: a GET or a HEAD with no body. Asking again is then the
// same question, so it cannot change what the registry holds. Anything else passes
// through untouched.
//
// A read still refused after the last attempt is handed on as the registry gave
// it, so the reader reports the registry's own answer; one that never got an
// answer is returned as its error. Either way the reader learns that it could not
// read, which is never the same as reading that nothing is there.
type readRetryingTransport struct {
	base http.RoundTripper
}

// retryingReadClient returns the network client for cfg with its reads going
// through readRetryingTransport. The client and the transport it wraps both come
// from the egress gate, so the walk reaches the network on the same terms, and
// under the same offline decision, as every other client.
func retryingReadClient(cfg workspaceconfig.Config) (*http.Client, error) {
	base, err := egress.RoundTripper(cfg)
	if err != nil {
		return nil, err
	}
	httpClient, err := egress.HTTPClient(cfg)
	if err != nil {
		return nil, err
	}
	httpClient.Transport = readRetryingTransport{base: base}
	return httpClient, nil
}

func (t readRetryingTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if (req.Method != http.MethodGet && req.Method != http.MethodHead) || (req.Body != nil && req.Body != http.NoBody) {
		return t.base.RoundTrip(req)
	}
	// refused is the latest answer the registry refused the read with. It is kept
	// open so that, if it is the last, it is handed on as the registry gave it, and
	// closed as soon as the read is asked again.
	var refused *refusedRead
	resp, err := retry.Transient(req.Context(), func() (*http.Response, error) {
		if refused != nil {
			_ = refused.resp.Body.Close()
			refused = nil
		}
		resp, err := t.base.RoundTrip(req)
		if err != nil {
			return nil, err
		}
		answer := &errcode.ErrorResponse{Method: req.Method, URL: req.URL, StatusCode: resp.StatusCode}
		if resp.StatusCode >= http.StatusBadRequest && oci.IsTransientRegistryError(answer) {
			refused = &refusedRead{answer: answer, resp: resp, after: retryAfter(resp.Header)}
			return nil, refused
		}
		if req.Method == http.MethodGet && resp.StatusCode >= 200 && resp.StatusCode < 300 {
			if err := readThrough(resp); err != nil {
				return nil, err
			}
		}
		return resp, nil
	}, oci.IsTransientRegistryError)
	if err == nil {
		return resp, nil
	}
	var last *refusedRead
	if errors.As(err, &last) && last == refused {
		return last.resp, nil
	}
	if refused != nil {
		_ = refused.resp.Body.Close()
	}
	return nil, err
}

// readThrough reads a successful answer's body before it is handed on, so an
// answer whose body ends early is a failed attempt the read is asked again for,
// rather than a failure only the reader would meet, after the transport could no
// longer ask again. The body is replaced by what was read. A failure to read it is
// reported as the body having ended early, whatever the connection said about it,
// since the registry answered and what it answered did not arrive.
func readThrough(resp *http.Response) error {
	data, err := io.ReadAll(io.LimitReader(resp.Body, indexReadBodyLimit+1))
	if err != nil {
		_ = resp.Body.Close()
		if errors.Is(err, io.ErrUnexpectedEOF) {
			return err
		}
		return fmt.Errorf("the response body ended early: %w: %w", io.ErrUnexpectedEOF, err)
	}
	if len(data) > indexReadBodyLimit {
		rest := resp.Body
		resp.Body = struct {
			io.Reader
			io.Closer
		}{io.MultiReader(bytes.NewReader(data), rest), rest}
		return nil
	}
	_ = resp.Body.Close()
	resp.Body = io.NopCloser(bytes.NewReader(data))
	return nil
}

// refusedRead is a read the registry answered with a status worth asking again
// for. It reads as the registry's error response, so the one classifier judges it
// as it judges that response anywhere else, and it carries how long the registry
// asked to be left alone, which the retry waits out.
type refusedRead struct {
	answer *errcode.ErrorResponse
	resp   *http.Response
	after  time.Duration
}

func (r *refusedRead) Error() string { return r.answer.Error() }

func (r *refusedRead) Unwrap() error { return r.answer }

// RetryAfter is how long the registry asked to be left alone before the next
// attempt; zero where it did not say.
func (r *refusedRead) RetryAfter() time.Duration { return r.after }

// retryAfter reads a Retry-After header, in either of its two forms: a number of
// seconds, or the time to try again at. A header that is absent, malformed, or
// names a time already past asks for no wait.
func retryAfter(h http.Header) time.Duration {
	v := strings.TrimSpace(h.Get("Retry-After"))
	if v == "" {
		return 0
	}
	if secs, err := strconv.Atoi(v); err == nil {
		if secs <= 0 {
			return 0
		}
		return time.Duration(secs) * time.Second
	}
	if at, err := http.ParseTime(v); err == nil {
		if d := time.Until(at); d > 0 {
			return d
		}
	}
	return 0
}
