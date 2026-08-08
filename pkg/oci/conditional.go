// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package oci

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
)

// indexTagsListPageSize is the tag page a conditional tags/list read requests. A
// registry that serves a validator for its computed listings derives it from the
// serialized response body, which depends on the page size, so the read must use a
// fixed page size for the stored validator to match on the next refresh.
const indexTagsListPageSize = 1000

// ConditionalListTags reads a repository's tags/list route over HTTP, carrying an
// If-None-Match precondition when ifNoneMatch is non-empty. It performs a plain
// request rather than the high-level ORAS tags listing, which does not tolerate a
// 304 Not Modified, while reusing the same authenticated, loopback-aware transport
// every other read uses, so credentials and plain-HTTP handling never diverge.
//
// When the registry answers 304 Not Modified — the listing is unchanged since the
// caller's stored validator — notModified is true, etag echoes the validator, and
// tags is nil. On 200 it returns the current tag page, the response's strong ETag
// validator (empty when the registry serves none), and truncated when a further page
// remains. The caller stores the ETag and presents it as ifNoneMatch on the next
// refresh; a truncated page means the single-request validator does not cover the
// whole listing, so the caller should fall back to the full enumeration.
func ConditionalListTags(ctx context.Context, host, repo, ifNoneMatch string, httpClient *http.Client) (tags []string, etag string, notModified bool, truncated bool, err error) {
	plainHTTP, client, err := remoteClient(host, httpClient)
	if err != nil {
		return nil, "", false, false, err
	}
	scheme := "https"
	if plainHTTP {
		scheme = "http"
	}
	endpoint := url.URL{
		Scheme:   scheme,
		Host:     host,
		Path:     "/v2/" + repo + "/tags/list",
		RawQuery: url.Values{"n": {strconv.Itoa(indexTagsListPageSize)}}.Encode(),
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint.String(), nil)
	if err != nil {
		return nil, "", false, false, fmt.Errorf("oci: tags/list request %q: %w", repo, err)
	}
	if ifNoneMatch != "" {
		req.Header.Set("If-None-Match", ifNoneMatch)
	}

	resp, err := client.Do(req)
	if err != nil {
		return nil, "", false, false, fmt.Errorf("oci: tags/list %q: %w", repo, err)
	}
	defer resp.Body.Close()

	switch resp.StatusCode {
	case http.StatusNotModified:
		return nil, ifNoneMatch, true, false, nil
	case http.StatusOK:
		var payload struct {
			Tags []string `json:"tags"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
			return nil, "", false, false, fmt.Errorf("oci: tags/list %q decode: %w", repo, err)
		}
		return payload.Tags, resp.Header.Get("ETag"), false, resp.Header.Get("Link") != "", nil
	case http.StatusUnauthorized, http.StatusForbidden:
		return nil, "", false, false, fmt.Errorf("oci: tags/list %q: status %d: %w", repo, resp.StatusCode, ErrFetchUnauthorized())
	default:
		return nil, "", false, false, fmt.Errorf("oci: tags/list %q: unexpected status %d", repo, resp.StatusCode)
	}
}
