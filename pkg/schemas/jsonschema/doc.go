// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

// Package jsonschema rebinds canonical JSON Schema 2020-12 source
// owned by the aggrega-schemas Go module under a stable identifier
// this module's validators consume — WorkloadJSONSchema, for the
// platform-owned Workload kind. The package holds no schema files of
// its own.
//
// Rebinding rather than copying is what makes the schema
// single-sourced by construction: aggrega-schemas is the one home for
// the bytes, and with no second copy here nothing can drift out of
// alignment with the published schema. Reintroducing a local copy
// would trade that guarantee for alignment kept only by convention.
// Keeping the consumer identifier stable while the source of truth
// stays in exactly one place is the package's whole job.
//
// pkg/validator/envelope is the consumer: it decodes the rebound
// source once and compiles a validator for strict validation of the
// Workload shape.
package jsonschema
