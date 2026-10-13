// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package wasm

import (
	"errors"
	"fmt"
	"strings"

	abi "github.com/aggrega-ai/aggrega-abi-go/pkg/abi"
	"github.com/aggrega-ai/aggrega-cli/internal/haltref"
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
	RulePlace: cerrors.NotARule("The sandbox refused a plugin because its descriptor declares or requires something the " +
		"sandbox does not grant or accept. It reports the sandbox's own admission bound on a guest's " +
		"self-report rather than a rule over anything an author wrote."),
})

// ErrWasmGuestPanic indicates that guest plugin code panicked during a
// call into the plugin. The plugin's call core wraps the Extism
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
	RulePlace: cerrors.NotARule("Guest plugin code panicked during a call and the panic escaped the runtime's own error " +
		"envelope. It reports a guest that trapped."),
})

// ErrWasmPluginExited indicates that a guest plugin invoked WASI
// proc_exit with a non-zero code during a call into the plugin. The Extism
// runtime surfaces the exit as a non-zero exit code on the call's
// return path; the compile-time call converts that into an error wrapping this
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
	RulePlace: cerrors.NotARule("A guest plugin invoked the WASI process-exit call with a non-zero code during a call. " +
		"Plugins return their output through the call-and-return path, so it reports a guest that " +
		"stopped itself rather than a rule refusing anything."),
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
	RulePlace: cerrors.NotARule("A guest plugin ran to completion and reported, on its own output envelope, that it could not " +
		"complete the operation. It carries that guest's message and reports a failure the guest " +
		"declared rather than a rule refusing anything."),
})

// The generic conditions a plugin reaches for.
//
// A plugin that stops a build reports a failure in-band on its socket output: a
// sentence of its own, and the identity of the condition that sentence names.
// The identity is not the plugin's to allocate, and the reason is that a plugin
// author cannot be coordinated with. There is nowhere for a stranger's plugin to
// publish a number — the published correspondence between codes and conditions
// is composed from this module's own declarations, and the self-report the host
// reads at load carries no channel for a condition at all — so a number a plugin
// minted for itself would be one no reader could look up and no registry could
// hold distinct from anybody else's.
//
// So the identity a plugin reports is one the COMPILER declares, and the host
// admits any of them. That buys the single property worth having: every code a
// reader meets resolves, so `aggrega explain` answers for it from the binary's own
// declarations, with no documentation to fetch and no guest to load.
//
// Most plugin failures have no compiler condition that fits, so the compiler
// declares a small GENERIC set naming the KINDS of failure a plugin reports, and a
// plugin reaches for one of those. Each names what happened at the socket and
// nothing about any particular target: that a construct the build declares has no
// expression, that an input could not be resolved to one value, that two produced
// things want one name, that a value has no representation. WHICH construct, which
// input, which name and which value are the plugin's own business, and the plugin
// says so in its own sentence, which the host carries through and prints after the
// condition's.
//
// That division is what keeps every target-specific fact on the far side of the
// plugin boundary while the identity a reader quotes stays one the compiler owns.
//
// The set gains a member when a genuinely new KIND of failure appears, which is
// a change to the compiler. It is not widened to accommodate a particular
// target: a proposed member whose sentence names one is the wrong member rather
// than a missing one, because such a sentence is domain knowledge that has
// crossed back over the boundary this set exists to hold. Nor is it widened to
// cover a failure some compiler condition already names precisely, since a plugin
// with a precise identity available reports less than it knows by reaching here.

// ErrPluginCannotExpressInput indicates that a plugin has no expression for a
// construct the build declares. The construct has no counterpart at all in what
// the plugin emits, so the repair is to stop declaring it against this plugin
// rather than to change a value it carries.
//
// It is one of the conditions a plugin chooses from. Which construct is in the
// plugin's own sentence; this condition says only that there is no expression
// for it.
//
// A guest core RAISES this condition as well as matching it, and it does so by
// wrapping the value this accessor returns rather than by handing an identity to
// the constructor that demands one. The accessor therefore publishes a plain
// error, which is also what keeps the condition visible to the generator that
// composes the published error reference: that generator recognises an accessor
// by its shape, and the shape it recognises returns error.
func ErrPluginCannotExpressInput() error { return errPluginCannotExpressInput }

var errPluginCannotExpressInput = cerrors.Register("plugin-cannot-express-input", cerrors.Meta{
	ShortCode: "E0288",
	Message:   "a plugin has no expression for a construct this build declares",
	RulePlace: cerrors.RuleEvaluatedElsewhere("A guest plugin decides whether it can express a construct, inside its own module and over " +
		"the input the host handed it, and this package raises the condition on the failure that " +
		"guest returned. The rule runs where no view of a compilation reaches, so there is no view " +
		"for the rule dispatch to invoke a rule over."),
})

// ErrPluginRequiredInputUnresolved indicates that a plugin could not resolve
// something it requires to exactly one value. Zero and many are one failure at
// the plugin's input boundary — it cannot pick — so both arrive here, and the
// plugin's own sentence says which it met.
//
// It is one of the conditions a plugin chooses from.
func ErrPluginRequiredInputUnresolved() error { return errPluginRequiredInputUnresolved }

var errPluginRequiredInputUnresolved = cerrors.Register("plugin-required-input-unresolved", cerrors.Meta{
	ShortCode: "E0289",
	Message:   "a plugin could not resolve something it requires to exactly one value",
	RulePlace: cerrors.RuleEvaluatedElsewhere("A guest plugin resolves what it requires, inside its own module and over the input the host " +
		"handed it, and this package raises the condition on the failure that guest returned. The " +
		"rule runs where no view of a compilation reaches, so there is no view for the rule dispatch " +
		"to invoke a rule over."),
})

// ErrPluginOutputNameCollision indicates that two things a plugin produced
// occupy one name in its output. It is keyed on the consequence rather than on
// where either name came from: one slot, two things that must go in it, and
// emitting would silently drop one. Whether either name was authored or derived
// is the plugin's own business and is in its sentence.
//
// It is one of the conditions a plugin chooses from.
func ErrPluginOutputNameCollision() error { return errPluginOutputNameCollision }

var errPluginOutputNameCollision = cerrors.Register("plugin-output-name-collision", cerrors.Meta{
	ShortCode: "E0290",
	Message:   "two things a plugin produced occupy one name in its output",
	RulePlace: cerrors.RuleEvaluatedElsewhere("A guest plugin names the things it produces, inside its own module, and discovers the " +
		"collision there; this package raises the condition on the failure that guest returned. The " +
		"rule runs over the plugin's own output where no view of a compilation reaches, so there is " +
		"no view for the rule dispatch to invoke a rule over."),
})

// ErrPluginValueNotRepresentable indicates that a plugin has no representation
// for a value it was given. It is distinct from having no expression for a
// construct, and the difference is which repair the author needs: here the
// counterpart exists in what the plugin emits and this particular value does not
// fit it, so the value is what changes.
//
// It is one of the conditions a plugin chooses from.
func ErrPluginValueNotRepresentable() error { return errPluginValueNotRepresentable }

var errPluginValueNotRepresentable = cerrors.Register("plugin-value-not-representable", cerrors.Meta{
	ShortCode: "E0291",
	Message:   "a plugin has no representation for a value it was given",
	RulePlace: cerrors.RuleEvaluatedElsewhere("A guest plugin decides whether a value it was given can be represented, inside its own " +
		"module, and this package raises the condition on the failure that guest returned. The rule " +
		"runs where no view of a compilation reaches, so there is no view for the rule dispatch to " +
		"invoke a rule over."),
})

// ErrPluginFailureUnclassified indicates that a plugin reported a failure that
// none of the declared kinds names. It is the fallback and never a destination:
// a plugin reaching for it where one of the other conditions fits reports less
// than it knows.
//
// Its wording is deliberately "none of the declared kinds names" rather than "a
// plugin reported an error", so a reader who quotes it is told plainly that the
// plugin declined to classify. An over-used fallback is then legible in the wild
// rather than indistinguishable from a correct report.
//
// It is one of the conditions a plugin chooses from, and it is chosen rather
// than fallen into: a report naming no condition at all is a different state,
// and the two stay distinguishable because they answer different questions about
// whether the set is right. A rising count of deliberate fallbacks says the set
// is missing a kind; a rising count of reports naming nothing says plugins are
// not reading the contract.
func ErrPluginFailureUnclassified() error { return errPluginFailureUnclassified }

var errPluginFailureUnclassified = cerrors.Register("plugin-failure-unclassified", cerrors.Meta{
	ShortCode: "E0292",
	Message:   "a plugin reported a failure that none of the declared kinds names",
	RulePlace: cerrors.RuleEvaluatedElsewhere("A guest plugin decides that its failure fits none of the declared kinds, inside its own " +
		"module, and this package raises the condition on the failure that guest returned. The rule " +
		"runs where no view of a compilation reaches, so there is no view for the rule dispatch to " +
		"invoke a rule over."),
})

// guestSelectableConditions is the generic set above as one value, and it is what
// a refusal names when it tells a plugin's author what to reach for.
//
// It is NOT the admissible set. A guest may name any condition the compiler
// declares, and the reason is that the compiler's own conditions are not all
// raised in the compiler's own process: shared code the host and the guests both
// link raises registered conditions INSIDE a guest, which then cross this socket
// as guest failures. A set that admitted only the generic five would refuse those,
// and the refusal would accuse a plugin of a protocol violation for a mistake the
// build's author made in their own file.
//
// What the set is for is advice. These five name the KINDS of failure a plugin
// reports when it has no better identity to offer, so a refusal points at them
// rather than leaving an author to guess.
//
// It is a list of identities rather than a table keyed by code, deliberately.
// Their codes are read off the identities at the moment they are needed, so no
// code is written down a second time — a keyed table would be that second copy,
// and it would agree with the registrations until the day one of them moved.
//
// Declaration order is the order a refusal names them in, so one rejected input
// yields one message.
var guestSelectableConditions = []error{
	ErrPluginCannotExpressInput(),
	ErrPluginRequiredInputUnresolved(),
	ErrPluginOutputNameCollision(),
	ErrPluginValueNotRepresentable(),
	ErrPluginFailureUnclassified(),
}

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
	RulePlace: cerrors.NotARule("A guest plugin returned an output that is not the shape the socket it was called at reads " +
		"— an undeclared top-level field, a field of the wrong type, or bytes that are not a " +
		"well-formed object at all. It reports a guest whose returned output does not conform to that " +
		"socket's shape."),
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
	// guest's failure names, or empty where the guest named none. The conditions
	// a guest may name are the ones the compiler declares, so a value here is
	// resolved against the registry rather than carried on trust.
	Code string
	// ShortCode is the compact identifier of that same condition — the letter E
	// and four digits — or empty where the guest named none. It is resolved the
	// same way, and checked against the descriptive code beside it, so the two
	// identifiers on one report always name one condition.
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
// diagnostic a reader meets carries the code the condition is registered under.
// The condition has to be one this compiler declares: a guest naming a code that
// resolves to nothing, or one the numbering has retired, is refused loudly with
// its message still carried through. Classification is unchanged in every case:
// the returned error unwraps to ErrWasmGuestError, so a caller matching the host
// taxonomy with errors.Is still matches.
//
// plugin identifies the guest, and it QUALIFIES the identifiers above rather than
// decorating the message. A generic condition names a KIND of failure and no
// place, so two plugins reporting one code are reporting the same condition and a
// head carrying the code alone cannot tell a reader which of a build's attached
// guests to look at. The plugin the host loaded and invoked is what localises it,
// which is why it travels beside the export name as an argument of the host's own
// rather than as a field of GuestFailure — GuestFailure is what the guest reported
// in-band, and a guest can neither supply nor forge this. Pass whatever identity
// the calling socket knows the guest by, which is the same identity that socket's
// other failures name; GuestDiagnosticSeverities takes it on the same terms.
func GuestReturnedError(plugin, export string, f GuestFailure) error {
	if f.Message == "" {
		return nil
	}
	return fmt.Errorf("call %q: %w: %s", export, reportedOver(ErrWasmGuestError(), plugin, f), f.Message)
}

// reportedOver returns the error the wrap above names as the condition: host on
// its own, the condition the guest chose presented over host, or the refusal for
// a guest that chose one this socket does not declare — the last two qualified by
// the plugin the report came from.
//
// It takes the host condition as an argument rather than reaching for it, so the
// one wrap above stays the one place this package names that condition
// constructively — which is the property the chokepoint guard beside this file
// reads, and it reads it by looking at exactly that argument position.
//
// The pairing rule is both-or-neither, and it is a rule rather than a tolerance.
// A condition has two identifiers naming one condition, so a report carrying one
// of them describes a condition the host cannot name completely — and attaching
// it would publish half an identity: one accessor answering for one condition and
// the other for another, on one error, with nothing to say the two differ. A half
// report therefore names no condition at all, and the host's condition stands,
// with the guest's message carrying everything the reader needs.
//
// The plugin rides on that same decision rather than on a test of its own, and
// deliberately: the qualifier says which plugin met this condition, so it means
// nothing where there is no condition. A half report and a report with no
// condition both fall through to the host's own condition, which is the
// compiler's and needs no qualification.
//
// A report that names both identifiers is CHECKED rather than attached, and this
// is the one place that check happens — it is the single site where a guest's
// identifiers cross into an error the host builds. The refusal is loud and the
// guest's message survives it: a build that says only "this plugin misbehaved"
// would hide what actually broke, so the violation is named and the failure is
// still reported.
func reportedOver(host error, plugin string, f GuestFailure) error {
	if f.Code == "" || f.ShortCode == "" {
		return host
	}
	named, refused := guestSelected(f)
	if refused != "" {
		return &undeclaredGuestCondition{
			refusal: ErrWasmGuestConditionUndeclared(),
			host:    host,
			plugin:  plugin,
			offered: f.ShortCode,
			reason:  refused,
		}
	}
	return &reportedCondition{host: host, condition: named, plugin: plugin}
}

// guestSelected returns the condition a guest named, or the zero description and
// the reason the report names none this compiler declares.
//
// What it admits is every condition the compiler REGISTERS, and that is wider
// than the generic set a plugin reaches for. The reason is measured rather than
// permissive: shared compiler code is linked into the guests as well as into the
// host, so a condition that package registers is raised INSIDE a guest and crosses
// this socket as a guest failure, having been chosen by nobody. Admitting only the
// generic set refused those, which halted a build under a protocol violation
// naming the plugin for a mistake the build's own author had made — and took a
// resolvable identity away from the reader at the same time. A condition the
// compiler declares is one `aggrega explain` answers for, which is the whole
// property the identity exists to carry.
//
// What it still refuses are the two failures that leave a reader holding an
// identifier that resolves to nothing. A code no condition is registered under is
// one, and a RETIRED code is the other — the second being the one thing the
// numbering promises can never reach a terminal, and the one a guest could
// otherwise break by printing a retired number as a live condition.
//
// The residue this deliberately does not police is a plugin naming a real
// condition its build did not meet. Nothing the host can see distinguishes that
// from a correct report, it harms only that plugin's users, and it is the plugin
// author's to get right.
//
// The lookup is [haltref.Resolve]'s and is not re-rolled here. That function owns
// the short-code-then-descriptive-code-then-retired sequence, and it is the only
// path that tells a retired number from one that names nothing. A second copy of
// that sequence would agree with it on the day it was written and diverge the
// first time either was edited.
//
// Its refusals are read rather than surfaced, and that is deliberate: their text
// is written for a reader who typed a code into a terminal, and speaks to
// repairing a query rather than a plugin's report.
//
// The refusals run in the order a reader repairs them, and each says the sharpest
// true thing available at that point. A code resolving to nothing — or to a
// retired number — is refused first. A code whose spelling is not the one its
// condition is registered under is refused next: the host matches the registered
// bytes rather than normalising what a guest sent, because a host that corrected a
// guest's identifier would be deciding what the guest meant. A pair whose two
// identifiers name two different conditions is last, since each alone might be
// legitimate and it is the pairing that is wrong.
func guestSelected(f GuestFailure) (cerrors.Condition, string) {
	named, err := haltref.Resolve(f.ShortCode)
	if err != nil {
		var retired *haltref.RetiredCodeError
		if errors.As(err, &retired) {
			return cerrors.Condition{}, "that number is retired and names no condition now: " + retired.Note
		}
		return cerrors.Condition{}, "no condition is registered under it"
	}
	if named.ShortCode != f.ShortCode {
		return cerrors.Condition{}, fmt.Sprintf("that is not the spelling the condition it resolves to "+
			"is registered under, which is %s", named.ShortCode)
	}
	if named.Code != f.Code {
		return cerrors.Condition{}, fmt.Sprintf("the descriptive code reported beside it, %q, names a "+
			"different condition: that short code is registered for %s", f.Code, named.Code)
	}
	return named, ""
}

// selectableConditionList renders the generic conditions a plugin reaches for, in
// the set's own declaration order, for a refusal to name.
//
// A refusal names these rather than the admissible set, and the two are not the
// same thing. The admissible set is every condition the compiler declares, which
// is a list no diagnostic could usefully print and which would tell an author
// nothing about what to write. These five are the answer to that question.
//
// It is derived from the set rather than written out, so a member added to the
// set is named by every refusal without a second edit — and a refusal cannot come
// to advertise a condition the compiler no longer declares.
func selectableConditionList() string {
	named := make([]string, 0, len(guestSelectableConditions))
	for _, condition := range guestSelectableConditions {
		code, _ := cerrors.CodeOf(condition)
		short, _ := cerrors.ShortCodeOf(condition)
		named = append(named, code+" ("+short+")")
	}
	return strings.Join(named, ", ")
}

// ErrWasmGuestConditionUndeclared indicates that a guest plugin named a condition
// this compiler does not declare. A report naming a number no condition is
// registered under, a number the numbering has RETIRED, a spelling the
// registration does not carry, or a pair of identifiers that name two different
// conditions is a plugin that has broken the socket's contract.
//
// What unites those is the consequence rather than the shape: each leaves a reader
// holding an identifier that resolves to nothing. A condition the compiler
// declares is one `aggrega explain` answers for, and that is the whole property an
// identity crossing this socket exists to carry — so the test is whether the code
// resolves, not whether a plugin was the party that raised it. Shared compiler
// code linked into a guest raises the compiler's own conditions from inside it,
// and those resolve perfectly.
//
// It halts rather than being dropped or coerced, and neither alternative is
// silent in a way this boundary can afford. Dropping the identifiers and letting
// the generic guest-failure condition stand would publish a build that looks
// exactly like one from a plugin that named no condition at all, so the author
// who has to fix it would never learn there was anything to fix. Coercing the
// value to the nearest declared condition would print a condition the plugin did
// not name, over a sentence written for a different one.
//
// The build still reports what broke it. The wrapping error quotes the offending
// code, names the generic conditions a plugin reaches for when it has no better
// identity, and carries the plugin's own message through unchanged — loud about
// the violation, never silent about the failure, because a user whose build
// stopped is owed the failure whatever the plugin got wrong about naming it.
func ErrWasmGuestConditionUndeclared() error { return errWasmGuestConditionUndeclared }

var errWasmGuestConditionUndeclared = cerrors.Register("wasm-guest-condition-undeclared", cerrors.Meta{
	ShortCode: "E0293",
	Message:   "wasm: guest plugin named a condition the socket does not declare",
	RulePlace: cerrors.RuleEvaluatedElsewhere("This package evaluates the rule itself, at the plugin socket boundary: it tests the " +
		"condition a guest named against the set the socket declares, over the output that guest " +
		"returned. The subject is a returned output rather than a compilation, so there is no view " +
		"for the rule dispatch to invoke a rule over."),
})

// undeclaredGuestCondition is the refusal for a report naming a condition this
// compiler does not declare: the refusal's own identity, the host condition it
// stands on, the plugin the report came from, the code it offered, and why that
// code resolves to no condition.
//
// It reports the REFUSAL's identifiers and not the offered code. The offered code
// names no condition the compiler declares, so publishing it would hand a reader
// an identifier that resolves to nothing — which is the exact failure this
// condition exists to stop. The offered code is in the sentence instead, quoted,
// which is where the plugin's author needs it and where nothing will mistake it
// for a condition.
//
// It unwraps to the guest-failure condition as well as to the refusal, so the
// error still classifies as what it is. A caller matching the host taxonomy goes
// on matching, and a caller asking specifically whether a plugin broke the
// socket's contract matches the refusal.
//
// The plugin qualifier is carried as a read rather than written into the
// sentence, exactly as it is for a condition a guest chose correctly, so the
// surface that composes a diagnostic joins the two in one place. It is set here
// where it is not set on a half report, and the difference is what the condition
// is about: a half report falls through to the compiler's own condition, which no
// plugin raised, while this condition is a statement about one plugin's report
// and is unreadable without naming it.
type undeclaredGuestCondition struct {
	refusal error
	host    error
	plugin  string
	offered string
	reason  string
}

// Error opens with the refusal's registered sentence, then quotes the offending
// code, says why it resolves to no condition, and points at what to write instead.
// The wording of the refusal itself has one home, so this contributes the
// occurrence's own values and no second copy of that sentence.
//
// The advice names the generic set rather than the admissible one. A plugin may
// name any condition the compiler declares, and printing that list would be
// neither short enough to read nor an answer to the author's actual question,
// which is what to put on this report.
func (c *undeclaredGuestCondition) Error() string {
	return fmt.Sprintf("%s: the guest named %q, and %s; a plugin names a condition this compiler "+
		"declares, and reaches for one of %s when it has none better",
		c.refusal.Error(), c.offered, c.reason, selectableConditionList())
}

// Unwrap returns the host condition it stands on and Is answers for the refusal,
// so errors.Is matches both: the guest-failure taxonomy every socket consumer
// classifies with, and the specific contract violation. The two are split across
// an unwrap and a match rather than returned together, because a frame that
// unwraps to several errors is one the surface that renders a diagnostic walks
// PAST — it would take the members' bare sentences and drop the text of the frame
// above them, which here carries the plugin's own message.
func (c *undeclaredGuestCondition) Unwrap() error { return c.host }

func (c *undeclaredGuestCondition) Is(target error) bool { return target == c.refusal }

// ErrorCode and ErrorShortCode report the refusal's identifiers, read off the
// refusal itself rather than written out here, so the two spellings of this
// condition have one home and cannot come to disagree.
func (c *undeclaredGuestCondition) ErrorCode() string {
	code, _ := cerrors.CodeOf(c.refusal)
	return code
}

func (c *undeclaredGuestCondition) ErrorShortCode() string {
	short, _ := cerrors.ShortCodeOf(c.refusal)
	return short
}

func (c *undeclaredGuestCondition) ErrorRaisingPlugin() string { return c.plugin }

// reportedCondition presents the condition a guest named, over the host condition
// it stands on, qualified by the plugin that reported it.
//
// It holds the registry's DESCRIPTION of that condition rather than its identity,
// and that is forced rather than chosen. The admissible set is every condition the
// compiler declares, and the registry deliberately offers no path from a code to
// an identity: its lookups return a description precisely so a code stays an
// OUTPUT and never becomes an input to matching. Adding such a path to satisfy
// this type would break that sealing for every caller in order to serve one.
//
// The description is not the strings the guest sent, which is the distinction that
// matters. Those were the QUERY; this is what the registry answered with, read out
// of the compiler's own declarations at the boundary. So the wording and the
// identifiers below are still the registration's and never the guest's, and a
// guest cannot put a sentence of its own where the condition's belongs.
//
// Error returns the NAMED condition's registered sentence, read off the
// description rather than written out here, so the wording still has exactly one
// home. It is the named condition's rather than the host's because the head a
// reader meets carries the named condition's code: a line whose code resolves to
// one sentence and whose text says another describes two conditions at once. The
// guest's own specific sentence follows it, appended by the wrap above, so the
// printed line is the KIND of failure and then what actually happened.
//
// That the sentence is said HERE is what obliges the guest not to say it too. A
// guest raises one of these conditions and the crossing message carries the
// occurrence's own clause alone — see the failure derivation on the guest side,
// which drops a registered condition's own frame for exactly this reason, the
// same way it already drops the authoring category the host composes into the
// head.
//
// Unwrap returns the host condition, so the whole host taxonomy still matches
// beneath. Is answers for the named condition by comparing the code the target
// reports against the code the registry answered with. That comparison is exact
// rather than approximate: the registry refuses a second registration under a code
// it already holds, so one code names one condition and code equality is identity
// equality. Neither side is a written-down literal — the target's code is read off
// the target and this one off the registry — so a condition re-registered under a
// different code moves both together, which is what separates this from comparing
// against a code spelled out in source.
//
// The two code reads report the named condition's own identifiers, unqualified. A
// machine consumer matching a code is matching the identifier the condition is
// registered under, and folding the plugin into it would hand that consumer a
// value that is not a code — and, for the short code, one that is not the letter E
// and four digits either. The plugin is a third read beside them, and the surface
// that composes a diagnostic for a reader is what joins the two.
type reportedCondition struct {
	host      error
	condition cerrors.Condition
	plugin    string
}

func (c *reportedCondition) Error() string { return c.condition.Message }

func (c *reportedCondition) Unwrap() error { return c.host }

func (c *reportedCondition) Is(target error) bool {
	code, ok := cerrors.CodeOf(target)
	return ok && code == c.condition.Code
}

func (c *reportedCondition) ErrorCode() string { return c.condition.Code }

func (c *reportedCondition) ErrorShortCode() string { return c.condition.ShortCode }

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
	RulePlace: cerrors.NotARule("A guest plugin returned a diagnostic whose severity the shared contract does not declare, so " +
		"the host has no route for it. It reports a guest whose returned output falls outside the " +
		"closed set that contract fixes."),
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
	RulePlace: cerrors.RuleEvaluatedElsewhere("This package evaluates the rule itself, at the plugin load boundary: it tests the " +
		"contract a catalog entity claims for each export against the descriptor that plugin " +
		"reported for itself. The half that decides the comparison is the output the guest returned " +
		"rather than anything a compilation carries, so there is no view for the rule dispatch to " +
		"invoke a rule over."),
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
	RulePlace: cerrors.RuleEvaluatedElsewhere("This package evaluates the rule itself, at the boundary where a published plugin is " +
		"acquired: it tests the host-function record an entity carries against the set it re-derives " +
		"from the import section of the content-verified binary. Unlike its neighbours here the " +
		"deciding half is read out of the artifact's own bytes rather than from anything the guest " +
		"reported about itself, and the rule runs over that published artifact rather than over a " +
		"compilation, so there is no view for the rule dispatch to invoke a rule over."),
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
	RulePlace: cerrors.RuleEvaluatedElsewhere("This package evaluates the rule itself, at the plugin load boundary: it tests the plugin " +
		"type the load site expects against the type that plugin reported for itself. The half that " +
		"decides the comparison is the output the guest returned rather than anything a compilation " +
		"carries, so there is no view for the rule dispatch to invoke a rule over."),
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
	RulePlace: cerrors.RuleEvaluatedElsewhere("This package evaluates the rule itself, at the plugin load boundary: it tests the command " +
		"mirror an entity records against the commands that plugin declared for itself. The half " +
		"that decides the comparison is the output the guest returned rather than anything a " +
		"compilation carries, so there is no view for the rule dispatch to invoke a rule over."),
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
	RulePlace: cerrors.NotARule("A producer reached through the host-mediated producer-consumption seam issued a further " +
		"producer-consumption call of its own. The seam bounds itself to one producer level, so it " +
		"reports a re-entrant call this host refuses by construction rather than a rule over anything " +
		"an author wrote."),
})

// ErrWasmPluginVersionMismatch indicates that a Plugin entity's recorded
// metadata.version disagrees with the build-provenance version the plugin's
// describe() self-report declares. The two name one release: the plugin's build
// stamps the version its release tag gives it, and the entity records that
// release — generated from a build of the tagged commit for a plugin that ships
// with the compiler, written by its author before tagging for one that ships
// outside it. Publish verifies the committed version against the binary before
// any bytes land; a divergence is a Loud Guardrail that halts and directs the
// author to a build of the commit the release's tag names, never to copying the
// version the plugin reports into the entity.
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
	RulePlace: cerrors.RuleEvaluatedElsewhere("This package evaluates the rule itself, at the plugin load boundary: it tests the version " +
		"an entity records against the build-provenance version that plugin reported for itself. The " +
		"half that decides the comparison is the output the guest returned rather than anything a " +
		"compilation carries, so there is no view for the rule dispatch to invoke a rule over."),
})

// ErrPluginBuiltFromUncommittedChanges indicates that a plugin being published
// reports, through describe(), a build-provenance version carrying the mark the
// version recipe appends when the working tree it built from held uncommitted
// changes (see BuiltFromUncommittedChanges). Such a version is a valid semantic
// version, but it names a commit that does not hold the bytes that were built,
// so nobody can rebuild what was published from the version it was published
// under. Publication refuses it before any bytes land and directs the author to a
// build of the commit the release's tag names; a clean development version and a
// prerelease both publish.
//
// It is raised only where the caller asks for it — ValidateOptions'
// RefuseUncommittedBuild, which publication sets. Validating a local build
// against its entity does not raise it, because a build under development is
// routinely made from an edited tree.
//
// It is checked ahead of the version equality (ErrWasmPluginVersionMismatch), so
// a guest built from an edited tree under an entity recording the clean version
// is refused as the build no commit holds rather than as a version disagreement.
func ErrPluginBuiltFromUncommittedChanges() error { return errPluginBuiltFromUncommittedChanges }

var errPluginBuiltFromUncommittedChanges = cerrors.Register("plugin-built-from-uncommitted-changes", cerrors.Meta{
	ShortCode: "E0314",
	Message:   "plugin was built from a working tree with uncommitted changes, so no commit reproduces the version it reports",
	RulePlace: cerrors.RuleEvaluatedElsewhere("This package evaluates the rule at publication, over the build-provenance version the " +
		"plugin reported for itself through describe(). The value the rule decides on is output the guest " +
		"returned rather than anything a compilation carries, so there is no view for the rule dispatch to " +
		"invoke a rule over."),
})
