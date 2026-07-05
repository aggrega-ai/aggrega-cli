// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

// Package compose implements the compose-https-fragment source-
// resolver adapter. The adapter fetches a single Docker Compose YAML
// fragment over HTTPS, wraps it in a deterministic .tar.gz archive
// (uniform handling across kinds per the source-resolution amendment
// §3.4), computes the content digest, populates the cache slot, and
// returns a ResolvedSource for the lockfile writer.
//
// Auth: plain HTTPS at this implementation. Richer auth (per-host
// tokens, OAuth flows) is deferred to a later implementation; the
// adapter consumes net/http.Client.Do directly so future auth
// transports drop in via http.RoundTripper.
package compose

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"path"
	"strings"

	"github.com/aggrega-ai/aggrega-cli/pkg/sourceresolver"
	"github.com/aggrega-ai/aggrega-cli/pkg/sourceresolver/internal/archive"
	"github.com/aggrega-ai/aggrega-cli/pkg/sourceresolver/cacheslot"
)

// Kind names the compose-https-fragment source kind.
const Kind = "compose-https-fragment"

// MediatypeYAML is the canonical media-type recorded for compose
// fragments. Per IANA, application/x-yaml is the de-facto type for
// YAML payloads (text/yaml is in proposal but not finalised).
const MediatypeYAML = "application/x-yaml"

type httpDoer interface {
	Do(req *http.Request) (*http.Response, error)
}

type adapter struct {
	client httpDoer
}

// NewAdapter returns a compose-https-fragment adapter wired to
// http.DefaultClient in production. Tests inject their own httptest
// server URL via spec.URL; the default client transports against
// any HTTP endpoint.
func NewAdapter() sourceresolver.Adapter {
	return &adapter{client: http.DefaultClient}
}

// Kind returns the compose-https-fragment kind string.
func (a *adapter) Kind() string { return Kind }

// Resolve downloads the YAML fragment at spec.URL, wraps it in a
// deterministic .tar.gz under the filename derived from the URL's
// final path segment, computes the digest, and populates the cache
// slot.
//
// Error mapping: HTTP 404 → ErrFetchNotFound; 401/403 →
// ErrFetchUnauthorized; transport-level failures (DNS, connection
// refused, timeouts) → ErrFetchNetworkUnreachable; non-2xx other →
// ErrFetchNotFound (treated as a fetch failure absent more
// information).
func (a *adapter) Resolve(spec sourceresolver.SourceSpec) (*sourceresolver.ResolvedSource, error) {
	req, err := http.NewRequest(http.MethodGet, spec.URL, nil)
	if err != nil {
		return nil, sourceresolver.Wrap(spec, sourceresolver.ErrFetchNetworkUnreachable, err)
	}
	resp, err := a.client.Do(req)
	if err != nil {
		return nil, mapTransportError(spec, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, mapStatusError(spec, resp.StatusCode)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, sourceresolver.Wrap(spec, sourceresolver.ErrFetchNetworkUnreachable, err)
	}

	fragmentName := path.Base(spec.URL)
	if fragmentName == "" || fragmentName == "/" || fragmentName == "." {
		fragmentName = "fragment.yaml"
	}

	var buf bytes.Buffer
	if err := archive.WriteTarGz(&buf, []archive.Entry{{RelPath: fragmentName, Data: body}}); err != nil {
		return nil, fmt.Errorf("compose-https-fragment: archive: %w", err)
	}
	archiveBytes := buf.Bytes()
	digest := archive.Digest(archiveBytes)
	slot, err := cacheslot.Slot(spec.WorkspaceRoot, spec.CacheDir, Kind, spec.URL, spec.Version, digest)
	if err != nil {
		return nil, fmt.Errorf("compose-https-fragment: %w", err)
	}
	// An HTTPS-fetched compose fragment has no OCI manifest; the archive is
	// verified against the recorded content digest alone.
	if err := cacheslot.WriteBlobAndMetadata(slot, archiveBytes,
		Kind, spec.URL, spec.Version, digest, MediatypeYAML, nil); err != nil {
		return nil, fmt.Errorf("compose-https-fragment: %w", err)
	}

	return &sourceresolver.ResolvedSource{
		Kind:           Kind,
		URL:            spec.URL,
		PinnedVersion:  spec.Version,
		Digest:         digest,
		Mediatype:      MediatypeYAML,
		CachePath:      slot + "/blob.tgz",
		OriginalSource: spec.OriginalSource,
	}, nil
}

func mapTransportError(spec sourceresolver.SourceSpec, err error) error {
	var netErr net.Error
	if errors.As(err, &netErr) {
		return sourceresolver.Wrap(spec, sourceresolver.ErrFetchNetworkUnreachable, err)
	}
	if strings.Contains(err.Error(), "connection refused") ||
		strings.Contains(err.Error(), "no such host") {
		return sourceresolver.Wrap(spec, sourceresolver.ErrFetchNetworkUnreachable, err)
	}
	return sourceresolver.Wrap(spec, sourceresolver.ErrFetchNetworkUnreachable, err)
}

func mapStatusError(spec sourceresolver.SourceSpec, status int) error {
	switch status {
	case http.StatusNotFound:
		return sourceresolver.Wrap(spec, sourceresolver.ErrFetchNotFound,
			fmt.Errorf("HTTP %d", status))
	case http.StatusUnauthorized, http.StatusForbidden:
		return sourceresolver.Wrap(spec, sourceresolver.ErrFetchUnauthorized,
			fmt.Errorf("HTTP %d", status))
	default:
		return sourceresolver.Wrap(spec, sourceresolver.ErrFetchNotFound,
			fmt.Errorf("HTTP %d", status))
	}
}

func init() {
	sourceresolver.Register(NewAdapter())
}
