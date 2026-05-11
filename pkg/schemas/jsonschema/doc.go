// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

// Package jsonschema exposes embedded JSON Schema 2020-12 sources for
// Aggrega's platform-owned kinds. The files are go:embed'd into the
// aggrega-cli binary and consumed by pkg/validator/jsonschema at
// runtime for strict validation of platform-owned shapes (Workload,
// etc.).
//
// The embedded sources mirror the canonical public schemas under
// aggrega-schemas/json-schemas/ (e.g., workload-v1alpha1.schema.json)
// so the in-binary and out-of-band schemas stay synchronized: the
// public schema is the source of truth; this package's files are
// copies kept aligned by convention.
package jsonschema
