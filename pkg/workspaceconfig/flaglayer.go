// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package workspaceconfig

import (
	"os"
	"strconv"
)

// ConfigFlagName is the long name, without its leading dashes, of the global
// flag naming the global configuration file: the command line's form of
// EnvConfigOverride, which it wins over.
//
// It is held here, beside the overlay that carries the flag's value, so the
// flag's declaration and the rendering of an invocation's flags (FlagOverlay.Args)
// spell it from one place.
const ConfigFlagName = "config"

// FlagOverlay carries what the reader typed on an invocation's command line
// that a workspace resolution reads, in two parts that enter a resolution at
// two different points.
//
// ConfigPath decides which file the cascade's global layer is read from, so it
// is read before the merge, through the environment reader the resolution is
// given (Lookup). Every other field is the command-line layer of the precedence
// ladder: the top layer, above the environment overlay and above the file
// cascade, laid over the finished merge (WithFlags). ResolveWithFlags applies
// both, which is the form a caller resolving on behalf of an invocation it did
// not parse — a server handed the command line it was started with — reaches
// for.
//
// Every setting field is a pointer, and that is the layer's shape rather than a
// style choice. The question this layer answers is "did the reader set this on
// the invocation", not "what value does it hold": a boolean's zero value and an
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
	// ConfigPath is the global configuration file the reader named with
	// --config on this invocation, and empty where they named none, in which
	// case $AGGREGA_CONFIG, else the default location, decides that file as it
	// does for every surface. It selects a file rather than setting a value, so
	// no setting is ever attributed to it: a setting that file holds is
	// attributed to the file.
	ConfigPath string

	// Offline carries the offline posture when the reader set it on this
	// invocation, in either direction.
	Offline *bool
}

// Lookup returns the environment reader a resolution for this invocation reads
// through: base, except that the variable naming the global configuration file
// is answered with ConfigPath where the reader named one. That is the whole of
// how --config wins over $AGGREGA_CONFIG, and it is why a configuration-mutating
// operation run under the flag writes the file it read: a resolution keeps the
// reader it was made with, and the write target is resolved through it.
//
// Where ConfigPath is empty the reader returned IS base, so $AGGREGA_CONFIG,
// else the default location, governs. A nil base reads the process environment.
func (f FlagOverlay) Lookup(base func(string) string) func(string) string {
	if base == nil {
		base = os.Getenv
	}
	if f.ConfigPath == "" {
		return base
	}
	path := f.ConfigPath
	return func(name string) string {
		if name == EnvConfigOverride {
			return path
		}
		return base(name)
	}
}

// Args renders the overlay as the command-line flags that reproduce it, each
// in the --name=value form: the global configuration file first, then every
// setting the reader set, in the overlay table's order, in whichever direction
// they set it. An overlay carrying nothing renders nothing.
//
// It is what lets a process hand the command line it was started with to a
// command it runs in a child process, so the child reads the configuration the
// parent reads. The setting flags are rendered from the same table that applies
// them and attributes a setting to them, so a flag that table gains is rendered
// with no second list to keep in step with it.
func (f FlagOverlay) Args() []string {
	var out []string
	if f.ConfigPath != "" {
		out = append(out, flagLayerPrefix+ConfigFlagName+"="+f.ConfigPath)
	}
	for _, b := range flagBindings {
		if v, ok := b.value(f); ok {
			out = append(out, b.label+"="+v)
		}
	}
	return out
}

// ResolveWithFlags resolves the workspace as Resolve does, for an invocation
// whose command line f carries: the cascade's global layer is read from the file
// f names with --config where it names one, and the settings f carries are laid
// over the result. The process environment is read for everything else.
//
// It is the one call for a caller resolving on behalf of a command line it was
// handed rather than one it parsed, so that both parts of the overlay reach the
// resolution together: WithFlags alone lays the settings over a resolution
// whose global layer was already read, and cannot change which file that was.
func ResolveWithFlags(start, flagRoot string, f FlagOverlay) (*ResolvedWorkspace, error) {
	rw, err := Resolve(start, flagRoot, f.Lookup(nil))
	if err != nil {
		return nil, err
	}
	return rw.WithFlags(f), nil
}

// flagLayerPrefix marks a source label as a command-line flag rather than a
// config file's path or an environment override. Nothing else in the cascade
// can collide with it: a file is recorded as an absolute path and an
// environment override behind its own prefix.
const flagLayerPrefix = "--"

// flagBinding is one row of the command-line overlay table: the source label a
// setting this row determined carries, the setting it writes, the function that
// writes the row's SET value onto a Config, and the function that renders that
// value as the flag's argument.
//
// It is the command-line counterpart of envBinding, and deliberately the same
// shape for the same reason: a layer whose members are a table has one
// enumeration of what it can carry, so the overlay, the provenance report and
// the contributing-layer report cannot come to disagree about the set.
type flagBinding struct {
	// label is the flag as a reader types it. That spelling is the only one a
	// reader can act on, so it is what a provenance report and a refusal name.
	label string
	// configPath is the dotted name of the setting apply writes, spelled as a
	// config file writes it. It is how the provenance report knows which setting
	// a flag that applied set, whatever value it set — a flag typed with the
	// value already in force is still the layer holding that value.
	configPath string
	// apply writes the row's value onto cfg when the overlay carries one, and
	// reports whether it wrote anything. An unset flag is a no-op
	// fall-through: it leaves the value the layers beneath resolved untouched
	// rather than overwriting it with a zero value.
	apply func(cfg *Config, f FlagOverlay) bool
	// value renders the row's value, as the flag's argument would spell it,
	// when the overlay carries one, and reports whether it carries one. It
	// reports exactly when apply writes, so the flags Args renders are the flags
	// whose settings this layer applies.
	value func(f FlagOverlay) (string, bool)
}

// flagBindings is the ordered single-source table of command-line overrides.
// Order is fixed and deterministic, so a provenance replay walks the layer the
// same way on every run.
var flagBindings = []flagBinding{
	{
		label:      "--" + OfflineSettingKey,
		configPath: OfflineSettingKey,
		apply: func(cfg *Config, f FlagOverlay) bool {
			if f.Offline == nil {
				return false
			}
			cfg.Offline = *f.Offline
			return true
		},
		value: func(f FlagOverlay) (string, bool) {
			if f.Offline == nil {
				return "", false
			}
			return strconv.FormatBool(*f.Offline), true
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

// set reports whether f carries any setting at all — the global configuration
// file it may name is not one.
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
// reader set on the invocation. An overlay setting nothing returns r itself.
//
// The global configuration file f names is not applied here, because it decides
// what the merge read rather than anything laid over it: a resolution meant to
// honour it is made through f.Lookup, which ResolveWithFlags does.
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
