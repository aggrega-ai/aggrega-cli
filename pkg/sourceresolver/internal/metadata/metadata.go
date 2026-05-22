// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

// Package metadata models the metadata.yaml file that every
// source-resolver cache slot carries alongside its blob.tgz. The
// file records what the slot contains (kind, url, version, digest,
// mediatype) and when it was pulled — enough for build-time
// integrity checks and for human inspection of the cache.
//
// The wire shape pinned here is forward-aligned with the future
// cache-metadata-v1alpha1 JSON Schema; at this WI, schema validation
// is not yet enforced — typed-struct round-trip in the test suite
// pins the shape. When the canonical schema ships at a future
// implementation, the schema's properties match these YAML tags
// exactly so adapters require no rewrite.
package metadata

import (
	"fmt"
	"os"
	"time"

	"gopkg.in/yaml.v3"
)

// CacheMetadata is the on-disk shape of metadata.yaml. The YAML tags
// are load-bearing — they pin the wire shape across schema-driven
// validation that lands at a future implementation. Field order in
// this struct controls field order in marshalled YAML (yaml.v3
// preserves struct order), which keeps the file diffable.
type CacheMetadata struct {
	Kind      string    `yaml:"kind"`
	URL       string    `yaml:"url"`
	Version   string    `yaml:"version"`
	Digest    string    `yaml:"digest"`
	Mediatype string    `yaml:"mediatype"`
	PulledAt  time.Time `yaml:"pulled_at"`
}

// Write marshals m to YAML and writes it to path. Returns a wrapped
// error on marshal or filesystem failure; the file is written with
// mode 0644 (matching the per-entry mode the archive package
// enforces, so cache-slot inspection sees consistent permissions).
func Write(path string, m CacheMetadata) error {
	data, err := yaml.Marshal(m)
	if err != nil {
		return fmt.Errorf("metadata: marshal: %w", err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return fmt.Errorf("metadata: write %s: %w", path, err)
	}
	return nil
}

// Read parses the metadata.yaml at path into a CacheMetadata. Errors
// surface as wrapped failures; callers that need to distinguish
// "missing file" from "corrupt content" can errors.Is against
// os.ErrNotExist on the returned error.
func Read(path string) (CacheMetadata, error) {
	var m CacheMetadata
	data, err := os.ReadFile(path)
	if err != nil {
		return m, fmt.Errorf("metadata: read %s: %w", path, err)
	}
	if err := yaml.Unmarshal(data, &m); err != nil {
		return m, fmt.Errorf("metadata: unmarshal %s: %w", path, err)
	}
	return m, nil
}
