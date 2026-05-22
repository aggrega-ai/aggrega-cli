// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package oci

import (
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
)

// CachePath returns the cache slot for the given URI + version under
// the workspace's configured cache directory. The slot path is
//
//	<workspaceRoot>/<cacheDir>/oci-cache/<URI-encoded>/<version>/
//
// URI encoding replaces filesystem-unsafe characters (the path
// separator `/`, the scheme separator `://`, the version-pin `:`,
// the registry-port `:`) with URL-percent-encoded forms so the slot
// is a stable, single-segment directory across operating systems.
// Two CachePath invocations with the same inputs produce
// byte-identical paths.
//
// The current layout is flat. A future implementation will refactor the root
// into the two-subtree shape `<cacheDir>/oci-cache/entities/<…>` and
// `<cacheDir>/oci-cache/source-artifacts/<…>` per the source-
// resolution amendment; the flat layout shipped here is the
// migration source.
func CachePath(workspaceRoot, cacheDir, uri, version string) string {
	encoded := encodeURI(uri)
	return filepath.Join(workspaceRoot, cacheDir, "oci-cache", encoded, version)
}

// ReadCached returns the bytes previously stored at the cache slot
// for uri@version. Returns a wrapped ErrFetchNotFound when the slot
// is empty (cache miss); other errors surface as wrapped read
// failures.
//
// The single-blob layout assumes one artifact per (uri, version)
// pair. Future cache layouts that store multi-file extracted
// artifacts (e.g., un-tarred Recipe directories) replace this
// function with a directory-walking variant; callers should consult
// the layout convention in effect at their entry point.
func ReadCached(workspaceRoot, cacheDir, uri, version string) ([]byte, error) {
	blob := filepath.Join(CachePath(workspaceRoot, cacheDir, uri, version), "blob")
	data, err := os.ReadFile(blob)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("oci: cache miss for %s@%s: %w", uri, version, ErrFetchNotFound)
		}
		return nil, fmt.Errorf("oci: cache read %s@%s: %w", uri, version, err)
	}
	return data, nil
}

// WriteCached stores bytes at the cache slot for uri@version,
// creating parent directories as needed. The blob filename is
// "blob"; callers wanting a multi-file layout should use the
// directory-aware variants that future milestones introduce.
func WriteCached(workspaceRoot, cacheDir, uri, version string, data []byte) error {
	dir := CachePath(workspaceRoot, cacheDir, uri, version)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("oci: cache mkdir %s: %w", dir, err)
	}
	blob := filepath.Join(dir, "blob")
	if err := os.WriteFile(blob, data, 0o644); err != nil {
		return fmt.Errorf("oci: cache write %s: %w", blob, err)
	}
	return nil
}

// encodeURI maps an OCI URI (e.g.,
// `oci://hub.aggrega.ai/component/core/api-server` or
// `hub.aggrega.ai/component/core/api-server`) to a single
// filesystem-safe segment via standard URL path-segment encoding.
// Slashes within the URI body are percent-encoded so the entire URI
// occupies one directory level. Two encodings of the same URI are
// identical; decoding (not implemented here; not needed by current
// consumers) is round-trippable via url.PathUnescape.
func encodeURI(uri string) string {
	uri = strings.TrimPrefix(uri, "oci://")
	return url.PathEscape(uri)
}
