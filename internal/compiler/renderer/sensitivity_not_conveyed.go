// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package renderer

// warnSensitivityNotConveyed advises on every `values` leaf this node's
// Component declares sensitive whose declaration the node's render cannot carry
// to its target, on the one render shape where the author holds a lever: a
// sourced render whose Recipe declares a values mapping. Which coordinates those
// are is decided by UnconveyedValueLeafDeclarations, beside the join it is the
// complement of; what happens here is the locating and the routing.
//
// It emits into the structured advisory sink and returns nothing. The condition
// it reports is an absence rather than a fault — no rendered key carries the
// marking — and a Recipe that maps its values is an ordinary supported shape, so
// there is nothing here to refuse and no rendered byte moves.
//
// Each advisory is located at the node and carries the declaration's authored
// coordinate as the element it concerns, so a reader that renders no text can act
// on the finding without recovering the coordinate from a sentence.
//
// It is raised host-side and handed straight to the coded sink, exactly as the
// unhonorable-annotation advisory is. A diagnostic arriving from a guest instead
// is routed to the uncoded stream unless it carries the unsupported-field code,
// so raising this one from a guest would silently cost it the independent
// suppressibility its own code exists to give it.
func warnSensitivityNotConveyed(
	nodeID, providerName, recipeName string,
	declared DeclaredSensitivity,
	sourced bool,
	sink func(Diagnostic),
) {
	if sink == nil {
		return
	}
	for _, coordinate := range UnconveyedValueLeafDeclarations(declared, sourced) {
		d := SensitivityNotConveyedDiagnostic(recipeName, coordinate)
		d.Location = &DiagnosticLocation{URI: nodeID, Kind: providerName}
		sink(d)
	}
}
