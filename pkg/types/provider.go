// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package types

//go:generate go run ../../internal/tools/typegen provider_gen.go

// Provider's struct declarations (Provider, ProviderMetadata,
// ProviderSpec) are generated into provider_gen.go from the published
// provider-v1alpha1 schema; their godoc is the schema's descriptions.
// This hand-written companion is where Provider methods or custom
// marshalers would live (there are none today); the shared Maintainer
// type the generated metadata references lives in common.go.
