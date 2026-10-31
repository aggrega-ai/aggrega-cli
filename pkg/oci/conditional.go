// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package oci

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"

	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
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

// indexReferrersPageSize is the referrer page a conditional referrers read
// requests. It is fixed for the same reason indexTagsListPageSize is: a registry
// deriving its listing validator from the serialized page answers a stored
// validator only for a request shaped like the one that earned it.
const indexReferrersPageSize = 1000

// ConditionalListReferrers revalidates the unfiltered referrer listing of one
// manifest digest in a repository, carrying an If-None-Match precondition when
// ifNoneMatch is non-empty. It reads no referrer: the listing's validator is the
// whole answer, because a caller that holds what it read from the referrers under
// that validator needs only to learn whether the set changed.
//
// When the registry answers 304 Not Modified, notModified is true and etag echoes
// ifNoneMatch. On 200 it returns the response's ETag — empty when the registry
// serves none, and empty as well when the listing continues on a further page,
// since a single page's validator does not cover the whole set. A registry that
// serves no referrers listing for the digest (404) is answered the same way, with
// no validator and no error: the question of whether the set changed is then one
// this read cannot settle, and a caller re-reads instead.
//
// Like ConditionalListTags it is a plain request over the same authenticated,
// loopback-aware transport every other read uses, because the high-level referrer
// discovery does not tolerate a 304.
func ConditionalListReferrers(ctx context.Context, host, repo, digest, ifNoneMatch string, httpClient *http.Client) (etag string, notModified bool, err error) {
	plainHTTP, client, err := remoteClient(host, httpClient)
	if err != nil {
		return "", false, err
	}
	scheme := "https"
	if plainHTTP {
		scheme = "http"
	}
	endpoint := url.URL{
		Scheme:   scheme,
		Host:     host,
		Path:     "/v2/" + repo + "/referrers/" + digest,
		RawQuery: url.Values{"n": {strconv.Itoa(indexReferrersPageSize)}}.Encode(),
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint.String(), nil)
	if err != nil {
		return "", false, fmt.Errorf("oci: referrers request %q@%s: %w", repo, digest, err)
	}
	req.Header.Set("Accept", ocispec.MediaTypeImageIndex)
	if ifNoneMatch != "" {
		req.Header.Set("If-None-Match", ifNoneMatch)
	}

	resp, err := client.Do(req)
	if err != nil {
		return "", false, fmt.Errorf("oci: referrers %q@%s: %w", repo, digest, err)
	}
	defer resp.Body.Close()
	// The body is not read for what it says, only drained so the connection can
	// carry the next request.
	_, _ = io.Copy(io.Discard, resp.Body)

	switch resp.StatusCode {
	case http.StatusNotModified:
		return ifNoneMatch, true, nil
	case http.StatusOK:
		if resp.Header.Get("Link") != "" {
			return "", false, nil
		}
		return resp.Header.Get("ETag"), false, nil
	case http.StatusNotFound:
		return "", false, nil
	case http.StatusUnauthorized, http.StatusForbidden:
		return "", false, fmt.Errorf("oci: referrers %q@%s: status %d: %w", repo, digest, resp.StatusCode, ErrFetchUnauthorized())
	default:
		return "", false, fmt.Errorf("oci: referrers %q@%s: unexpected status %d", repo, digest, resp.StatusCode)
	}
}
