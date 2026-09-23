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
// A resolution that is not entity-fronted carries a nil Contract and nil
// Limits — the bare cascade, the linter's --plugin override, the publish path.
// A nil claim maps to a nil spec.Contract (the load gate skips the
// cross-check); nil limits map to zero caps (the loader applies the platform
// default cap, so a load is never unbounded). This preserves the non-entity load
// behaviour while the entity-fronted path gains both the cross-check and the
// entity's explicit caps.
//
// It is the sibling of PluginContractClaim / PluginRuntimeLimits (the
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
	spec.Contract = WasmContractClaim(resolved.Contract)
	if resolved.Limits != nil {
		spec.MaxMemoryPages = resolved.Limits.MaxMemoryPages
		spec.TimeoutMs = resolved.Limits.TimeoutMs
	}
	// The command mirror is carried only on the entity path (resolved.Commands is
	// non-nil), so CommandsMirrored is the explicit "entity supplied its mirror"
	// signal the load gate keys its full-equality command check on; a bare
	// resolution leaves it false and the check is skipped.
	if resolved.Commands != nil {
		spec.CommandsMirrored = true
		spec.Commands = make([]wasm.PluginCLICommand, len(*resolved.Commands))
		for i, c := range *resolved.Commands {
			spec.Commands[i] = wasm.PluginCLICommand{
				Name:        c.Name,
				Description: c.Description,
				Flags:       wasmCommandFlags(c.Flags),
			}
		}
	}
	return spec
}

// wasmCommandFlags maps a resolved command's neutral flag mirror onto the load
// gate's flag shape, preserving declared order. A command with no flags yields a
// nil slice.
func wasmCommandFlags(flags []pluginpath.PluginCommandFlagMirror) []wasm.PluginCommandFlag {
	if len(flags) == 0 {
		return nil
	}
	out := make([]wasm.PluginCommandFlag, len(flags))
	for i, f := range flags {
		out[i] = wasm.PluginCommandFlag{
			Name:        f.Name,
			Shorthand:   f.Shorthand,
			Description: f.Description,
			Type:        f.Type,
			Default:     f.Default,
		}
	}
	return out
}

// WasmContractClaim maps a resolved plugin's neutral capability claim onto the
// load gate's cross-check shape — the per-export name/operates/scope triples and
// the plugin-level capabilities block — or nil when the resolution carries none.
//
// It is the single home of the neutral-claim → wasm.EntityContractClaim
// mapping. The load path reaches it through PluginSpec; the publish path calls it
// directly (fronting it with PluginContractClaim to translate the authored
// entity). Both surfaces therefore cross-check against a claim built by exactly
// one mapping, so the set of authored fields carried into the describe()
// comparison cannot drift between load and publish.
func WasmContractClaim(claim *pluginpath.PluginContractClaim) *wasm.EntityContractClaim {
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
	return &wasm.EntityContractClaim{
		Exports:  exports,
		Contract: wasmContract(claim.Contract),
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

// wasmContract maps the neutral plugin-level capability claim onto the
// load gate's cross-check shape, or nil when the resolution carries none.
//
// The input-kind copy below SURVIVES consolidation for a structural reason: it
// crosses into the GENERATED describe-socket codec, which is single-sourced at that
// schema rather than at a Go type and cannot be merged with the host's own home for
// the claim ([types.PluginInputKinds], which every alias-able consumer shares).
//
// This copy is what feeds the load gate's `want` operand, so a kind missed here
// arrives as false and the gate compares two falses and passes. That is the exact
// silent drop the field-set guard in internal/wasm exists to make loud: it fails
// when the generated shape and the home type stop agreeing. Adding an input kind
// means editing this copy — never declaring another three-boolean struct.
func wasmContract(c *pluginpath.ProviderContractClaim) *wasm.ProviderContract {
	if c == nil {
		return nil
	}
	caps := &wasm.ProviderContract{
		InputKinds: wasm.InputKinds{
			Sourced:             c.InputKinds.Sourced,
			Synthesized:         c.InputKinds.Synthesized,
			ArtifactsStandalone: c.InputKinds.ArtifactsStandalone,
		},
		BoundaryAssembly:     c.BoundaryAssembly,
		ReusableInstance:     c.ReusableInstance,
		CrossBoundaryLinking: c.CrossBoundaryLinking,
		NestSegment:          c.NestSegment,
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
