// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package serve

import (
	"fmt"
	"sort"

	"github.com/aggrega-ai/aggrega-cli/internal/opsurface"
)

// methodBinding binds one wire method to the handler that answers it.
type methodBinding struct {
	method  string
	handler methodHandler
}

// at binds a wire method to its handler, for one row of the table below.
func at(method string, handler methodHandler) methodBinding {
	return methodBinding{method: method, handler: handler}
}

// serveAvailability is what the daemon does with one operation: the request
// methods that carry it, each with the handler answering it, or the reason no
// method carries it.
//
// It carries the handler because the table registers the daemon rather than
// describing it: the routing index below is built from these rows and from
// nothing else, so a wire method exists because a row names it. What the table
// does not carry is anything a handler needs in order to run — the handler is a
// value the row holds, never a shape the row constructs. A row that assembled a
// handler's inputs would have to speak about every handler at once, which means
// an untyped bag, which means each handler's own required inputs stop being
// required.
type serveAvailability struct {
	methods              []methodBinding
	unavailabilityReason string
}

// servedAt declares an operation reachable at the given methods. Several are
// admissible: one capability can be reachable under more than one method, and
// the two forms of a build are.
func servedAt(bindings ...methodBinding) serveAvailability {
	return serveAvailability{methods: bindings}
}

// notServed declares an operation no method carries, carrying the reason.
func notServed(reason string) serveAvailability {
	return serveAvailability{unavailabilityReason: reason}
}

// OperationAvailability returns what the daemon does with every operation: the
// request method it is reachable at, or the reason no method carries it.
//
// It is the surface-neutral projection of the table below — the same rows with
// their handlers dropped — so it cannot disagree with what the daemon routes.
//
// It is exported so the omissions are readable from outside this package. A
// declared absence is only declared while someone other than the daemon can
// enumerate it.
//
// It returns a fresh map, so a caller enumerating the availability cannot alter
// what the completeness checks read.
func OperationAvailability() map[string]opsurface.OperationAvailability {
	out := make(map[string]opsurface.OperationAvailability, len(operationAvailability))
	for id, a := range operationAvailability {
		if a.unavailabilityReason != "" {
			out[id] = opsurface.Unavailable(a.unavailabilityReason)
			continue
		}
		names := make([]string, 0, len(a.methods))
		for _, b := range a.methods {
			names = append(names, b.method)
		}
		out[id] = opsurface.Exposed(names...)
	}
	return out
}

// The reasons on which no request method carries an operation.
//
// Each is a property of this daemon a reader can check against the package, not
// a judgement about the capability. They are named constants rather than
// repeated prose so that a dozen rows resting on one property cannot drift into
// a dozen descriptions of it.
//
// A reason here decides behaviour rather than annotating it: a row carrying one
// contributes no route, so the operation is unreachable because of what the
// reason says. That is why none of them may restate how the daemon happens to be
// wired — "no method carries it, therefore no method should" would justify
// itself, and the routing index would then be built from its own shape.
const (
	// unavailabilityReasonNoRegistryTransport covers an operation that talks to a
	// registry. The daemon's own code links no registry, artifact-store or
	// signing client, so such an operation has no transport here.
	unavailabilityReasonNoRegistryTransport = "it talks to a registry, and the daemon links no registry, " +
		"artifact-store or signing client"

	// unavailabilityReasonNoTerminal covers an operation that asks a person for
	// something. The daemon's only stream is its request channel, so there is
	// nothing to prompt on.
	unavailabilityReasonNoTerminal = "it asks a person for input, and the daemon's only stream is its request channel"

	// unavailabilityReasonOutsideTheWorkspace covers an operation whose subject is
	// not in the workspace: a machine-wide cache, the machine-wide credential
	// store, the global configuration and the registries and plugins configured
	// in it, a configured registry's index, the schema set compiled into the
	// binary, a guest module named by path, the binary's own identity. A session
	// is opened over a workspace and holds nothing besides, so it brings such a
	// subject no closer than any other caller of this binary already has it.
	//
	// It deliberately claims nothing about where every method here is anchored.
	// The drafting method is anchored to a kind named in the request rather than
	// to the workspace, so a reason resting on the stronger claim would be false
	// against the table below — and a reason that is false about the surface it
	// describes decides nothing, whatever it happens to decide today.
	unavailabilityReasonOutsideTheWorkspace = "its subject is not in the workspace, and the session the daemon " +
		"holds over the workspace it opened brings such a subject no closer than any other caller of this " +
		"binary already has it"

	// unavailabilityReasonIsAStreamServer covers an operation that is itself a
	// server owning a stream. The daemon already owns this one, and a second
	// stream server inside it would nest one protocol inside another.
	unavailabilityReasonIsAStreamServer = "it is itself a server owning a stream, and the daemon already owns this one"

	// unavailabilityReasonProtocolLocalMeta covers an operation that reports one
	// protocol's own capability surface. The daemon withholds no method, so it
	// has no withheld set to reveal.
	unavailabilityReasonProtocolLocalMeta = "it reveals definitions one protocol withholds from its default " +
		"listing; the daemon withholds no method, so there is nothing to reveal"
)

// operationAvailability is what the daemon does with every operation, and it is
// the one place the daemon's wire surface is stated: adding a method means
// adding a binding to the row of the operation it carries.
//
// An exposed row names the wire method and supplies the handler, so the method
// name exists once. A row carrying a reason supplies no handler and produces no
// route: the reason is what makes the operation unreachable.
//
// The operation set is deliberately not restated here: an operation absent from
// this map is reported by the completeness check rather than defaulting to
// anything, and an identifier here that is not an operation is refused when the
// routing index is built.
var operationAvailability = map[string]serveAvailability{
	// Capabilities a request method carries.
	"session-initialize": servedAt(at("aggrega/initialize", (*Server).dispatchInitialize)),
	"validate":           servedAt(at("aggrega/validate", (*Server).dispatchValidate)),
	"session-invalidate": servedAt(at("aggrega/invalidate", (*Server).dispatchInvalidate)),
	"cached-diagnostics": servedAt(at("aggrega/diagnostics", (*Server).dispatchDiagnostics)),
	"ir-graph":           servedAt(at("aggrega/irGraph", (*Server).dispatchIrGraph)),

	// The compile runs here in full, under the posture of a surface that emits an
	// artifact: every gate on, every divergence a halt. Two methods carry it and
	// the difference between them is what happens to the result — one writes the
	// artifacts into the workspace, the other reports what writing them would have
	// produced. Both are named, because a caller enumerating this table is asking
	// what the daemon can be asked for, and a capability reachable two ways under
	// one name would be described as reachable one way.
	"build": servedAt(
		at("aggrega/build", (*Server).dispatchBuild),
		at("aggrega/buildPreview", (*Server).dispatchBuildPreview),
	),

	"definition-at-position":      servedAt(at("aggrega/resolve", (*Server).dispatchResolve)),
	"references-at-position":      servedAt(at("aggrega/findReferences", (*Server).dispatchFindReferences)),
	"implementations-at-position": servedAt(at("aggrega/findImplementations", (*Server).dispatchFindImplementations)),
	"hover-at-position":           servedAt(at("aggrega/hover", (*Server).dispatchHover)),
	"completion-at-position":      servedAt(at("aggrega/completion", (*Server).dispatchCompletion)),
	"workspace-entity-index":      servedAt(at("aggrega/catalogIndex", (*Server).dispatchCatalogIndex)),
	"feature-attach-candidates":   servedAt(at("aggrega/attachCandidates", (*Server).dispatchAttachCandidates)),
	"connection-candidates":       servedAt(at("aggrega/connectionCandidates", (*Server).dispatchConnectionCandidates)),
	"release-candidates":          servedAt(at("aggrega/releaseCandidates", (*Server).dispatchReleaseCandidates)),
	"active-release":              servedAt(at("aggrega/activeRelease", (*Server).dispatchActiveRelease)),

	// The session reports the configuration it resolved at startup and the layer
	// each setting came from. It reports no other part of this capability: the
	// command line's single-layer verbatim dumps preserve a file's own comments
	// and formatting, which is a reading of the bytes on disk rather than of the
	// resolution, and a caller wanting those has the file.
	"config-show": servedAt(at("aggrega/effectiveConfiguration", (*Server).dispatchEffectiveConfiguration)),

	// The session drafts a starter document for an entity kind the request
	// names. Its subject is not the workspace: the skeleton is derived from the
	// schema set compiled into the binary, so the session contributes nothing to
	// producing it, and the rows below decline several capabilities for exactly
	// that property.
	//
	// It is carried anyway, and the reason is about who is on the other end
	// rather than about where the subject lives. This is the surface a person
	// drives, starting a new document is something a person does, and the answer
	// lands in a buffer they can read before it is anything else. Nothing is
	// written: the answer is text, and where it goes is the client's.
	"scaffold-entity": servedAt(at("aggrega/scaffoldEntity", (*Server).dispatchScaffoldEntity)),

	// Capabilities no method carries, each on a property of this daemon.
	//
	// The set is smaller than the capabilities the daemon lacks a method for,
	// and deliberately so. A row here asserts that a property of the daemon
	// declines the capability; a capability nobody has ruled on is named in the
	// bound the availability check reads instead, where it reads as the question
	// it is rather than as a decision.
	"cache-status":          notServed(unavailabilityReasonOutsideTheWorkspace),
	"get-schema":            notServed(unavailabilityReasonOutsideTheWorkspace),
	"plugin-list":           notServed(unavailabilityReasonOutsideTheWorkspace),
	"plugin-run-local":      notServed(unavailabilityReasonOutsideTheWorkspace),
	"registry-list":         notServed(unavailabilityReasonOutsideTheWorkspace),
	"search":                notServed(unavailabilityReasonOutsideTheWorkspace),
	"version":               notServed(unavailabilityReasonOutsideTheWorkspace),
	"deprecate":             notServed(unavailabilityReasonNoRegistryTransport),
	"push":                  notServed(unavailabilityReasonNoRegistryTransport),
	"push-artifact":         notServed(unavailabilityReasonNoRegistryTransport),
	"sign":                  notServed(unavailabilityReasonNoRegistryTransport),
	"auth-token":            notServed(unavailabilityReasonNoTerminal),
	"login":                 notServed(unavailabilityReasonNoTerminal),
	"mcp":                   notServed(unavailabilityReasonIsAStreamServer),
	"serve":                 notServed(unavailabilityReasonIsAStreamServer),
	"describe-capabilities": notServed(unavailabilityReasonProtocolLocalMeta),
}

// methodIndex resolves a method name to its handler in one lookup, and it is the
// whole of the daemon's routing. It is built from the availability table and
// from nothing else, so a method exists exactly because a row names it.
var methodIndex = buildMethodIndex(operationAvailability)

// buildMethodIndex projects the availability table into the routing index,
// refusing at startup rather than routing a table that cannot be right.
//
// Three refusals, and each covers a way a row could otherwise reach the wire
// unexamined: a row keyed on something that is not an operation would register a
// method no capability claims; an exposed row with an empty method or a nil
// handler would register a route that cannot answer; and two rows naming one
// method would have one silently win. A row carrying an unavailability reason
// contributes nothing, which is the whole of what makes the reason operative.
//
// It panics because the table is a compile-time literal: every refusal here is a
// programming error a reader can fix by editing the row, and a daemon that
// started anyway would serve a surface nobody declared.
func buildMethodIndex(table map[string]serveAvailability) map[string]methodHandler {
	idx := map[string]methodHandler{}
	claimed := map[string]string{}

	for _, id := range sortedOperationIDs(table) {
		if _, known := opsurface.Lookup(id); !known {
			panic(fmt.Sprintf("serve: the availability table declares %q, which is not an operation: "+
				"either the operation was renamed or removed and this row is stale, or the identifier "+
				"is misspelled", id))
		}
		a := table[id]
		if a.unavailabilityReason != "" {
			continue
		}
		if len(a.methods) == 0 {
			panic(fmt.Sprintf("serve: the availability table exposes %q under no method and gives no "+
				"unavailability reason: use servedAt(at(method, handler)…) or notServed(reason)", id))
		}
		for _, b := range a.methods {
			switch {
			case b.method == "":
				panic(fmt.Sprintf("serve: the availability table exposes %q under an empty method name", id))
			case b.handler == nil:
				panic(fmt.Sprintf("serve: the availability table exposes %q at %q with no handler", id, b.method))
			}
			if other, dup := claimed[b.method]; dup {
				panic(fmt.Sprintf("serve: the availability table binds %q to both the %q and %q operations; "+
					"one wire method answers one capability", b.method, other, id))
			}
			claimed[b.method] = id
			idx[b.method] = b.handler
		}
	}
	return idx
}

// sortedOperationIDs returns the table's keys in operation-set order, with any
// key that is not an operation last and in lexical order.
//
// The order matters only so that a refusal above and the method census below
// report the same sequence across runs; nothing routes on it. An unknown key
// sorts last rather than being dropped, because dropping it is how a stale row
// would reach the refusal as an absence instead of as itself.
func sortedOperationIDs(table map[string]serveAvailability) []string {
	out := make([]string, 0, len(table))
	for _, op := range opsurface.Operations() {
		if _, ok := table[op.ID]; ok {
			out = append(out, op.ID)
		}
	}
	unknown := make([]string, 0, len(table))
	for id := range table {
		if _, ok := opsurface.Lookup(id); !ok {
			unknown = append(unknown, id)
		}
	}
	sort.Strings(unknown)
	return append(out, unknown...)
}

// Methods returns every request method the daemon routes, in operation-set order
// and, within one operation, in the order its row binds them.
//
// It is exported so what the daemon carries is readable from outside this
// package, which is what lets the operation set be checked against the wire
// surface rather than against someone's recollection of it. It reads the same
// table the routing index is built from, so the census and the routing cannot
// disagree.
func Methods() []string {
	out := make([]string, 0, len(methodIndex))
	for _, id := range sortedOperationIDs(operationAvailability) {
		for _, b := range operationAvailability[id].methods {
			out = append(out, b.method)
		}
	}
	return out
}
