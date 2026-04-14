// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

// Package resolver implements the Step 1 pipeline: YAML loading, parameter
// interpolation (Go templates + Sprig), parametersFrom external file merging,
// instance name validation, and boundary infra wiring.
//
// The resolver does NOT perform CUE validation — that is handled by pkg/validator.
// The pipeline ordering is: load → parametersFrom merge → interpolate → validate
// names → wire boundary infra → purge parameters.
package resolver
