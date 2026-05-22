// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package jsonschema

import (
	schemas "github.com/aggrega-ai/aggrega-schemas"
)

// WorkloadJSONSchema is the JSON Schema 2020-12 source for the
// aggrega.ai/v1alpha1 Workload kind. Rebinds the canonical bytes
// from the aggrega-schemas Go module so this package's existing
// consumer surface (the WorkloadJSONSchema identifier) remains
// stable while the underlying source-of-truth lives in exactly one
// place.
//
// Consumers (pkg/validator/workload_validate.go) unmarshal the
// source into a *github.com/google/jsonschema-go/jsonschema.Schema
// and build a validator via pkg/validator/jsonschema.New for
// runtime checking during Workload parsing.
var WorkloadJSONSchema = schemas.WorkloadV1alpha1
