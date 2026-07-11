// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package resolver

import (
	"sync"
	"time"

	"github.com/aggrega-ai/aggrega-cli/internal/wasm"
	"github.com/aggrega-ai/aggrega-cli/internal/wasm/provindex"
	"github.com/aggrega-ai/aggrega-cli/pkg/digest"
	"github.com/aggrega-ai/aggrega-cli/pkg/pluginpath"
)

// provenanceIndexPath resolves the provenance index location. It is a package
// variable so a test can redirect the recording to a scratch location.
var provenanceIndexPath = provindex.DefaultIndexPath

// provenanceUpsert records one digest→identity pair into the index. It is a
// package variable so a test can observe how many first-time records occur.
var provenanceUpsert = provindex.Upsert

// provenanceRecorded remembers which {URI, version} identities have already been
// recorded in this process, so the guest content-digest computation and the
// index write happen at most once per identity per process. It is keyed on the
// full identity — never on the digest — so two distinct URIs that resolve to
// byte-identical guests are each recorded, letting the index union both into one
// digest's identity set.
var provenanceRecorded = &sync.Map{}

// PluginSpec builds the loader input for a resolved plugin: the bytes to
// compile under the given diagnostic name, plus the entity's neutral capability
// claim (mapped onto the load gate's cross-check shape) and runtime limits
// (mapped onto the loader's caps). It is the single home of the
// neutral → wasm.PluginSpec mapping; every socket that loads a resolved plugin
// (the render pipeline's per-step load and the publishing-linter dispatch) calls
// it rather than re-spelling the field copy.
//
// A resolution that is not entity-fronted carries a nil Capability and nil
// Limits — the bare cascade, the linter's --plugin override, the publish path.
// A nil claim maps to a nil spec.Capability (the load gate skips the
// cross-check); nil limits map to zero caps (the loader applies the platform
// default cap, so a load is never unbounded). This preserves the non-entity load
// behaviour while the entity-fronted path gains both the cross-check and the
// entity's explicit caps.
//
// It is the sibling of PluginCapabilityClaim / PluginRuntimeLimits (the
// entity → neutral mapping): those translate an authored Plugin entity into the
// neutral shapes the resolution layer attaches to a resolved plugin; this
// translates those neutral shapes into the loader's spec. Keeping both mappings
// in this package leaves pkg/pluginpath free of any internal/wasm dependency and
// the loader free of any resolver dependency.
//
// Because this is the one site every socket funnels through to load a resolved
// plugin, it is also where the resolved artifact's identity is recorded: the
// mapping records the guest's content digest against its {URI, version} in the
// provenance index as a best-effort side effect (see recordProvenance). The
// record is re-derivable cache metadata; a write failure never affects the
// returned spec or the load.
func PluginSpec(name string, resolved pluginpath.ResolvedPlugin) wasm.PluginSpec {
	recordProvenance(resolved)
	spec := wasm.PluginSpec{Name: name, Bytes: resolved.Bytes}
	spec.Capability = WasmCapabilityClaim(resolved.Capability)
	if resolved.Limits != nil {
		spec.MaxMemoryPages = resolved.Limits.MaxMemoryPages
		spec.TimeoutMs = resolved.Limits.TimeoutMs
	}
	return spec
}

// WasmCapabilityClaim maps a resolved plugin's neutral capability claim onto the
// load gate's cross-check shape — the per-export name/operates/scope triples and
// the plugin-level capabilities block — or nil when the resolution carries none.
//
// It is the single home of the neutral-claim → wasm.EntityCapabilityClaim
// mapping. The load path reaches it through PluginSpec; the publish path calls it
// directly (fronting it with PluginCapabilityClaim to translate the authored
// entity). Both surfaces therefore cross-check against a claim built by exactly
// one mapping, so the set of authored fields carried into the describe()
// comparison cannot drift between load and publish.
func WasmCapabilityClaim(claim *pluginpath.PluginCapabilityClaim) *wasm.EntityCapabilityClaim {
	if claim == nil {
		return nil
	}
	exports := make([]wasm.EntityExportClaim, 0, len(claim.Exports))
	for _, e := range claim.Exports {
		exports = append(exports, wasm.EntityExportClaim{
			Name:     e.Name,
			Operates: e.Operates,
			Scope:    e.Scope,
		})
	}
	return &wasm.EntityCapabilityClaim{
		Exports:      exports,
		Capabilities: wasmCapabilities(claim.Capabilities),
	}
}

// recordProvenance records the resolved guest's content digest against its
// {URI, version} identity in the provenance index. It is best-effort: any
// failure — no resolvable cache root, a locked or unwritable index — is
// swallowed, because the index is re-derivable metadata and must never break a
// plugin load. A resolution carrying no URI or no bytes has no identity to
// record and is skipped.
//
// PluginSpec is called once per crossing on the render path for a non-reusable
// provider, so recordProvenance memoizes on the {URI, version} identity and does
// its work at most once per identity per process: the first caller for an
// identity computes the guest content digest and takes the index's file lock;
// every later caller for that same identity returns before either. Without the
// memo a large build would re-hash multi-megabyte guest bytes and re-serialize
// on the index lock on every crossing. The identity is marked on first sight,
// before the best-effort write runs, so a swallowed write failure does not
// un-mark it — a persistently unwritable index yields one attempt per process,
// never a per-crossing retry storm.
func recordProvenance(resolved pluginpath.ResolvedPlugin) {
	if resolved.URI == "" || len(resolved.Bytes) == 0 {
		return
	}
	id := provindex.Identity{URI: resolved.URI, Version: resolved.Version}
	if _, seen := provenanceRecorded.LoadOrStore(id, struct{}{}); seen {
		return
	}
	path, ok := provenanceIndexPath()
	if !ok {
		return
	}
	_ = provenanceUpsert(
		path,
		digest.ContentDigest(resolved.Bytes),
		id,
		int64(len(resolved.Bytes)),
		time.Now().UTC(),
	)
}

// wasmCapabilities maps the neutral plugin-level capability claim onto the
// load gate's cross-check shape, or nil when the resolution carries none.
func wasmCapabilities(c *pluginpath.PluginCapabilitiesClaim) *wasm.Capabilities {
	if c == nil {
		return nil
	}
	caps := &wasm.Capabilities{
		InputKinds: wasm.InputKinds{
			Sourced:             c.InputKinds.Sourced,
			Synthesized:         c.InputKinds.Synthesized,
			ArtifactsStandalone: c.InputKinds.ArtifactsStandalone,
		},
		BoundaryAssembly:     c.BoundaryAssembly,
		SourceFilter:         c.SourceFilter,
		ReusableInstance:     c.ReusableInstance,
		CrossBoundaryLinking: c.CrossBoundaryLinking,
	}
	if c.ArtifactDelimiters != nil {
		caps.ArtifactDelimiters = &wasm.ArtifactDelimiters{
			Left:  c.ArtifactDelimiters.Left,
			Right: c.ArtifactDelimiters.Right,
		}
	}
	if c.AssemblyNameAnnotation != nil {
		caps.AssemblyNameAnnotation = &wasm.AssemblyNameAnnotation{
			Annotation:    c.AssemblyNameAnnotation.Annotation,
			RequireUnique: c.AssemblyNameAnnotation.RequireUnique,
		}
	}
	return caps
}
