// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

// Package schemasurface names the places a Component or Feature declares a schema
// contract, in the spelling every diagnostic about one prints: the entity's own
// schema block and each of its pillars, and the per-edge contract fields of each
// dependency socket.
//
// It is the one home of those names and of which pillar each socket field
// constrains. Two readings depend on the names agreeing — the `$import:`
// substitution names the surface it read, and the schema-contract reading names
// the field a finding is about — and a reader matching one against the other has
// to see the same string for the same field. Spelled in two places, the two would
// agree only for as long as nobody edited one of them, and a disagreement would
// show as a diagnostic pointing at the wrong file rather than as a failure.
package schemasurface

// Schema names the entity's own schema block.
const Schema = "spec.schema"

// Pillar names one pillar of the entity's own schema block.
func Pillar(pillar string) string {
	return Schema + "." + pillar
}

// SocketContract names one socket's per-edge contract field: field is the field
// the contract is declared in, as [SocketContractFields] spells it.
func SocketContract(socket, field string) string {
	return "spec.usesSchema." + socket + "." + field
}

// SocketContractField pairs a pillar a socket may constrain with the field its
// per-edge contract is declared in.
type SocketContractField struct {
	// Pillar is the pillar the contract constrains (values / infra).
	Pillar string
	// Field is the socket field the contract is declared in.
	Field string
}

// SocketContractFields returns every per-edge contract field a socket declares,
// in field order. It returns a copy, so a caller cannot change the set through it.
//
// `provides` has no entry: it is a producer's published surface rather than a
// consumer's authored block, so an edge writes nothing there and a socket
// declares no contract over it.
func SocketContractFields() [2]SocketContractField {
	return [2]SocketContractField{
		{Pillar: "values", Field: "valueSchema"},
		{Pillar: "infra", Field: "infraSchema"},
	}
}
