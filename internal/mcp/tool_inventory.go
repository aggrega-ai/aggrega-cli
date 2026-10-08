// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/aggrega-ai/aggrega-cli/internal/cacheops"
	"github.com/aggrega-ai/aggrega-cli/internal/registryindex"
	"github.com/aggrega-ai/aggrega-cli/internal/registryops"
	"github.com/aggrega-ai/aggrega-cli/internal/wasm/provindex"
	"github.com/aggrega-ai/aggrega-cli/pkg/readscope"
	"github.com/aggrega-ai/aggrega-cli/pkg/sourceresolver/gitrepo"
)

// The two inventories below answer the question a caller asks first in an
// unfamiliar workspace — what do I already have, and where would I get the rest —
// and they answer it as data.
//
// Each operation is also reachable as the tool generated from its command, which
// returns the command's rendered output as text. That is the right answer for a
// person reading it and the wrong one for a program: a table has to be parsed
// back into the values it was rendered from, and every renderer change breaks the
// parser. These carry the values themselves, and render the text from them.

// cacheStatusArgs is the cache-status tool's argument shape. It names no subject:
// what the caches hold is a property of the machine and of the workspace, not of
// anything a caller would have to name first.
type cacheStatusArgs struct {
	WorkspaceRoot string `json:"workspace_root"`
}

// cacheStatusElement is one enumerated cache element.
//
// SizeText is the byte count as the shared formatter writes it, carried beside
// the number rather than instead of it. An assistant that totals or compares
// sizes needs the number; one that shows a size needs the text, and composing
// that text itself would mean reproducing the rounding rule the formatter owns.
// The vocabulary it is written in is the one the size-taking operations parse,
// so a quantity read out of this answer can be sent back unrewritten.
//
// NoteWithheld marks a note kept back from a caller that is not told where things
// sit on this host: a compiled module's note names the plugin identities it was
// built for, and one of them was loaded from a file on this host. The note is one
// composed sentence, so it is kept back whole rather than in part. The element's
// digest, the content digest of the guest the module was compiled for, names no
// place and is answered either way.
//
// LabelWithheld marks a label kept back from such a caller: a source-artifact
// slot's label is its location under the cache, composed from the host and path
// of the URL its source was resolved from, so the label of a slot whose repository
// was read from a path on this host spells that path. The label is absent rather
// than empty, so the mark is what a caller reads, and the element's size and
// digest, which name no place, are answered either way.
type cacheStatusElement struct {
	Label         *string `json:"label,omitempty"`
	LabelWithheld bool    `json:"label_withheld,omitempty"`
	Size          int64   `json:"size"`
	SizeText      string  `json:"size_text"`
	Digest        string  `json:"digest,omitempty"`
	Note          string  `json:"note,omitempty"`
	NoteWithheld  bool    `json:"note_withheld,omitempty"`
}

// cacheStatusProvenance is one recorded content digest's resolved plugin
// identities — every URI@version the resolver loaded those exact guest bytes
// for. SharedBy is present only where more than one identity resolves to the
// same bytes.
//
// It is carried apart from the classes and excluded from the totals, on the
// same ground the shared inventory keeps it apart: an identity is metadata
// about a compiled module rather than a reclaimable element, so counting it
// would report a size nothing can reclaim. It is what gives a compiled module a
// name an assistant can reason about instead of the runtime's opaque leaf key.
//
// An identity whose reference is a path on this host's filesystem is kept back
// from a caller that is not told where things sit on this host: it is null in its
// place, so every other identity keeps its position, and IdentitiesWithheld marks
// the entry. An identity naming a registry reference or a Plugin entity is
// answered whole.
type cacheStatusProvenance struct {
	Identities         []*string `json:"identities"`
	IdentitiesWithheld bool      `json:"identities_withheld,omitempty"`
	SharedBy           int       `json:"shared_by,omitempty"`
}

// cacheStatusClass is one cache class, carrying how far its contents are shared.
//
// Reach is the field that stops a caller reading this answer as being about the
// workspace it named. Two of the classes are shared with every other project on
// the machine, and an assistant deciding whether to reclaim space has to know
// which before it acts.
type cacheStatusClass struct {
	Name     string               `json:"name"`
	Reach    string               `json:"reach"`
	Prunable bool                 `json:"prunable"`
	Elements []cacheStatusElement `json:"elements"`
}

// cacheStatusResult is the cache-status tool's structured payload.
//
// Every collection is an array rather than a null, so a caller iterates
// unconditionally instead of special-casing an empty cache. TotalSizeText
// carries the total in the shared spelling, on the same terms as every other
// rendered size here.
//
// WorkspaceRoot is empty where no workspace resolved, and absent — marked by
// WorkspaceRootWithheld — where one did and the caller is not told where it sits
// on this host.
type cacheStatusResult struct {
	WorkspaceRoot         *string `json:"workspace_root"`
	WorkspaceRootWithheld bool    `json:"workspace_root_withheld,omitempty"`

	Classes            []cacheStatusClass      `json:"classes"`
	Provenance         []cacheStatusProvenance `json:"provenance"`
	TotalCount         int                     `json:"total_count"`
	TotalSize          int64                   `json:"total_size"`
	TotalSizeText      string                  `json:"total_size_text"`
	MachineWideClasses []string                `json:"machine_wide_classes"`
}

var cacheStatusToolSpec = toolSpec{
	summary: "Reports what the on-disk caches hold.",
	description: "Report what the on-disk caches hold, reading them and writing nothing. Returns one entry per cached " +
		"element with its size and, where it has one, its content digest — so you can total, compare or select without " +
		"parsing a table. Every size is reported twice: `size` as the byte count to calculate with, and `size_text` as " +
		"the same quantity written in binary units to display, which is also the vocabulary the size-taking operations " +
		"accept, so a quantity read here can be sent back unrewritten. Each class carries a `reach`: `workspace` for " +
		"the artifact cache under this workspace's own cache directory, `machine` for the compiled-module and " +
		"plugin-describe stores, which every project on this machine shares. Read `reach` before proposing any " +
		"reclamation: removing a machine-wide element affects workspaces nobody in this conversation has named. " +
		"`machine_wide_classes` names those classes directly. `provenance` names the plugin identities each recorded " +
		"guest digest resolved for, marking a digest more than one identity shares — it is what gives a compiled " +
		"module a name rather than an opaque key, and it is metadata rather than reclaimable content, so it is " +
		"excluded from the totals. Without a resolvable workspace only the machine-wide classes are reported. Where " +
		"the workspace's own configuration file, or the CI-owned overlay beside it, is a link leading out of the " +
		"workspace, the call is refused naming the link: where that workspace's cache sits is what its " +
		"configuration decides.",
	workspaceSelectionNote: "Optional `workspace_root` overrides discovery.",
	listenerNote: "You did not start this server, so you are not told where a plugin it loaded from a file sits on " +
		"its host: that identity is null in its place in `provenance`, and its entry is marked " +
		"`identities_withheld`. A compiled module's `note` naming one is absent and marked `note_withheld`, and its " +
		"`digest` is still answered. An identity naming a registry reference or a Plugin entity is answered whole. " +
		"A source artifact read from a repository on the server's host is listed without its `label`, marked " +
		"`label_withheld`, with its `size` and `digest` still answered.",
	inputSchema: `{
  "type": "object",
  "properties": {
    "workspace_root": {
      "type": "string",
      "description": "Optional workspace root override. Defaults to discovery from the server's working directory."
    }
  }
}`,
	invoke: cacheStatusInvoke,
}

func cacheStatusInvoke(s *server, ctx context.Context, args json.RawMessage) (*callToolResult, *rpcError) {
	var a cacheStatusArgs
	if len(args) > 0 {
		if err := json.Unmarshal(args, &a); err != nil {
			return invalidToolArguments("cache-status", err)
		}
	}

	// A workspace that does not resolve is not a failure here. The machine-wide
	// caches exist and are reportable whatever the caller is standing in, and
	// refusing the whole answer because one half of it is unavailable would be
	// less useful than saying which half was read.
	//
	// A workspace whose configuration is held is refused, as every tool but the two
	// listings refuses it. Where its cache sits is what that configuration decides,
	// so an inventory without its classes would read as the inventory of a
	// workspace that keeps none, and the link the hold names would go unsaid.
	var root, cacheDir string
	tw, err := s.resolveToolWorkspace(a.WorkspaceRoot, nil)
	switch {
	case err == nil:
		root = tw.root
		cacheDir = tw.cacheDir
	case errors.Is(err, readscope.ErrReadLeadsOutOfWorkspace()):
		return errorResult(explainToolError("Could not resolve the workspace to report its caches.", err, ""))
	default:
		tw = s.servedWorkspace()
	}

	// The inventory is bound to a name before it is projected rather than passed
	// inline. A result handed straight into a call is a value nothing here holds,
	// so what this surface takes from it stops being readable at this site — and
	// a projection nobody can read is indistinguishable from one that drops
	// everything.
	locate := s.cacheLocations
	if locate == nil {
		locate = cacheops.ResolveLocations
	}
	report, err := cacheops.Status(ctx, locate(root, cacheDir))
	if err != nil {
		return tw.answer(errorResult(err.Error()))
	}
	payload := cacheStatusPayload(root, report, tw.withholdsHostLocations())
	// The root is told the way every location here is. An empty one names no
	// workspace, which is not a place to keep back.
	payload.WorkspaceRoot, payload.WorkspaceRootWithheld = tw.reportedLocationField(root)
	return tw.answer(textResult(renderCacheStatusText(payload), payload))
}

// cacheStatusPayload projects the shared inventory onto this surface's shape.
//
// It is a function of the report rather than of the machine so the projection
// can be driven over a report supplied to it. The machine-wide classes are
// shared by every process on the machine, so anything that had to enumerate
// them a second time in order to check this projection could not be reading a
// fixed answer; handed the report, the projection is checkable exactly.
//
// Every collection is materialised as an empty slice rather than left nil, so a
// caller iterates unconditionally instead of special-casing an empty cache.
//
// withholdHostLocations keeps back what names a plugin loaded from a file on
// this host, judged from the identities the inventory carries beside its
// composed text rather than from that text, and the label of a source artifact
// read from a repository on this host, judged from the source URL its slot
// records rather than from the label composed out of it.
func cacheStatusPayload(root string, report cacheops.Report, withholdHostLocations bool) cacheStatusResult {
	classes := make([]cacheStatusClass, 0, len(report.Classes))
	for _, cl := range report.Classes {
		elements := make([]cacheStatusElement, 0, len(cl.Elements))
		for _, el := range cl.Elements {
			element := cacheStatusElement{
				Label:    stringPointer(el.Label),
				Size:     el.Size,
				SizeText: cacheops.HumanSize(el.Size),
				Digest:   el.DigestHex,
				Note:     el.Note,
			}
			if withholdHostLocations && slotNamesAHostLocation(el.SourceURL) {
				element.Label, element.LabelWithheld = nil, true
			}
			if withholdHostLocations && anyIdentityNamesAHostLocation(el.Recorded) {
				element.Note, element.NoteWithheld = "", true
			}
			elements = append(elements, element)
		}
		classes = append(classes, cacheStatusClass{
			Name:     cl.Name,
			Reach:    string(cl.Reach),
			Prunable: cl.Prunable,
			Elements: elements,
		})
	}
	provenance := make([]cacheStatusProvenance, 0, len(report.Provenance))
	for _, p := range report.Provenance {
		provenance = append(provenance, cacheStatusProvenanceEntry(p, withholdHostLocations))
	}
	machineWide := cacheops.MachineWideClasses(report.Classes)
	if machineWide == nil {
		machineWide = []string{}
	}

	return cacheStatusResult{
		WorkspaceRoot:      &root,
		Classes:            classes,
		Provenance:         provenance,
		TotalCount:         report.TotalCount,
		TotalSize:          report.TotalSize,
		TotalSizeText:      cacheops.HumanSize(report.TotalSize),
		MachineWideClasses: machineWide,
	}
}

// cacheStatusProvenanceEntry projects one provenance entry, keeping back each
// identity whose reference is a path on this host where withholdHostLocations
// says to.
//
// Each identity is judged from the record it was composed from, paired by
// position. An identity whose record did not arrive beside it cannot be judged,
// and it is kept back rather than guessed at: the direction a missing record errs
// in is the one that tells the caller less.
func cacheStatusProvenanceEntry(p cacheops.ProvenanceEntry, withholdHostLocations bool) cacheStatusProvenance {
	entry := cacheStatusProvenance{SharedBy: p.SharedBy}
	if p.Identities == nil {
		return entry
	}
	entry.Identities = make([]*string, 0, len(p.Identities))
	for i, name := range p.Identities {
		if withholdHostLocations && (i >= len(p.Recorded) || referenceNamesAHostLocation(p.Recorded[i].URI)) {
			entry.Identities = append(entry.Identities, nil)
			entry.IdentitiesWithheld = true
			continue
		}
		entry.Identities = append(entry.Identities, stringPointer(name))
	}
	return entry
}

// anyIdentityNamesAHostLocation reports whether any of the recorded identities
// names a plugin by a path on this host, asked of each reference by the plugin
// reading's own test of one.
func anyIdentityNamesAHostLocation(recorded []provindex.Identity) bool {
	for _, id := range recorded {
		if referenceNamesAHostLocation(id.URI) {
			return true
		}
	}
	return false
}

// slotNamesAHostLocation reports whether a cache element's label names a place on
// this host, judged from the source URL the element's slot records rather than
// from the label composed out of it.
//
// A source-artifact slot's label is its location under the cache, built from the
// host and path of the URL its source was resolved from, so a slot whose source
// was read from a path on this host spells that path. Only a repository source
// can be read from one — every other source kind is fetched over HTTP(S) or from
// a registry — and whether a repository was is the repository adapter's own
// reading of the URL, the one its clone selects a transport by. That reading
// answers a network transport for every other kind's URL too, so it is asked of
// every slot rather than of repository slots alone.
//
// An element of any other class records no source URL, and its label names no
// place. A slot whose metadata could not be read recorded none to judge, and its
// label is kept back: the direction a missing record errs in is the one that
// tells the caller less.
func slotNamesAHostLocation(sourceURL *string) bool {
	if sourceURL == nil {
		return false
	}
	return *sourceURL == "" || gitrepo.ReadsFromThisHost(*sourceURL)
}

// renderCacheStatusText renders the readable listing: one heading per class,
// marked where the class is machine-wide, then its elements, then the recorded
// plugin identities and the totals.
//
// Every size it prints is the payload's own rendered field rather than a fresh
// call to the formatter, so the text and the structured half of this answer
// cannot spell one quantity two ways. What the payload keeps back, the text
// keeps back too: it is rendered from the payload's values, and a note or an
// identity kept back reads as the kept-back phrase in its place.
func renderCacheStatusText(p cacheStatusResult) string {
	var b strings.Builder
	if p.WorkspaceRoot == nil || *p.WorkspaceRoot != "" {
		fmt.Fprintf(&b, "Caches for %s:\n", workspaceRootWords(p.WorkspaceRoot))
	} else {
		b.WriteString("Caches (no workspace resolved; machine-wide classes only):\n")
	}
	for _, cl := range p.Classes {
		if cl.Reach == string(cacheops.ReachMachine) {
			fmt.Fprintf(&b, "  %s [shared with every project on this machine]:\n", cl.Name)
		} else {
			fmt.Fprintf(&b, "  %s:\n", cl.Name)
		}
		if len(cl.Elements) == 0 {
			b.WriteString("    (empty)\n")
			continue
		}
		for _, el := range cl.Elements {
			fmt.Fprintf(&b, "    %s  %s", reportedLocationWords(el.Label), el.SizeText)
			switch {
			case el.NoteWithheld:
				fmt.Fprintf(&b, "  %s", keptBackLocationWords)
			case el.Note != "":
				fmt.Fprintf(&b, "  %s", el.Note)
			}
			b.WriteByte('\n')
		}
	}
	b.WriteString("  plugin provenance:\n")
	if len(p.Provenance) == 0 {
		b.WriteString("    (empty)\n")
	}
	for _, entry := range p.Provenance {
		names := make([]string, 0, len(entry.Identities))
		for _, identity := range entry.Identities {
			names = append(names, reportedLocationWords(identity))
		}
		b.WriteString("    " + strings.Join(names, ", "))
		if entry.SharedBy > 0 {
			fmt.Fprintf(&b, "  shared by %d", entry.SharedBy)
		}
		b.WriteByte('\n')
	}
	fmt.Fprintf(&b, "\n%d elements, %s\n", p.TotalCount, p.TotalSizeText)
	if len(p.MachineWideClasses) > 0 {
		fmt.Fprintf(&b, "Machine-wide: %s. Reclaiming from these reaches workspaces this session has not named.\n",
			strings.Join(p.MachineWideClasses, ", "))
	}
	return strings.TrimRight(b.String(), "\n")
}

// registryListArgs is the registry-list tool's argument shape. It names no
// subject: which registries are reachable is a property of the workspace's
// configuration.
type registryListArgs struct {
	WorkspaceRoot string `json:"workspace_root"`
}

// registryListResult is the registry-list tool's structured payload.
//
// CredentialStateIsMachineWide is stated because every other field is a property
// of the workspace's own configuration and the credential state is not: it is
// read from the machine-wide credential store, so the same host reads the same
// way from every workspace on this machine.
//
// WorkspaceRoot is absent, and marked by WorkspaceRootWithheld, where the caller is
// not told where the workspace sits on this host.
type registryListResult struct {
	WorkspaceRoot         *string `json:"workspace_root"`
	WorkspaceRootWithheld bool    `json:"workspace_root_withheld,omitempty"`

	Registries                   []registryops.Registry `json:"registries"`
	CredentialStateIsMachineWide bool                   `json:"credential_state_is_machine_wide"`
}

var registryListToolSpec = toolSpec{
	summary: "Reports the registries this workspace reaches and how each one is trusted.",
	description: "Report the registries this workspace is configured to reach and the signature trust posture of each, " +
		"reading configuration, writing nothing and contacting no registry. Returns each entry's host, optional alias, whether it is the " +
		"default, its trust posture as discrete fields, and whether a credential for that host is present. The " +
		"credential state reports presence only — never the credential, never its shape, and never whether the registry " +
		"would accept it, which would take a network round trip. It is read from the machine-wide credential store, so " +
		"it is the same answer in every workspace on this machine. Use it to find the host or alias other operations " +
		"accept.",
	workspaceSelectionNote: "Optional `workspace_root` overrides discovery.",
	inputSchema: `{
  "type": "object",
  "properties": {
    "workspace_root": {
      "type": "string",
      "description": "Optional workspace root override. Defaults to discovery from the server's working directory."
    }
  }
}`,
	invoke: registryListInvoke,
}

func registryListInvoke(s *server, ctx context.Context, args json.RawMessage) (*callToolResult, *rpcError) {
	var a registryListArgs
	if len(args) > 0 {
		if err := json.Unmarshal(args, &a); err != nil {
			return invalidToolArguments("registry-list", err)
		}
	}
	tw, err := s.resolveToolWorkspace(a.WorkspaceRoot, nil)
	if err != nil {
		return errorResult(explainToolError("Could not resolve the workspace to report its configured registries.", err, "component"))
	}

	entries := registryops.List(tw.cfg, registryops.AuthResolver(ctx, nil))
	if entries == nil {
		entries = []registryops.Registry{}
	}
	payload := registryListResult{
		Registries:                   entries,
		CredentialStateIsMachineWide: true,
	}
	payload.WorkspaceRoot, payload.WorkspaceRootWithheld = tw.reportedLocationField(tw.root)
	return tw.answer(textResult(renderRegistryListText(payload), payload))
}

// renderRegistryListText renders one line per registry: the host, its alias, the
// trust posture in words, and its credential state.
func renderRegistryListText(p registryListResult) string {
	var b strings.Builder
	if len(p.Registries) == 0 {
		return "No registries are configured for " + workspaceRootWords(p.WorkspaceRoot)
	}
	fmt.Fprintf(&b, "Registries for %s (%d):\n", workspaceRootWords(p.WorkspaceRoot), len(p.Registries))
	for _, r := range p.Registries {
		fmt.Fprintf(&b, "  %s", r.Host)
		if r.Name != "" {
			fmt.Fprintf(&b, " (%s)", r.Name)
		}
		if r.Default {
			b.WriteString(" [default]")
		}
		fmt.Fprintf(&b, " — %s; credential %s\n", registryTrustWords(r.Trust), r.AuthState)
	}
	b.WriteString("\nCredential presence is read from the machine-wide store, so it is the same in every workspace here.")
	return strings.TrimRight(b.String(), "\n")
}

// registryTrustWords renders a registry's trust posture as a short phrase, from
// the discrete fields the shared layer resolved.
func registryTrustWords(t registryops.Trust) string {
	var parts []string
	switch {
	case t.VerificationSkipped:
		parts = append(parts, "verification skipped")
	case t.SignaturesRequired:
		parts = append(parts, "signatures required")
	default:
		parts = append(parts, "signatures optional")
	}
	switch {
	case t.Anchor == "keyed":
		parts = append(parts, "keyed")
	case t.Anchor == "keyless" && t.KeylessIdentities == 1:
		parts = append(parts, "keyless (1 identity)")
	case t.Anchor == "keyless" && t.KeylessIdentities > 1:
		parts = append(parts, fmt.Sprintf("keyless (%d identities)", t.KeylessIdentities))
	}
	if t.Advisory {
		parts = append(parts, "advisory")
	}
	return strings.Join(parts, ", ")
}

// searchArgs is the search tool's argument shape. Every field narrows and every
// one is optional, so an empty call reports everything the local index holds.
type searchArgs struct {
	Query            string   `json:"query"`
	Kinds            []string `json:"kinds"`
	Selector         string   `json:"selector"`
	Capabilities     []string `json:"capabilities"`
	PluginType       string   `json:"plugin_type"`
	Registry         string   `json:"registry"`
	Limit            int      `json:"limit"`
	IncludeRetracted bool     `json:"include_retracted"`
	WorkspaceRoot    string   `json:"workspace_root"`
}

// searchRecordResult is one matched entity: the index record, plus the versions
// screened out of selection.
type searchRecordResult struct {
	registryindex.Record
	Retracted []string `json:"retracted,omitempty"`
}

// searchIndexResult is one searched host's freshness.
type searchIndexResult struct {
	Host        string `json:"host"`
	LastUpdated string `json:"last_updated,omitempty"`
}

// searchResult is the search tool's structured payload.
//
// The provenance travels with the answer. A search reads an offline snapshot, so
// a caller acting on the result needs to know which registries were consulted,
// how old each snapshot is, and which configured registries had no index to
// consult — otherwise a partial answer reads as a complete one.
//
// WorkspaceRoot is absent, and marked by WorkspaceRootWithheld, where the caller is
// not told where the workspace sits on this host.
type searchResult struct {
	WorkspaceRoot         *string `json:"workspace_root"`
	WorkspaceRootWithheld bool    `json:"workspace_root_withheld,omitempty"`

	Records              []searchRecordResult `json:"records"`
	Indexes              []searchIndexResult  `json:"indexes"`
	MissingIndexHosts    []string             `json:"missing_index_hosts"`
	Stale                bool                 `json:"stale"`
	RegistriesConfigured bool                 `json:"registries_configured"`
}

var searchToolSpec = toolSpec{
	summary: "Searches the local index of the configured registries.",
	description: "Search the local index of the configured registries for publishable entities, reading an offline " +
		"snapshot, writing nothing and contacting no network. Use it to find what exists to depend on before naming a reference. " +
		"Returns each match's URI, kind, version, description, category, labels and — for plugins — socket type and " +
		"capabilities, as values rather than a table. The reported `version` is the one a resolution would take: a " +
		"version whose retraction carries a signature this workspace trusts is excluded and named in `retracted`, so an " +
		"entity whose every version is retracted is reported with no version rather than disappearing. `indexes` gives " +
		"each searched registry's snapshot age and `stale` marks one worth refreshing; `missing_index_hosts` names " +
		"configured registries with no local index yet, whose contents are therefore absent from this answer. Refreshing " +
		"the snapshot is a separate operation that does reach the network.",
	inputSchema: `{
  "type": "object",
  "properties": {
    "query": {
      "type": "string",
      "description": "Case-insensitive substring matched against each entity's name and description. Omit to match every indexed entity."
    },
    "kinds": {
      "type": "array",
      "items": { "type": "string" },
      "description": "Entity kinds to match (component, feature, recipe, featurerecipe, workload, plugin). A record matching any one is kept. Case-insensitive."
    },
    "selector": {
      "type": "string",
      "description": "Label selector over the entity's catalog labels, which include its category (component.aggrega.ai/category=compute)."
    },
    "capabilities": {
      "type": "array",
      "items": { "type": "string" },
      "description": "Plugin capabilities to match. A record declaring any one is kept."
    },
    "plugin_type": {
      "type": "string",
      "description": "Plugin socket type to match, for example provider or governance. Exact match."
    },
    "registry": {
      "type": "string",
      "description": "Search only this registry, addressed by host or friendly alias. Omit to search every configured registry."
    },
    "limit": {
      "type": "integer",
      "description": "Cap the number of results. Zero or absent means no cap."
    },
    "include_retracted": {
      "type": "boolean",
      "description": "Report the highest published version whatever its retraction status, instead of the version a resolution would take. The retracted versions are named either way."
    },
    "workspace_root": {
      "type": "string",
      "description": "Optional workspace root override. Defaults to discovery from the server's working directory."
    }
  }
}`,
	invoke: searchInvoke,
}

func searchInvoke(s *server, ctx context.Context, args json.RawMessage) (*callToolResult, *rpcError) {
	var a searchArgs
	if len(args) > 0 {
		if err := json.Unmarshal(args, &a); err != nil {
			return invalidToolArguments("search", err)
		}
	}
	tw, err := s.resolveToolWorkspace(a.WorkspaceRoot, nil)
	if err != nil {
		return errorResult(explainToolError("Could not resolve the workspace to search its configured registries.", err, "component"))
	}

	res, searchErr := registryops.Search(ctx, tw.rw, registryops.Query{
		Text:             a.Query,
		Kinds:            a.Kinds,
		Selector:         a.Selector,
		Capabilities:     a.Capabilities,
		PluginType:       a.PluginType,
		Registry:         a.Registry,
		Limit:            a.Limit,
		IncludeRetracted: a.IncludeRetracted,
	}, discardWriter{}, time.Now())
	if searchErr != nil {
		return errorResult(explainToolError("The registry search could not be completed.", searchErr, "component"))
	}

	records := make([]searchRecordResult, 0, len(res.Records))
	for _, rec := range res.Records {
		records = append(records, searchRecordResult{Record: rec, Retracted: res.Screening[rec.URI].Retracted})
	}
	indexes := make([]searchIndexResult, 0, len(res.Indexes))
	for _, li := range res.Indexes {
		e := searchIndexResult{Host: li.Host}
		if li.HasTimestamp {
			e.LastUpdated = li.LastUpdated.Format(time.RFC3339)
		}
		indexes = append(indexes, e)
	}
	missing := res.MissingIndexHosts
	if missing == nil {
		missing = []string{}
	}

	payload := searchResult{
		Records:              records,
		Indexes:              indexes,
		MissingIndexHosts:    missing,
		Stale:                res.Stale,
		RegistriesConfigured: !res.NoRegistriesConfigured,
	}
	payload.WorkspaceRoot, payload.WorkspaceRootWithheld = tw.reportedLocationField(tw.root)
	return tw.answer(textResult(renderSearchText(payload), payload))
}

// renderSearchText renders the readable result: the matches, then what the answer
// rests on — which snapshots were read and which registries could not be.
func renderSearchText(p searchResult) string {
	var b strings.Builder
	if !p.RegistriesConfigured {
		return "No registries are configured for " + workspaceRootWords(p.WorkspaceRoot) + ", so there is no index to search."
	}
	if len(p.Records) == 0 {
		b.WriteString("No entities matched.\n")
	} else {
		fmt.Fprintf(&b, "Matched %d entit%s:\n", len(p.Records), plural(len(p.Records), "y", "ies"))
		for _, rec := range p.Records {
			fmt.Fprintf(&b, "  %s  %s", rec.URI, rec.Kind)
			if rec.Version != "" {
				fmt.Fprintf(&b, "  %s", rec.Version)
			} else if len(rec.Retracted) > 0 {
				b.WriteString("  (no version remains)")
			}
			if len(rec.Retracted) > 0 {
				fmt.Fprintf(&b, "  [retracted: %s]", strings.Join(rec.Retracted, ", "))
			}
			b.WriteByte('\n')
		}
	}
	for _, li := range p.Indexes {
		if li.LastUpdated != "" {
			fmt.Fprintf(&b, "Index for %s last updated %s\n", li.Host, li.LastUpdated)
		} else {
			fmt.Fprintf(&b, "Index for %s last updated (timestamp unavailable)\n", li.Host)
		}
	}
	for _, host := range p.MissingIndexHosts {
		fmt.Fprintf(&b, "Registry %s has no local index, so nothing it publishes is in this answer.\n", host)
	}
	if p.Stale {
		b.WriteString("One or more indexes are stale; refreshing them would reach the network.\n")
	}
	return strings.TrimRight(b.String(), "\n")
}

// plural picks the singular or plural form for a count.
func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}

// discardWriter swallows the screening layer's per-record commentary. That prose
// is written for a person watching a terminal; this surface answers with the
// verdict itself, which travels on each record.
type discardWriter struct{}

func (discardWriter) Write(p []byte) (int, error) { return len(p), nil }
