// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package workspaceconfig

// FlagOverlay carries the command-line layer of the precedence ladder: the top
// layer, above the environment overlay and above the file cascade.
//
// Every field is a pointer, and that is the layer's shape rather than a style
// choice. The question this layer answers is "did the reader set this on the
// invocation", not "what value does it hold": a boolean's zero value and an
// unset flag are the same bits, so a plain bool could not express a flag
// deliberately set to false — which is exactly how a reader turns a committed
// or exported posture off for one run. Nil means the reader set nothing on this
// invocation and the layer beneath governs.
//
// A command-line surface fills it from whether the reader actually typed the
// flag; a surface with no command line leaves it zero, which is why the layer
// composes rather than replaces: every other surface resolves the same
// configuration through the same cascade and simply contributes no top layer.
type FlagOverlay struct {
	// Offline carries the offline posture when the reader set it on this
	// invocation, in either direction.
	Offline *bool
}

// flagLayerPrefix marks a source label as a command-line flag rather than a
// config file's path or an environment override. Nothing else in the cascade
// can collide with it: a file is recorded as an absolute path and an
// environment override behind its own prefix.
const flagLayerPrefix = "--"

// flagBinding is one row of the command-line overlay table: the source label a
// setting this row determined carries, and the function that writes the row's
// SET value onto a Config.
//
// It is the command-line counterpart of envBinding, and deliberately the same
// shape for the same reason: a layer whose members are a table has one
// enumeration of what it can carry, so the overlay, the provenance replay and
// the contributing-layer report cannot come to disagree about the set.
type flagBinding struct {
	// label is the flag as a reader types it. That spelling is the only one a
	// reader can act on, so it is what a provenance report and a refusal name.
	label string
	// apply writes the row's value onto cfg when the overlay carries one, and
	// reports whether it wrote anything. An unset flag is a no-op
	// fall-through: it leaves the value the layers beneath resolved untouched
	// rather than overwriting it with a zero value.
	apply func(cfg *Config, f FlagOverlay) bool
}

// flagBindings is the ordered single-source table of command-line overrides.
// Order is fixed and deterministic, so a provenance replay walks the layer the
// same way on every run.
var flagBindings = []flagBinding{
	{
		label: "--" + OfflineSettingKey,
		apply: func(cfg *Config, f FlagOverlay) bool {
			if f.Offline == nil {
				return false
			}
			cfg.Offline = *f.Offline
			return true
		},
	},
}

// applyFlagOverlay overlays the SET rows of f onto cfg, in table order, and
// returns the source labels of the flags that contributed (also in table
// order).
func applyFlagOverlay(cfg *Config, f FlagOverlay) []string {
	var contributing []string
	for _, b := range flagBindings {
		if b.apply(cfg, f) {
			contributing = append(contributing, b.label)
		}
	}
	return contributing
}

// set reports whether f carries any flag at all.
//
// It answers by running the overlay against a scratch Config rather than
// testing the fields, so the question "does this layer contribute anything"
// is answered by the same table that decides what it contributes.
func (f FlagOverlay) set() bool {
	var scratch Config
	return len(applyFlagOverlay(&scratch, f)) > 0
}

// WithFlags returns the resolution r carries with the command-line layer
// applied on top of it, which completes the precedence ladder: flag over
// environment over the file cascade over the built-in defaults.
//
// It is an overlay on a finished resolution rather than a second resolution
// path, and that is what keeps the single-chokepoint promise intact: the root
// discovery, the cascade, the appends and the environment overlay have all
// already run through the one merge path, and this writes only the settings the
// reader set on the invocation. An overlay carrying nothing returns r itself.
//
// The applied flags are retained so the per-value provenance report attributes
// a setting the command line determined to the flag that determined it, spelled
// as the reader typed it. Without that a reader who did not edit any file and
// exported no variable would be shown a source they never touched.
func (r *ResolvedWorkspace) WithFlags(f FlagOverlay) *ResolvedWorkspace {
	if r == nil || !f.set() {
		return r
	}
	out := *r
	applyFlagOverlay(&out.config, f)
	out.flags = f
	return &out
}
