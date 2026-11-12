// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

// Package pluginabi holds the plugin ABI version this compiler speaks, the
// versions it spoke once and speaks no longer, and the condition a plugin
// declaring one of those is refused under.
//
// A plugin declares the ABI it was built against in two places: its guest's
// describe() report, read when the guest is loaded, and its Plugin entity's
// spec.abiVersion, read when the entity is. Both say the same thing about the
// same plugin and both are fixed the same way — rebuild the plugin against the
// current plugin kit and publish it again — so the two refusals report one
// condition, and the versions they recognise as retired have this one home.
package pluginabi

import (
	"slices"

	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
)

// ErrRetiredABIVersion is the sentinel for a plugin declaring a plugin ABI version
// this compiler spoke once and speaks no longer — in its guest's describe() report
// or in its Plugin entity's spec.abiVersion. A version the compiler never spoke is
// not this condition: the guest's is refused as any other descriptor refusal, and
// the entity's by its schema.
func ErrRetiredABIVersion() error { return errRetiredABIVersion }

var errRetiredABIVersion = cerrors.Register("retired-abi-version", cerrors.Meta{
	ShortCode: "E0374",
	Message:   "a plugin declares a plugin ABI version this compiler no longer speaks",
	RulePlace: cerrors.RuleOverAuthoredInput(),
})

// Version is the plugin ABI version this compiler speaks: the one value a
// guest's describe() report may declare, which the descriptor check in
// internal/wasm admits and the Plugin schema's abiVersion enum states. It is held
// to the descriptor schema's generated constant by a cross-check in this module's
// tests, so moving the ABI is a schema edit, a regeneration and this line.
const Version = "v1beta1"

// retired are the plugin ABI versions a plugin could once declare and this
// compiler no longer speaks. A version leaves the list never: a plugin still
// declaring one is told what to rebuild against for as long as the compiler can
// recognise it.
var retired = []string{"v1alpha1"}

// IsRetired reports whether version is a plugin ABI version this compiler spoke
// once and speaks no longer.
func IsRetired(version string) bool { return slices.Contains(retired, version) }

// Advice is what a reader refused under ErrRetiredABIVersion is told to do,
// worded once for both refusals.
const Advice = "rebuild the plugin against the current plugin kit and publish it again"
