// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

// Package plugintest is a public, in-process validation facade over the
// plugin publish-side validation door. It lets a Go plugin author validate a
// .wasm binary against a declared plugin type — and, in entity mode, against an
// authored Plugin entity surface — without loading the CLI. A binary that
// passes here passes the same checks the publish flow runs before any bytes
// land, so an author can gate the binary in an ordinary Go test.
//
// The facade projects only the fields a consumer needs from the underlying
// descriptor, so the internal descriptor and validation shapes stay private:
// a third-party author imports this package and pkg/types, nothing internal.
package plugintest

import (
	"context"

	"github.com/aggrega-ai/aggrega-cli/internal/wasm"
)

// The validation door's failure sentinels, re-exported so a consumer can
// errors.Is against them without importing an internal package. They alias the
// same error values the CLI publish flow reports, so a check written against
// them here matches a failure the publish flow would raise.

// ErrTypeMismatch reports that the declared plugin type disagrees with the
// type the binary's describe() self-report states.
func ErrTypeMismatch() error { return wasm.ErrWasmPluginTypeMismatch() }

// ErrRejected reports that the sandbox refused the binary at the describe
// load — an illegal host-function import, an unsupported ABI, a malformed
// describe() self-report, or a similar descriptor-level violation.
func ErrRejected() error { return wasm.ErrWasmManifestRejected() }

// ErrContractMismatch reports that an authored capability claim disagrees
// with the binary's describe() self-report. Entity mode only.
func ErrContractMismatch() error { return wasm.ErrWasmContractMismatch() }

// ErrCommandsMismatch reports that an authored command mirror disagrees with
// the commands the binary's describe() self-report declares. Entity mode
// only.
func ErrCommandsMismatch() error { return wasm.ErrWasmCommandsMirrorMismatch() }

// ErrHostFunctionsMismatch reports that an authored host-function record
// disagrees with the functions the binary actually imports. Entity mode
// only.
func ErrHostFunctionsMismatch() error { return wasm.ErrWasmHostFunctionsStampMismatch() }

// Options configures a binary-only guest validation.
type Options struct {
	// DeclaredType is the plugin type the caller declares the bytes to be,
	// checked against the binary's describe() self-report. Required.
	DeclaredType string

	// MaxMemoryPages caps the guest's linear memory for the describe load. Zero
	// applies the platform default cap.
	MaxMemoryPages uint32

	// Identity names the plugin in every diagnostic the validation emits.
	Identity string
}

// Report is the public projection of a successful guest validation: the
// binary's self-reported plugin type, its offered export names in declared
// order, and any advisory under-declaration notices. It copies only the fields
// a consumer needs from the internal descriptor.
type Report struct {
	// PluginType is the plugin type the binary's describe() self-report states.
	PluginType string

	// Exports is the export names the binary offers, in declared order.
	Exports []string

	// Notices is the advisory list of surfaces the binary offers that an entity
	// does not declare. Empty in binary-only validation; populated only in
	// entity mode when the binary offers more than the entity curates.
	Notices []string
}

// ValidateGuest validates .wasm bytes against a declared plugin type in
// process: it reads the binary's describe() self-report and checks the declared
// type against the reported one. It runs the binary-only checks — no authored
// entity surface is cross-checked. On success it returns the projected report;
// on failure it returns the halting error, which a caller may errors.Is against
// this package's sentinels.
func ValidateGuest(bytes []byte, opts Options) (Report, error) {
	report, err := wasm.ValidatePlugin(context.Background(), bytes, wasm.ValidateOptions{
		DeclaredType:   opts.DeclaredType,
		MaxMemoryPages: opts.MaxMemoryPages,
		Identity:       opts.Identity,
	})
	if err != nil {
		return Report{}, err
	}
	return projectReport(report), nil
}

// projectReport copies the consumer-facing fields out of the internal validate
// report, so the internal descriptor shape never crosses the public boundary.
func projectReport(r wasm.ValidateReport) Report {
	exports := make([]string, len(r.Descriptor.Exports))
	for i, e := range r.Descriptor.Exports {
		exports[i] = e.Name
	}
	return Report{
		PluginType: r.Descriptor.PluginType,
		Exports:    exports,
		Notices:    r.Notices,
	}
}
