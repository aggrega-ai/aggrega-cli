// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

// Package frpcodec is a reflect-free, generated JSON codec for the
// feature-recipe-pipeline step socket wire shapes (FRPStepInput / FRPStepOutput
// and the types they reference). Every type and its streaming Marshal/Unmarshal
// pair is generated from the published FRP step JSON schema by
// internal/tools/typegen; the codec contains no reflection by construction —
// composite fields are walked by generated token-driven code, and scalar leaf
// fields decode into concrete typed fields.
//
// The package uses a neutral, cycle-free type vocabulary: an opaque
// cross-package host type (the render-time context, a diagnostic) carries
// through as a verbatim json.RawMessage, and an order-preserving host
// container surfaces as a plain map. The wire bytes are identical to the host
// hand types; the order-preserving container's key order is the emitting
// host's concern, and a consumer that selects by content reads a map.
package frpcodec

//go:generate go run ../../tools/typegen frp_gen.go
