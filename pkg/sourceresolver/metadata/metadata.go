// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

// Package metadata models the metadata.yaml file that every
// source-resolver cache slot carries alongside its blob.tgz. The
// file records what the slot contains (kind, url, version, digest,
// mediatype) and when it was pulled — enough for build-time
// integrity checks and for human inspection of the cache.
//
// The wire shape is pinned at read time by the
// cache-metadata-v1alpha1 JSON Schema (embed-by-import from the
// aggrega-schemas Go module). The validator is package-singleton:
// compiled once via sync.Once on first Read call and reused for the
// process lifetime. Read calls are frequent at build time (one per
// source-artifact cache slot the renderer touches), so amortising
// the compile cost matters.
package metadata

import (
	"encoding/json"
	"fmt"
	"os"
	"sync"
	"time"

	libschema "github.com/google/jsonschema-go/jsonschema"
	"go.yaml.in/yaml/v3"

	"github.com/aggrega-ai/aggrega-cli/pkg/oci"
	jsonvalidator "github.com/aggrega-ai/aggrega-cli/pkg/validator/jsonschema"
	schemas "github.com/aggrega-ai/aggrega-schemas"
)

// ErrCacheMetadataInvalid wraps every structural-validation failure
// Read raises against the cache-metadata-v1alpha1 schema. Errors
// satisfying errors.Is(err, ErrCacheMetadataInvalid) carry a wrapped
// message naming the offending field path and the validation
// diagnostic.
//
// The sentinel wraps oci.ErrFetchIntegrity so cache-metadata
// corruption surfaces as a fetch-class error at the orchestrator
// boundary, with the same `aggrega pull --regenerate` remediation
// that catches archive-bytes corruption. Both signals mean the same
// thing operationally: the cache slot is bad, re-pull. Callers can
// errors.Is the returned error against ErrCacheMetadataInvalid for
// the specific cause, or against oci.ErrFetchIntegrity for the
// fetch-class category.
var ErrCacheMetadataInvalid = fmt.Errorf("metadata: cache metadata.yaml failed schema validation: %w", oci.ErrFetchIntegrity)

var (
	validatorOnce sync.Once
	validator     jsonvalidator.Validator
	validatorErr  error
)

func getValidator() (jsonvalidator.Validator, error) {
	validatorOnce.Do(func() {
		schema := &libschema.Schema{}
		if err := json.Unmarshal([]byte(schemas.CacheMetadataV1alpha1), schema); err != nil {
			validatorErr = fmt.Errorf("metadata: decode schema: %w", err)
			return
		}
		v, err := jsonvalidator.New(schema)
		if err != nil {
			validatorErr = fmt.Errorf("metadata: compile schema: %w", err)
			return
		}
		validator = v
	})
	return validator, validatorErr
}

// CacheMetadata is the on-disk shape of metadata.yaml. The YAML tags
// are load-bearing — they pin the wire shape across schema-driven
// validation that lands at a future implementation. Field order in
// this struct controls field order in marshalled YAML (yaml.v3
// preserves struct order), which keeps the file diffable.
type CacheMetadata struct {
	Kind string `yaml:"kind"`
	// URL is the authored identity URL — spec.source.url as authored (a repo root
	// or a direct .tgz for helm-https), NOT the resolved artifact location.
	URL       string `yaml:"url"`
	Version   string `yaml:"version"`
	Digest    string `yaml:"digest"`
	Mediatype string `yaml:"mediatype"`
	// ResolvedURL is provenance only: the concrete artifact location the authored
	// URL resolved to at pull time (a repo-root helm source's direct .tgz).
	// Omitted when the resolved location equals URL, keeping metadata for
	// URL-identified kinds byte-identical.
	ResolvedURL string    `yaml:"resolved_url,omitempty"`
	PulledAt    time.Time `yaml:"pulled_at"`
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

// Read parses the metadata.yaml at path into a CacheMetadata.
// Validates the parsed document against the embedded
// cache-metadata-v1alpha1 JSON Schema before typed unmarshal —
// missing required fields, unknown properties, malformed digest
// patterns, and malformed RFC 3339 pulled_at timestamps all halt
// with ErrCacheMetadataInvalid.
//
// Errors surface as wrapped failures; callers that distinguish
// "missing file" from "schema-invalid" can errors.Is against
// os.ErrNotExist OR ErrCacheMetadataInvalid on the returned error.
func Read(path string) (CacheMetadata, error) {
	var m CacheMetadata
	data, err := os.ReadFile(path)
	if err != nil {
		return m, fmt.Errorf("metadata: read %s: %w", path, err)
	}

	var instance any
	if err := yaml.Unmarshal(data, &instance); err != nil {
		return m, fmt.Errorf("metadata: parse %s: %w", path, err)
	}
	instance = normaliseYAML(instance)

	v, err := getValidator()
	if err != nil {
		return m, fmt.Errorf("metadata: schema build: %w", err)
	}
	report, err := v.Validate(instance, nil)
	if err != nil {
		return m, fmt.Errorf("metadata: validate %s: %w", path, err)
	}
	if len(report.Findings) > 0 {
		f := report.Findings[0]
		fieldPath := f.FieldPath
		if fieldPath == "" {
			fieldPath = "(root)"
		}
		return m, fmt.Errorf("metadata: %s: %s: %s: %w",
			path, fieldPath, f.Message, ErrCacheMetadataInvalid)
	}

	if err := yaml.Unmarshal(data, &m); err != nil {
		return m, fmt.Errorf("metadata: unmarshal %s: %w", path, err)
	}
	return m, nil
}

// normaliseYAML mirrors the lockfile loader's normalisation:
// map[any]any → map[string]any so the validator's walker consumes
// the document; time.Time → RFC 3339 string so the date-time
// pulled_at field validates as a string rather than as the
// object form yaml.v3 produces for time.Time auto-detected values.
func normaliseYAML(v any) any {
	switch val := v.(type) {
	case map[any]any:
		out := make(map[string]any, len(val))
		for k, vv := range val {
			if ks, ok := k.(string); ok {
				out[ks] = normaliseYAML(vv)
			}
		}
		return out
	case map[string]any:
		out := make(map[string]any, len(val))
		for k, vv := range val {
			out[k] = normaliseYAML(vv)
		}
		return out
	case []any:
		out := make([]any, len(val))
		for i, item := range val {
			out[i] = normaliseYAML(item)
		}
		return out
	case time.Time:
		return val.UTC().Format(time.RFC3339)
	default:
		return v
	}
}
