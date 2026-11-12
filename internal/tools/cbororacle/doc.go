// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

// Package cbororacle holds the independent byte-parity oracle for the generated
// canonical-CBOR guest codec. It asserts that the reflect-free EncodeCBOR the
// generator emits into the aggrega-abi module produces exactly the bytes a
// reference deterministic-CBOR encoder produces for the same value, and that a
// decode round-trips.
//
// The reference is fxamacker/cbor in its Core Deterministic preset
// (CoreDetEncOptions): RFC 8949 §4.2.1 canonical encoding with the shortest
// float16/32/64 that round-trips, zero preserved by sign, NaN canonicalized,
// and no numeric reduction — an integer-valued float stays a float. The
// reference reads the cbor struct tags the generator emits, so it keys the map
// by the wire key and sorts the same way; the generated codec carries a
// distinct method name (EncodeCBOR, not a Marshaler-interface method) precisely
// so the reference encodes the value independently rather than dispatching back
// into the generated codec.
//
// The oracle lives here in aggrega-cli, which already carries the reference as a
// dependency, rather than in aggrega-abi, so the abi module stays dependency-
// free for a guest to import.
package cbororacle
