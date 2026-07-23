// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package wasm

import (
	"errors"
	"fmt"
)

// CodedHostError carries a machine-matchable host-function wire error code
// alongside an underlying error. A seam adapter that owns the dependency for a
// cross-package failure (for example a parser-resolution failure whose sentinel
// lives outside this package) wraps its error in a CodedHostError, mapping the
// internal failure to the public wire code at its own layer. The host-function
// boundary reads the code with errors.As and sets it on the reply envelope,
// without importing the seam's packages. Error and Unwrap delegate to the wrapped
// error, so the human message and errors.Is matching against the underlying
// sentinel are both preserved.
type CodedHostError struct {
	Code string
	Err  error
}

func (e *CodedHostError) Error() string { return e.Err.Error() }

func (e *CodedHostError) Unwrap() error { return e.Err }

// ErrWasmManifestRejected indicates that the sandbox rejected a plugin
// because it declares or requires a capability, or reports a
// descriptor, the sandbox does not grant or accept. It is the shared
// sentinel for descriptor-rejection failures across the plugin-loading
// surface. Reasons include:
//
//   - non-empty AllowedHosts (outbound network access)
//   - non-empty AllowedPaths (filesystem access)
//   - declared host functions outside the curated allowlist
//   - imports declaring a module namespace outside the allowed set
//     (wasi_snapshot_preview1, extism:host/env, extism:host/user)
//   - an ABI version the host does not support
//   - a missing, undeclared, or malformed self-reported export
//   - an invalid stage, scope, or other describe() field value
//   - a describe() self-report that is not valid JSON
//
// WASI stdio passthrough and wall-time access remain denied by
// construction: the Loader enables WASI but hands the Extism runtime a
// capability-denied ModuleConfig (frozen observable wall time, real
// monotonic time, real entropy, discarded stdio, no filesystem or
// environment, a single synthetic program name as argv). The guest
// bootstraps against real WASI behaviour with zero ambient authority,
// so any I/O call fails at the call boundary; see wasi_imports.go for
// the import-module allowlist and loader.go for the sandbox
// configuration.
//
// Errors wrapping this sentinel name the violated surface and the
// plugin identifier so callers see exactly which surface caused the
// rejection.
var ErrWasmManifestRejected = errors.New("wasm: plugin rejected by the sandbox")

// ErrWasmGuestPanic indicates that guest plugin code panicked during a
// Plugin.Call invocation. The Loader's Call method wraps the Extism
// call boundary with `defer recover()`; on panic, the recovered value
// is wrapped in an error that satisfies errors.Is against this
// sentinel and carries the plugin identifier plus the called function
// name in its message.
//
// Note: Extism translates many Wasm-side traps (for example the
// unreachable instruction, divide-by-zero, out-of-bounds memory
// access) into error return values rather than Go panics. This
// sentinel is reserved for panics that escape Extism's own error
// envelope — the safety net behind the SDK's normal error path.
var ErrWasmGuestPanic = errors.New("wasm: guest plugin panicked during call")

// ErrWasmPluginExited indicates that a guest plugin invoked WASI
// proc_exit with a non-zero code during a Plugin.Call. The Extism
// runtime surfaces the exit as a non-zero exit code on the call's
// return path; Plugin.Call converts that into an error wrapping this
// sentinel. The plugin's execution is stopped; the host process is
// unaffected. Plugin authors who reach proc_exit are doing something
// the Aggrega plugin model does not support — plugins return their
// output via the Extism call/return ABI, not via process exit.
//
// The wrapping error carries the exit code the plugin passed to
// proc_exit so callers see the failure code in the message.
var ErrWasmPluginExited = errors.New("wasm: plugin invoked proc_exit")

// ErrWasmGuestError indicates that a guest plugin ran to completion but
// reported, through the optional error field on its socket-output envelope, that
// it could not complete the operation. It is the structured-return counterpart
// to the crash-path sentinels ErrWasmGuestPanic and ErrWasmPluginExited: all
// three surface a guest-side failure through one host taxonomy that callers
// classify with errors.Is, by distinct transport (a panic escaping the call, a
// proc_exit, or a message the guest itself placed on its output). The wrapping
// error carries the export name and the guest's message.
var ErrWasmGuestError = errors.New("wasm: guest plugin reported an error")

// GuestReturnedError maps a guest-returned socket-output error message onto the
// host taxonomy: it returns an error wrapping ErrWasmGuestError (naming the
// export and carrying the message) when msg is non-empty, and nil when msg is
// empty (the success case). A socket consumer calls it right after decoding a
// guest output so a guest that reports "I could not complete" halts through the
// same classification a guest crash does.
func GuestReturnedError(export, msg string) error {
	if msg == "" {
		return nil
	}
	return fmt.Errorf("call %q: %w: %s", export, ErrWasmGuestError, msg)
}

// ErrWasmContractMismatch indicates that a catalog Plugin entity's
// authored contract claim disagrees with the plugin's self-reported describe()
// descriptor at load. The cross-check is per-export, keyed by export name:
// either the entity claims an export the plugin's describe() does not declare, or
// a claimed export's operates or scope field does not match the plugin's
// self-report. This is a structural contract check — the authored claim and the
// plugin's own report must agree — not semantic policing of what an export does.
//
// The wrapping error names the entity, the offending export, the disagreeing
// field, and both values so the author sees exactly which claim diverged from
// the plugin's self-report. The same cross-check runs at publish time, so a
// mismatch surfaces before the entity is published rather than at load.
var ErrWasmContractMismatch = errors.New("wasm: entity contract claim does not match plugin describe()")

// ErrWasmHostFunctionsStampMismatch indicates that a Plugin entity's stamped
// runtime host-function record disagrees with the host functions the verified
// .wasm binary actually imports. The host-function record is machine-owned: the
// publish flow derives it from the binary's import section and stamps the
// canonical (sorted, de-duplicated) set into the entity. At pull, after the
// .wasm is content-digest-verified, the host re-derives the set from the
// verified bytes and compares it to the stamp; a divergence in either direction
// is a Loud Guardrail that halts the pull.
//
// This is defense-in-depth over the content-digest pin: it uniquely catches a
// binary published by something other than the aggrega publish flow (which would
// carry no stamp, or a stamp that does not match its own bytes) and a
// publish-side stamping bug, neither of which the digest pin alone surfaces.
//
// The wrapping error names the plugin, the stamped set, and the derived set so
// the divergence is visible. This sentinel is distinct from
// ErrWasmContractMismatch: that covers the export operates/scope claim against
// describe(); this covers the stamped host-function record against the verified
// binary's imports.
var ErrWasmHostFunctionsStampMismatch = errors.New("wasm: stamped host-function record does not match .wasm imports")

// ErrWasmPluginTypeMismatch indicates that the plugin type expected at a load
// site disagrees with the plugin type the .wasm binary self-reports in
// describe(). The expected type is the load site's statement of what kind of
// plugin it is loading — the socket it is attached at, or the type an entity
// declares; the describe() report is the binary's own statement of its kind. The
// load gate validates the two against each other so a guest of the wrong kind
// never reaches its socket's dispatch — a linter socket fronting a provider
// binary, a mutator guest at the policy socket, a non-CLI guest run as a terminal
// command — and so an entity can never publish a binary of a different kind than
// it declares.
//
// This is a structural identity check, distinct from the capability and command
// cross-checks: those compare an authored surface against the binary's exports
// and commands; this compares the single plugin-type label on each side. The
// wrapping error names the plugin identity, the expected type, and the reported
// type so the author sees exactly which two kinds diverged.
var ErrWasmPluginTypeMismatch = errors.New("wasm: declared plugin type does not match plugin describe()")

// ErrWasmCommandsMirrorMismatch indicates that a Plugin entity's recorded
// command mirror disagrees with the commands the plugin's describe() self-report
// declares. The command mirror is machine-owned: the stamp step reads the CLI
// commands a plugin contributes from its describe() self-report and records the
// canonical (describe()-declared order) set into the entity. Publish verifies the
// committed mirror against the binary before any bytes land, and the load gate
// verifies it again when an entity supplies the mirror; a divergence in name,
// short-help text, or order is a Loud Guardrail that halts and directs the author
// to re-record the set.
//
// The full-equality comparison is by design: unlike the authored export and
// capability claims — which are curated subsets an author may legitimately
// under-declare — the command mirror is a machine record that must reflect the
// binary exactly. The check runs only when an entity supplies the mirror; a bare
// .wasm load carries no mirror and is not checked.
//
// The wrapping error names the plugin, the recorded set, and the described set so
// the divergence is visible. This sentinel is distinct from
// ErrWasmHostFunctionsStampMismatch: that covers the stamped host-function record
// against the verified binary's imports; this covers the recorded command mirror
// against the plugin's describe() self-report.
var ErrWasmCommandsMirrorMismatch = errors.New("wasm: recorded command mirror does not match plugin describe()")

// ErrProducerConsumptionReentry indicates that a producer reached through the
// host-mediated producer-consumption seam attempted a further producer-consumption
// call of its own. The mechanism lets a consumer obtain a producer's output
// through the host — a consumer at the base level drives one producer — but a
// producer running through the seam that calls back into it is a re-entrant cycle
// the host refuses, bounding the mechanism to a single producer level.
//
// The denial is a Loud Guardrail: the seam halts the nested call rather than
// recursing, and the failure crosses to the calling guest in-band as a message
// carrying this sentinel's text. The sentinel is producer- and consumer-agnostic
// — it guards any consumer/producer pair routed through the one shared seam, not
// a single socket pair.
var ErrProducerConsumptionReentry = errors.New("producer-consumption re-entry denied: a producer reached through the host may not issue a further producer-consumption call")

// ErrWasmPluginVersionMismatch indicates that a Plugin entity's recorded
// metadata.version disagrees with the build-provenance version the plugin's
// describe() self-report declares. The recorded version is machine-sourced: the
// entity's metadata.version is derived from the guest's describe() version when
// the entity is generated, so the two agree by construction. Publish verifies the
// committed version against the binary before any bytes land; a hand-edit that
// diverges the recorded version from the guest's build-provenance version is a
// Loud Guardrail that halts and directs the author to re-record the version.
//
// A guest that carries no build-provenance version (an unstamped build) reports
// an empty version, which the check treats as the value to match: the entity's
// metadata.version must equal it exactly. The comparison is by design an exact
// string match, so a published entity's metadata.version always reflects the
// version its binary actually reports.
//
// The wrapping error names the plugin, the recorded version, and the described
// version so the divergence is visible. This sentinel is distinct from
// ErrWasmCommandsMirrorMismatch and ErrWasmHostFunctionsStampMismatch: those
// cover the command mirror and host-function record; this covers the recorded
// metadata.version against the plugin's describe() build-provenance version.
var ErrWasmPluginVersionMismatch = errors.New("wasm: entity metadata.version does not match plugin describe()")
