// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package serve

import "github.com/aggrega-ai/aggrega-cli/internal/opsurface"

// Dispositions returns what the daemon does with every operation: the request
// method it is reachable at, or the reason no method carries it.
//
// It is exported so the omissions are readable from outside this package. A
// declared absence is only declared while someone other than the daemon can
// enumerate it.
//
// It returns a fresh map, so a caller enumerating the dispositions cannot alter
// what the completeness checks read.
func Dispositions() map[string]opsurface.Disposition {
	out := make(map[string]opsurface.Disposition, len(dispositions))
	for id, d := range dispositions {
		out[id] = d
	}
	return out
}

// The grounds on which no request method carries an operation.
//
// Each is a property of this daemon a reader can check against the package, not
// a judgement about the capability. They are named constants rather than
// repeated prose so that a dozen dispositions resting on one property cannot
// drift into a dozen descriptions of it.
const (
	// groundWritesOnlyBuildOutput covers an operation whose product is something
	// written that a build does not render: a dependency fill, a cache entry, a
	// stored credential, a configuration file. The daemon has exactly one writing
	// path — the artifact set an explicitly invoked build produces, plus its own
	// response bodies — so such an operation has no output here.
	//
	// The ground is stated as the one path rather than as "writes nothing"
	// because the narrower claim is the one that stays true: a reader checking it
	// against this package finds a single write and can see that none of the
	// products below is what it produces.
	groundWritesOnlyBuildOutput = "its product is something written that a build does not render, and the " +
		"daemon's only writing path is the artifact set an explicitly invoked build produces"

	// groundNoRegistryTransport covers an operation that talks to a registry.
	// The daemon links no registry, artifact-store or signing client, so such
	// an operation has no transport here.
	groundNoRegistryTransport = "it talks to a registry, and the daemon links no registry, artifact-store or " +
		"signing client"

	// groundNoTerminal covers an operation that asks a person for something.
	// The daemon's only stream is its request channel, so there is nothing to
	// prompt on.
	groundNoTerminal = "it asks a person for input, and the daemon's only stream is its request channel"

	// groundOutsideTheWorkspace covers an operation whose subject is not in
	// the workspace: a machine-wide cache, a configured registry's index, the
	// schema set compiled into the binary, a skeleton derived from one of those
	// schemas for a kind named in the request, a guest module named by path,
	// the binary's own identity. Every method the daemon answers is anchored to
	// a document in the workspace it opened or to the workspace root it
	// resolved at startup, so the session adds nothing such a request could
	// address.
	groundOutsideTheWorkspace = "its subject is not in the workspace, and every method the daemon answers is " +
		"anchored to a document in the workspace it opened or to the workspace root it resolved at startup"

	// groundIsAStreamServer covers an operation that is itself a server owning
	// a stream. The daemon already owns this one, and a second stream server
	// inside it would nest one protocol inside another.
	groundIsAStreamServer = "it is itself a server owning a stream, and the daemon already owns this one"

	// groundProtocolLocalMeta covers an operation that reports one protocol's
	// own capability surface. The daemon withholds no method, so it has no
	// withheld set to reveal.
	groundProtocolLocalMeta = "it reveals definitions one protocol withholds from its default listing; " +
		"the daemon withholds no method, so there is nothing to reveal"
)

// dispositions is what the daemon does with every operation.
//
// An exposed entry names the wire method, which is the identifier the routing
// table carries, so a renamed method fails the member check rather than
// silently ceasing to be the exposure it claims to be.
//
// The operation set is deliberately not restated here: an operation absent from
// this map is reported by the completeness check rather than defaulting to
// anything.
var dispositions = map[string]opsurface.Disposition{
	// Capabilities a request method carries.
	"session-initialize": opsurface.Exposed("aggrega/initialize"),
	"validate":           opsurface.Exposed("aggrega/validate"),
	"session-invalidate": opsurface.Exposed("aggrega/invalidate"),
	"cached-diagnostics": opsurface.Exposed("aggrega/diagnostics"),
	"ir-graph":           opsurface.Exposed("aggrega/irGraph"),

	// The compile runs here in full, under the posture of a surface that emits an
	// artifact: every gate on, every divergence a halt. Two methods carry it and
	// the difference between them is what happens to the result — one writes the
	// artifacts into the workspace, the other reports what writing them would have
	// produced. Both are named, because a caller enumerating this table is asking
	// what the daemon can be asked for, and a capability reachable two ways under
	// one name would be described as reachable one way.
	"build": opsurface.Exposed("aggrega/build", "aggrega/buildPreview"),

	"definition-at-position":      opsurface.Exposed("aggrega/resolve"),
	"references-at-position":      opsurface.Exposed("aggrega/findReferences"),
	"implementations-at-position": opsurface.Exposed("aggrega/findImplementations"),
	"hover-at-position":           opsurface.Exposed("aggrega/hover"),
	"completion-at-position":      opsurface.Exposed("aggrega/completion"),
	"workspace-entity-index":      opsurface.Exposed("aggrega/catalogIndex"),
	"feature-attach-candidates":   opsurface.Exposed("aggrega/attachCandidates"),
	"connection-candidates":       opsurface.Exposed("aggrega/connectionCandidates"),
	"release-candidates":          opsurface.Exposed("aggrega/releaseCandidates"),
	"active-release":              opsurface.Exposed("aggrega/activeRelease"),

	// The session reports the configuration it resolved at startup and the layer
	// each setting came from. It reports no other part of this capability: the
	// command line's single-layer verbatim dumps preserve a file's own comments
	// and formatting, which is a reading of the bytes on disk rather than of the
	// resolution, and a caller wanting those has the file.
	"config-show": opsurface.Exposed("aggrega/effectiveConfiguration"),

	// Capabilities no method carries, each on a property of this daemon.
	"acquire":               opsurface.NotApplicable(groundWritesOnlyBuildOutput),
	"cache-clear":           opsurface.NotApplicable(groundWritesOnlyBuildOutput),
	"cache-evict":           opsurface.NotApplicable(groundWritesOnlyBuildOutput),
	"cache-prune":           opsurface.NotApplicable(groundWritesOnlyBuildOutput),
	"config-capture":        opsurface.NotApplicable(groundWritesOnlyBuildOutput),
	"config-init":           opsurface.NotApplicable(groundWritesOnlyBuildOutput),
	"import":                opsurface.NotApplicable(groundWritesOnlyBuildOutput),
	"logout":                opsurface.NotApplicable(groundWritesOnlyBuildOutput),
	"plugin-add":            opsurface.NotApplicable(groundWritesOnlyBuildOutput),
	"plugin-gen-catalog":    opsurface.NotApplicable(groundWritesOnlyBuildOutput),
	"plugin-remove":         opsurface.NotApplicable(groundWritesOnlyBuildOutput),
	"plugin-sync":           opsurface.NotApplicable(groundWritesOnlyBuildOutput),
	"pull":                  opsurface.NotApplicable(groundWritesOnlyBuildOutput),
	"registry-add":          opsurface.NotApplicable(groundWritesOnlyBuildOutput),
	"registry-remove":       opsurface.NotApplicable(groundWritesOnlyBuildOutput),
	"registry-update":       opsurface.NotApplicable(groundWritesOnlyBuildOutput),
	"deprecate":             opsurface.NotApplicable(groundNoRegistryTransport),
	"push":                  opsurface.NotApplicable(groundNoRegistryTransport),
	"push-artifact":         opsurface.NotApplicable(groundNoRegistryTransport),
	"sign":                  opsurface.NotApplicable(groundNoRegistryTransport),
	"auth-token":            opsurface.NotApplicable(groundNoTerminal),
	"login":                 opsurface.NotApplicable(groundNoTerminal),
	"cache-status":          opsurface.NotApplicable(groundOutsideTheWorkspace),
	"get-schema":            opsurface.NotApplicable(groundOutsideTheWorkspace),
	"plugin-list":           opsurface.NotApplicable(groundOutsideTheWorkspace),
	"plugin-run-local":      opsurface.NotApplicable(groundOutsideTheWorkspace),
	"registry-list":         opsurface.NotApplicable(groundOutsideTheWorkspace),
	"scaffold-entity":       opsurface.NotApplicable(groundOutsideTheWorkspace),
	"search":                opsurface.NotApplicable(groundOutsideTheWorkspace),
	"version":               opsurface.NotApplicable(groundOutsideTheWorkspace),
	"mcp":                   opsurface.NotApplicable(groundIsAStreamServer),
	"serve":                 opsurface.NotApplicable(groundIsAStreamServer),
	"describe-capabilities": opsurface.NotApplicable(groundProtocolLocalMeta),
}
