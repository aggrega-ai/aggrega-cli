// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package wasm

import (
	"fmt"

	abi "github.com/aggrega-ai/aggrega-abi-go/pkg/abi"
	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
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
func ErrWasmManifestRejected() error { return errWasmManifestRejected }

var errWasmManifestRejected = cerrors.Register("wasm-manifest-rejected", cerrors.Meta{
	ShortCode: "E0085",
	Message:   "wasm: plugin rejected by the sandbox",
})

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
func ErrWasmGuestPanic() error { return errWasmGuestPanic }

var errWasmGuestPanic = cerrors.Register("wasm-guest-panic", cerrors.Meta{
	ShortCode: "E0083",
	Message:   "wasm: guest plugin panicked during call",
})

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
func ErrWasmPluginExited() error { return errWasmPluginExited }

var errWasmPluginExited = cerrors.Register("wasm-plugin-exited", cerrors.Meta{
	ShortCode: "E0086",
	Message:   "wasm: plugin invoked proc_exit",
})

// ErrWasmGuestError indicates that a guest plugin ran to completion but
// reported, through the optional error field on its socket-output envelope, that
// it could not complete the operation. It is the structured-return counterpart
// to the crash-path sentinels ErrWasmGuestPanic and ErrWasmPluginExited: all
// three surface a guest-side failure through one host taxonomy that callers
// classify with errors.Is, by distinct transport (a panic escaping the call, a
// proc_exit, or a message the guest itself placed on its output). The wrapping
// error carries the export name and the guest's message.
func ErrWasmGuestError() error { return errWasmGuestError }

var errWasmGuestError = cerrors.Register("wasm-guest-error", cerrors.Meta{
	ShortCode: "E0082",
	Message:   "wasm: guest plugin reported an error",
})

// GuestOutput is a socket-output envelope the host decodes strictly from the
// textual encoding. The single method is the generated codec's strict decode
// entry point: it refuses a top-level field the envelope's shape does not
// declare instead of skipping it.
//
// GuestOutputCBOR is the same contract read from the binary encoding, for the
// sockets whose wire carries that. The two are separate only while both
// encodings are in use on the socket wire; see DecodeGuestOutputCBOR for when
// they become one.
type GuestOutput interface {
	UnmarshalJSONStrict(data []byte) error
}

// GuestOutputCBOR is a socket-output envelope the host decodes strictly from the
// binary encoding. The single method is the generated codec's strict decode
// entry point for that encoding: it refuses a top-level field the envelope's
// shape does not declare instead of skipping it.
//
// It is GuestOutput's counterpart, and exists beside it for the same reason
// DecodeGuestOutputCBOR exists beside DecodeGuestOutput.
type GuestOutputCBOR interface {
	DecodeCBORStrict(data []byte) error
}

// ErrWasmOutputShape indicates that a guest plugin returned an output that is
// not the shape the socket it was called at reads. The socket decodes against
// the fields its own output shape declares and refuses everything else, so the
// condition covers the whole class rather than one member of it: a top-level
// field the shape does not declare, a field whose value is not the type the
// shape declares, and bytes that are not a well-formed object at all.
//
// It halts rather than reading what it can, and the reason is that at this
// boundary reading what it can is silent. A socket's entry function is
// overridable per attachment, so an override can name an export whose output is
// a different shape entirely — and skipping the fields that do not fit yields an
// empty envelope, which is itself a legitimate outcome (no verdict fired, no
// finding raised). The build would then proceed exactly as if the plugin had
// approved it.
//
// The wrapping error names the export the output came back from and carries the
// decode's own reason after it — the field that does not belong, or what about
// the bytes could not be read — so an author sees which entry function was
// called and what came back instead of this socket's shape.
func ErrWasmOutputShape() error { return errWasmOutputShape }

var errWasmOutputShape = cerrors.Register("wasm-output-shape", cerrors.Meta{
	ShortCode: "E0248",
	Message:   "wasm: guest plugin returned an output that is not the shape the socket reads",
})

// DecodeGuestOutput decodes a guest's socket output into dst and returns an
// error wrapping ErrWasmOutputShape, naming the export, when the bytes are not
// the shape the socket reads.
//
// It is the first of the three guest-envelope contract checks a socket runs at
// one boundary, ahead of GuestReturnedError and GuestDiagnosticSeverities: the
// output has to be this socket's shape before its error field or its diagnostics
// mean anything. It refuses rather than skips because skipping is silent in the
// one direction that matters. A socket's entry function is overridable per
// attachment, so an override can name an export whose output is a different
// shape entirely — and a lenient decode reads such an output into an empty
// envelope, which is itself a legitimate result (no verdict fired, no finding
// raised). The two are then byte-indistinguishable, and the build proceeds as if
// the plugin had approved it.
//
// export is the guest function the output came back from. The returned error
// names it, and the codec names the field that does not belong; together the two
// identify the entry function the attachment should have named instead. The
// condition's sentence is said once, by the sentinel: this wrap contributes the
// export and the codec's own detail and repeats neither.
//
// This is the entry point for a socket whose wire carries the textual encoding.
// DecodeGuestOutputCBOR is the same check for a socket whose wire carries the
// binary one; that declaration records when the two become a single function.
func DecodeGuestOutput(export string, out []byte, dst GuestOutput) error {
	if err := dst.UnmarshalJSONStrict(out); err != nil {
		return fmt.Errorf("call %q: %w: %s", export, ErrWasmOutputShape(), err)
	}
	return nil
}

// DecodeGuestOutputCBOR decodes a guest's socket output into dst and returns an
// error wrapping ErrWasmOutputShape, naming the export, when the bytes are not
// the shape the socket reads. It is DecodeGuestOutput read through the binary
// encoding: the same condition, the same wrapping, the same refusal, and the
// same reason for refusing rather than skipping — a lenient decode of an output
// that is a different shape entirely yields an empty envelope, which is itself a
// legitimate result, so the two would be indistinguishable and the build would
// proceed as if the plugin had approved it.
//
// The two functions are deliberately separate rather than one function that
// branches on the encoding, and the reason is which mistake is visible. A
// duplicated wrapping is a cost a test can see and pin. A single function
// carrying every socket would have to be told, at each of its call sites, which
// encoding that socket speaks — and a site told wrong moves a socket's wire with
// nothing failing.
//
// They become one function when the last socket reading a textual output has
// moved: at that point DecodeGuestOutput has no caller left, and this one keeps
// the name.
func DecodeGuestOutputCBOR(export string, out []byte, dst GuestOutputCBOR) error {
	if err := dst.DecodeCBORStrict(out); err != nil {
		return fmt.Errorf("call %q: %w: %s", export, ErrWasmOutputShape(), err)
	}
	return nil
}

// GuestFailure is the failure a guest reported in-band on its socket output: the
// message, and the identifiers of the halting condition that message names where
// the socket's shape carries them.
//
// It is a struct rather than a list of parameters so a call site cannot transpose
// two identifiers of the same type, and so a socket whose output declares no code
// fields states that by setting only Message — the absence is then visible where
// the call is written rather than inferable from a positional empty string.
type GuestFailure struct {
	// Message is the failure text the guest reported. Empty is the success case.
	Message string
	// Code is the descriptive, machine-matchable identity of the condition the
	// guest's failure names, or empty where the guest named none.
	Code string
	// ShortCode is the compact identifier of that same condition — the letter E
	// and four digits — or empty where the guest named none.
	ShortCode string
}

// GuestReturnedError maps a guest-returned socket-output failure onto the host
// taxonomy: it returns an error wrapping ErrWasmGuestError (naming the export and
// carrying the message) when f carries a message, and nil when it does not (the
// success case). A socket consumer calls it right after decoding a guest output so
// a guest that reports "I could not complete" halts through the same
// classification a guest crash does.
//
// Where the guest also named the condition its failure is about, the returned
// error reports that condition's identifiers rather than the host's own, so the
// diagnostic a reader meets carries the code the guest published. Classification
// is unchanged either way: the guest condition wraps ErrWasmGuestError, so a
// caller matching the host taxonomy with errors.Is still matches.
//
// plugin identifies the guest, and it QUALIFIES the identifiers above rather than
// decorating the message: a code is unique only within whoever allocated it, and
// nothing coordinates two plugin authors, so two guests can publish one code for
// two unrelated conditions. The plugin the host loaded and invoked is what tells
// them apart, which is why it travels beside the export name as an argument of the
// host's own rather than as a field of GuestFailure — GuestFailure is what the
// guest reported in-band, and a guest can neither supply nor forge this. Pass
// whatever identity the calling socket knows the guest by, which is the same
// identity that socket's other failures name; GuestDiagnosticSeverities takes it
// on the same terms.
func GuestReturnedError(plugin, export string, f GuestFailure) error {
	if f.Message == "" {
		return nil
	}
	return fmt.Errorf("call %q: %w: %s", export, reportedOver(ErrWasmGuestError(), plugin, f), f.Message)
}

// reportedOver returns the error the wrap above names as the condition: host on
// its own, or the identifiers the guest reported presented over host, qualified by
// the plugin that raised them.
//
// It takes the host condition as an argument rather than reaching for it, so the
// one wrap above stays the one place this package names that condition
// constructively — which is the property the chokepoint guard beside this file
// reads, and it reads it by looking at exactly that argument position.
//
// The pairing rule is both-or-neither, and it is a rule rather than a tolerance.
// A condition has two identifiers naming one condition, so a report carrying one
// of them describes a condition the host cannot name completely — and attaching
// it would publish half an identity: one accessor answering for the guest's
// condition and the other for the host's, on one error, with nothing to say the
// two are different conditions. A half report therefore names no condition at
// all, and the host's condition stands, with the guest's message carrying
// everything the reader needs. The guest's own identity is not forged here and
// could not be: the identifiers travel as data, and there is no path from a code
// back to the identity that declared it.
//
// The plugin rides on that same decision rather than on a test of its own, and
// deliberately: the qualifier says whose code this is, so it means nothing where
// there is no code. A half report and a report with no condition both fall
// through to the host's own condition, which is the compiler's and needs no
// qualification.
func reportedOver(host error, plugin string, f GuestFailure) error {
	if f.Code == "" || f.ShortCode == "" {
		return host
	}
	return &reportedCondition{host: host, plugin: plugin, code: f.Code, shortCode: f.ShortCode}
}

// reportedCondition presents the identifiers a guest reported for the condition
// its failure names, over the host condition it stands on, qualified by the
// plugin that raised it.
//
// Error returns the host condition's own registered sentence and not a second
// copy of it: the wording has one home, and this type contributes identifiers
// rather than words. Unwrap returns that condition, so the whole host taxonomy
// still matches beneath these identifiers.
//
// The two code reads report exactly what the guest published and nothing else,
// unqualified. A machine consumer matching a code is matching the identifier the
// guest allocated, and folding the plugin into it would hand that consumer a
// value that is not a code — and, for the short code, one that is not the letter
// E and four digits either. The plugin is a third read beside them, and the
// surface that composes a diagnostic for a reader is what joins the two.
type reportedCondition struct {
	host      error
	plugin    string
	code      string
	shortCode string
}

func (c *reportedCondition) Error() string { return c.host.Error() }

func (c *reportedCondition) Unwrap() error { return c.host }

func (c *reportedCondition) ErrorCode() string { return c.code }

func (c *reportedCondition) ErrorShortCode() string { return c.shortCode }

func (c *reportedCondition) ErrorRaisingPlugin() string { return c.plugin }

// ErrWasmDiagnosticSeverity indicates that a guest plugin returned a diagnostic
// whose severity is not one of the values the shared diagnostic contract
// declares. The contract closes the severity to a fixed set — halt stops the
// build and names what to fix, warn surfaces alongside a build that still
// completes, info records the observation and leaves the build outcome
// unchanged — and a value outside that set is a plugin the host cannot route.
//
// It halts rather than being coerced to a default, and the reason is not
// tidiness. A host that silently skipped an unroutable severity would treat it
// as "nothing to do", which on a governance surface reads as an allow: a verdict
// the plugin raised would pass the build without ever being reported. Coercing
// it to the halting value would instead reject a build on a value nobody chose.
// Both are silent, and the contract is stated precisely so neither is necessary:
// the value is wrong, the host says so, and the plugin author fixes it.
//
// The wrapping error names the plugin, the export it was called through, the
// offending value, and the values the contract declares, so an author sees which
// plugin emitted what and what it should have emitted instead.
func ErrWasmDiagnosticSeverity() error { return errWasmDiagnosticSeverity }

var errWasmDiagnosticSeverity = cerrors.Register("wasm-diagnostic-severity-invalid", cerrors.Meta{
	ShortCode: "E0081",
	Message:   "wasm: guest plugin returned a diagnostic whose severity the contract does not declare",
})

// GuestDiagnosticSeverities checks every diagnostic a guest returned against the
// severities the shared diagnostic contract declares, returning an error
// wrapping ErrWasmDiagnosticSeverity for the first entry outside the set and nil
// when every entry is inside it (the success case, including an empty list).
//
// This is the one home for that decision. Every socket that decodes a
// diagnostic-carrying guest output calls it right after decoding — beside the
// GuestReturnedError call that checks the same output's error field — so the two
// guest-envelope contract checks run at the same boundary and a diagnostic that
// reaches any routing site downstream is one the host can route. Putting the
// decision here rather than at each routing site is what keeps the routing sites
// from disagreeing with one another about what an unroutable value means, and it
// is also the only layer where the plugin's identity is still in hand: a
// diagnostic that has been projected onto a report or joined into a build result
// no longer knows which plugin raised it.
//
// plugin identifies the guest for the message — whatever identity the calling
// socket knows it by, which is the same identity that socket's other failures
// name. export is the guest function the output came back from.
func GuestDiagnosticSeverities(plugin, export string, diags []abi.Diagnostic) error {
	for i, d := range diags {
		switch d.Severity {
		case abi.SeverityHalt, abi.SeverityWarn, abi.SeverityInfo:
			continue
		}
		return fmt.Errorf(
			"plugin %q call %q: %w: diagnostic %d has severity %q; the declared severities are %q, %q and %q",
			plugin, export, ErrWasmDiagnosticSeverity(), i, d.Severity,
			abi.SeverityHalt, abi.SeverityWarn, abi.SeverityInfo)
	}
	return nil
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
func ErrWasmContractMismatch() error { return errWasmContractMismatch }

var errWasmContractMismatch = cerrors.Register("wasm-contract-mismatch", cerrors.Meta{
	ShortCode: "E0080",
	Message:   "wasm: entity contract claim does not match plugin describe()",
})

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
func ErrWasmHostFunctionsStampMismatch() error { return errWasmHostFunctionsStampMismatch }

var errWasmHostFunctionsStampMismatch = cerrors.Register("wasm-host-function-set-mismatch", cerrors.Meta{
	ShortCode: "E0084",
	Message:   "wasm: stamped host-function record does not match .wasm imports",
})

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
func ErrWasmPluginTypeMismatch() error { return errWasmPluginTypeMismatch }

var errWasmPluginTypeMismatch = cerrors.Register("wasm-plugin-type-mismatch", cerrors.Meta{
	ShortCode: "E0087",
	Message:   "wasm: declared plugin type does not match plugin describe()",
})

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
func ErrWasmCommandsMirrorMismatch() error { return errWasmCommandsMirrorMismatch }

var errWasmCommandsMirrorMismatch = cerrors.Register("wasm-command-set-mismatch", cerrors.Meta{
	ShortCode: "E0079",
	Message:   "wasm: recorded command mirror does not match plugin describe()",
})

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
func ErrProducerConsumptionReentry() error { return errProducerConsumptionReentry }

var errProducerConsumptionReentry = cerrors.Register("wasm-nested-producer-consumption-denied", cerrors.Meta{
	ShortCode: "E0078",
	Message:   "producer-consumption re-entry denied: a producer reached through the host may not issue a further producer-consumption call",
})

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
func ErrWasmPluginVersionMismatch() error { return errWasmPluginVersionMismatch }

var errWasmPluginVersionMismatch = cerrors.Register("wasm-plugin-version-mismatch", cerrors.Meta{
	ShortCode: "E0088",
	Message:   "wasm: entity metadata.version does not match plugin describe()",
})
