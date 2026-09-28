// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package wasm

import (
	"context"
	"fmt"
)

// ValidateOptions is the input to ValidatePlugin: the declared plugin type the
// bytes are validated against, the memory cap for the describe-load, the identity
// used in diagnostics, and an optional entity front. A nil Entity runs the
// binary-only checks (describe-load, declared-vs-reported type); a non-nil Entity
// additionally cross-checks the binary against an authored entity surface.
type ValidateOptions struct {
	// DeclaredType is the plugin type the caller declares the bytes to be. It is
	// checked against the binary's self-reported type. Required.
	DeclaredType string

	// MaxMemoryPages caps the guest's linear memory for the describe-load. Zero
	// applies the platform default cap.
	MaxMemoryPages uint32

	// Identity names the plugin in every diagnostic ValidatePlugin emits.
	Identity string

	// Entity is the optional authored entity surface to cross-check the binary
	// against. Nil runs binary-only validation; non-nil additionally runs the
	// capability, command-mirror, and host-function cross-checks and collects the
	// under-declaration notices.
	Entity *EntityInputs

	// RefuseUncommittedBuild refuses a binary whose describe() build-provenance
	// version says it was built from a working tree with uncommitted changes
	// (BuiltFromUncommittedChanges), with ErrPluginBuiltFromUncommittedChanges.
	//
	// Publication sets it: such a version names a commit that does not hold the
	// bytes, so what is published could not be rebuilt from the version it is
	// published under. Validating a local build leaves it unset, because a build
	// under development is routinely made from an edited tree and checking it
	// against its entity is exactly what that work needs.
	//
	// It is judged after the entity's capability, command-mirror and host-function
	// cross-checks and immediately ahead of the version cross-check, so a guest
	// built from an edited tree is told to commit and rebuild rather than to copy
	// its marked version into the entity.
	RefuseUncommittedBuild bool
}

// EntityInputs carries the authored entity surface ValidatePlugin cross-checks a
// binary against, in this package's neutral shapes. The caller maps the catalog
// entity into these fields so this package stays ignorant of the catalog entity
// types — the same neutral boundary the load gate's per-field cross-checks use.
type EntityInputs struct {
	// Claim is the authored capability claim, cross-checked against the binary's
	// describe() self-report via CrossCheckContract.
	Claim EntityContractClaim

	// CommandMirror is the entity's machine-owned CLI-command record, checked for
	// full equality against the binary's described commands.
	CommandMirror []PluginCLICommand

	// HostFunctions is the entity's machine-owned host-function record, checked for
	// set-equality against the host functions the binary actually imports.
	HostFunctions []string

	// Version is the authored metadata.version, cross-checked for exact-string
	// equality against the binary's describe() build-provenance version so a
	// hand-edit cannot diverge the recorded version from the binary silently.
	Version string

	// CuratedSurface is the entity's authored, curated surface, read to derive the
	// advisory under-declaration notices (binary offers more than the entity
	// curates).
	CuratedSurface EntityCuratedSurface
}

// ValidateReport is the successful result of ValidatePlugin: the validated
// describe() descriptor and any advisory under-declaration notices. Notices are
// empty in binary-only mode and never signal failure — an author may curate a
// partial surface deliberately.
type ValidateReport struct {
	// Descriptor is the binary's validated describe() self-report.
	Descriptor PluginDescriptor

	// Notices is the advisory list of surfaces the binary offers that the entity
	// does not declare. Empty when Entity is nil or the entity covers everything
	// the binary reports.
	Notices []string
}

// ValidatePlugin is the single publish-side validation door over a .wasm binary:
// it loads the bytes to read their describe() self-report, checks the declared
// plugin type against the reported one, and cross-checks an optional authored
// entity surface. It is the one home of the validate sequence so a caller cannot
// assemble a partial or divergent subset of the checks.
//
// A declared plugin type is required: an empty DeclaredType is refused up front,
// because the type verdict rides on the describe-load and fires only against a
// declared expectation, so validating without one would admit unverified bytes.
//
// The sequence is ordered so the most precise diagnostic surfaces first:
//
//  1. Describe-load under the describe-only profile (inert host-function stubs,
//     the union import allowlist), so a binary of any plugin type instantiates
//     purely to have its describe() read and validated. This load is itself the
//     import-legality gate: the descriptor validation rejects any declared host
//     function illegal for the reported plugin type, and the load cross-checks the
//     declared host functions against the functions the binary actually imports,
//     so a binary cannot advertise a surface it lacks nor hide one it uses. The
//     declared plugin type flows into this load as the expected type, so the load
//     gate itself halts on a declared-vs-reported disagreement with
//     ErrWasmPluginTypeMismatch — the single home of that verdict, shared with
//     every load-and-dispatch socket.
//  2. Entity cross-checks, only when opts.Entity is non-nil: the authored
//     capability claim against describe(), the machine-owned command mirror for
//     full equality against the described commands, and the machine-owned
//     host-function record for set-equality against the binary's actual imports.
//  3. The build-provenance version, only when opts.RefuseUncommittedBuild is set:
//     a describe() version saying the binary was built from a working tree with
//     uncommitted changes is refused.
//  4. The recorded metadata.version, only when opts.Entity is non-nil, for
//     exact-string equality against the binary's describe() build-provenance
//     version; the advisory under-declaration notices are then collected into the
//     report.
//
// The load binds inert host-function stubs and never invokes an export beyond
// describe(), so validation grants the guest no real host capability. On any
// failure ValidatePlugin returns the zero report and the halting error; on
// success it returns the report with the validated descriptor and any notices.
func ValidatePlugin(ctx context.Context, wasmBytes []byte, opts ValidateOptions) (ValidateReport, error) {
	if opts.DeclaredType == "" {
		return ValidateReport{}, fmt.Errorf(
			"plugin %q: a declared plugin type is required to validate the binary", opts.Identity)
	}

	desc, err := DescribePluginBytes(ctx, opts.Identity, wasmBytes, opts.MaxMemoryPages, opts.DeclaredType)
	if err != nil {
		return ValidateReport{}, err
	}

	report := ValidateReport{Descriptor: desc}

	describeVersion := ""
	if desc.Metadata != nil {
		describeVersion = desc.Metadata.Version
	}

	if opts.Entity != nil {
		if err := CrossCheckContract(opts.Identity, opts.Entity.Claim, desc); err != nil {
			return ValidateReport{}, err
		}
		if !CommandMirrorsEqual(opts.Entity.CommandMirror, desc.Commands) {
			return ValidateReport{}, fmt.Errorf(
				"plugin %q: recorded command mirror %s does not match the plugin describe() commands %s: %w",
				opts.Identity, FormatCommandMirror(opts.Entity.CommandMirror), FormatCommandMirror(desc.Commands),
				ErrWasmCommandsMirrorMismatch())
		}
		derived, err := DeriveHostFunctions(opts.Identity, wasmBytes)
		if err != nil {
			return ValidateReport{}, err
		}
		if !HostFunctionsEqual(opts.Entity.HostFunctions, derived) {
			return ValidateReport{}, fmt.Errorf(
				"plugin %q: recorded host-function record %v does not match the .wasm imports %v: %w",
				opts.Identity, opts.Entity.HostFunctions, derived, ErrWasmHostFunctionsStampMismatch())
		}
	}

	if opts.RefuseUncommittedBuild && BuiltFromUncommittedChanges(describeVersion) {
		return ValidateReport{}, fmt.Errorf("%w (plugin %q, describe() version %q, which ends with %q)",
			ErrPluginBuiltFromUncommittedChanges(), opts.Identity, describeVersion, uncommittedChangesMarker)
	}

	if opts.Entity != nil {
		if opts.Entity.Version != describeVersion {
			return ValidateReport{}, fmt.Errorf(
				"plugin %q: recorded metadata.version %q does not match the plugin describe() version %q: %w",
				opts.Identity, opts.Entity.Version, describeVersion, ErrWasmPluginVersionMismatch())
		}
		report.Notices = UnderDeclaredNotices(opts.Entity.CuratedSurface, desc)
	}

	return report, nil
}

// HostFunctionsEqual reports whether two host-function name sets are equal as
// sets — order and duplicates ignored — treating a nil record and an explicit
// empty set as equal. It is the single home of the machine-owned host-function
// record comparison: the publish validation door and the record-sync tooling
// both consult it, so a record verifies consistently whether a binary imports
// nothing and its record is omitted or an explicit empty list.
func HostFunctionsEqual(a, b []string) bool {
	seen := make(map[string]bool, len(a))
	for _, x := range a {
		seen[x] = true
	}
	other := make(map[string]bool, len(b))
	for _, x := range b {
		other[x] = true
	}
	if len(seen) != len(other) {
		return false
	}
	for x := range seen {
		if !other[x] {
			return false
		}
	}
	return true
}
