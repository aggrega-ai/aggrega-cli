// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

// Package compose implements the compose-https-fragment source-
// resolver adapter. The adapter fetches a single Docker Compose YAML
// fragment over HTTPS, wraps it in a deterministic .tar.gz archive,
// computes the content digest, populates the cache slot, and returns a
// ResolvedSource for the lockfile writer.
//
// Auth: the fetch carries no credential of its own. The adapter sends a
// plain GET for the URL as written through the client it is handed, so
// only a user and password written into the URL itself are sent, and a
// 401 or 403 answer is reported as an unauthorized fetch.
package compose

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"path"
	"strings"

	"github.com/aggrega-ai/aggrega-cli/pkg/sourcekind"
	"github.com/aggrega-ai/aggrega-cli/pkg/sourceresolver"
	"github.com/aggrega-ai/aggrega-cli/pkg/sourceresolver/cacheslot"
	"github.com/aggrega-ai/aggrega-cli/pkg/sourceresolver/internal/archive"
)

// Kind names the compose-https-fragment source kind, whose one declaration is
// [sourcekind.ComposeHTTPSFragment].
const Kind = sourcekind.ComposeHTTPSFragment

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

// NewAdapter returns a compose-https-fragment adapter. In production the adapter
// carries no client of its own and transports each fetch over the network-egress
// gate's client threaded through the source spec (SourceSpec.Client), so the gate
// is the sole client constructor and an offline workspace is refused upstream.
// Tests inject their own httptest-backed client via the unexported constructor.
func NewAdapter() sourceresolver.Adapter {
	return &adapter{}
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
//
// ctx rides on the request, so ending it closes the connection under an
// in-flight fetch: the download stops on the wire and the body read
// fails, rather than the caller being released while the transfer runs
// on. The fragment is digested only once it has arrived in full, and the
// cache slot is derived from that digest, so an interrupted fetch writes
// nothing at all.
func (a *adapter) Resolve(ctx context.Context, spec sourceresolver.SourceSpec) (*sourceresolver.ResolvedSource, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, spec.URL, nil)
	if err != nil {
		return nil, sourceresolver.Wrap(spec, sourceresolver.ErrFetchNetworkUnreachable(), err)
	}
	// Production carries no client of its own: the egress-gate client threaded
	// through the spec transports the fetch. Tests inject a client on the adapter.
	client := a.client
	if client == nil {
		client = spec.Client
	}
	if client == nil {
		return nil, sourceresolver.Wrap(spec, sourceresolver.ErrFetchNetworkUnreachable(),
			fmt.Errorf("compose-https-fragment: no egress client available for %q", spec.URL))
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, mapTransportError(spec, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, mapStatusError(spec, resp.StatusCode)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, sourceresolver.Wrap(spec, sourceresolver.ErrFetchNetworkUnreachable(), err)
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
	slot, err := cacheslot.Slot(spec.WorkspaceRoot, spec.CacheDir, Kind, spec.URL, "", "", spec.Version, digest)
	if err != nil {
		return nil, fmt.Errorf("compose-https-fragment: %w", err)
	}
	// An HTTPS-fetched compose fragment has no OCI manifest; the archive is
	// verified against the recorded content digest alone.
	if err := cacheslot.WriteBlobAndMetadata(spec.WorkspaceRoot, spec.CacheDir, slot, archiveBytes,
		Kind, spec.URL, spec.Version, digest, MediatypeYAML, "", nil); err != nil {
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
		return sourceresolver.Wrap(spec, sourceresolver.ErrFetchNetworkUnreachable(), err)
	}
	if strings.Contains(err.Error(), "connection refused") ||
		strings.Contains(err.Error(), "no such host") {
		return sourceresolver.Wrap(spec, sourceresolver.ErrFetchNetworkUnreachable(), err)
	}
	return sourceresolver.Wrap(spec, sourceresolver.ErrFetchNetworkUnreachable(), err)
}

func mapStatusError(spec sourceresolver.SourceSpec, status int) error {
	switch status {
	case http.StatusNotFound:
		return sourceresolver.Wrap(spec, sourceresolver.ErrFetchNotFound(),
			fmt.Errorf("HTTP %d", status))
	case http.StatusUnauthorized, http.StatusForbidden:
		return sourceresolver.Wrap(spec, sourceresolver.ErrFetchUnauthorized(),
			fmt.Errorf("HTTP %d", status))
	default:
		return sourceresolver.Wrap(spec, sourceresolver.ErrFetchNotFound(),
			fmt.Errorf("HTTP %d", status))
	}
}

func init() {
	sourceresolver.Register(NewAdapter())
}
