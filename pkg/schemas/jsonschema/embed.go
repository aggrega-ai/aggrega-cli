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
// place. The package documentation states which validator consumes it,
// so this declaration does not name a consumer of its own.
var WorkloadJSONSchema = schemas.WorkloadV1alpha1
