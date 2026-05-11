// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package jsonschema

import _ "embed"

// WorkloadJSONSchema is the JSON Schema 2020-12 source for the
// aggrega.ai/v1alpha1 Workload kind. Mirrors
// workload-v1alpha1.schema.json in the aggrega-schemas repo
// (the canonical public schema; this embedded copy is kept
// aligned by convention).
//
// Consumers (pkg/validator/workload_validate.go) unmarshal the
// source into a *github.com/google/jsonschema-go/jsonschema.Schema
// and build a validator via pkg/validator/jsonschema.New for
// runtime checking during Workload parsing.
//
//go:embed workload.json
var WorkloadJSONSchema string
