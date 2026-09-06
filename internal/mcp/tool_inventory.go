// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/aggrega-ai/aggrega-cli/internal/cacheops"
	"github.com/aggrega-ai/aggrega-cli/internal/registryindex"
	"github.com/aggrega-ai/aggrega-cli/internal/registryops"
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
type cacheStatusElement struct {
	Label  string `json:"label"`
	Size   int64  `json:"size"`
	Digest string `json:"digest,omitempty"`
	Note   string `json:"note,omitempty"`
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
type cacheStatusResult struct {
	WorkspaceRoot      string             `json:"workspace_root"`
	Classes            []cacheStatusClass `json:"classes"`
	TotalCount         int                `json:"total_count"`
	TotalSize          int64              `json:"total_size"`
	MachineWideClasses []string           `json:"machine_wide_classes"`
}

var cacheStatusToolSpec = toolSpec{
	description: "Report what the on-disk caches hold, reading them and writing nothing. Returns one entry per cached " +
		"element with its size and, where it has one, its content digest — so you can total, compare or select without " +
		"parsing a table. Each class carries a `reach`: `workspace` for the artifact cache under this workspace's own " +
		"cache directory, `machine` for the compiled-module and plugin-describe stores, which every project on this " +
		"machine shares. Read `reach` before proposing any reclamation: removing a machine-wide element affects " +
		"workspaces nobody in this conversation has named. `machine_wide_classes` names those classes directly. " +
		"Optional `workspace_root` overrides discovery; without a resolvable workspace only the machine-wide classes " +
		"are reported.",
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
	var root, cacheDir string
	if tw, err := s.resolveToolWorkspace(a.WorkspaceRoot, nil); err == nil {
		root = tw.root
		cacheDir = tw.cacheDir
	}

	report := cacheops.Status(cacheops.ResolveLocations(root, cacheDir))
	classes := make([]cacheStatusClass, 0, len(report.Classes))
	for _, cl := range report.Classes {
		elements := make([]cacheStatusElement, 0, len(cl.Elements))
		for _, el := range cl.Elements {
			elements = append(elements, cacheStatusElement{
				Label:  el.Label,
				Size:   el.Size,
				Digest: el.DigestHex,
				Note:   el.Note,
			})
		}
		classes = append(classes, cacheStatusClass{
			Name:     cl.Name,
			Reach:    string(cl.Reach),
			Prunable: cl.Prunable,
			Elements: elements,
		})
	}
	machineWide := cacheops.MachineWideClasses(report.Classes)
	if machineWide == nil {
		machineWide = []string{}
	}

	payload := cacheStatusResult{
		WorkspaceRoot:      root,
		Classes:            classes,
		TotalCount:         report.TotalCount,
		TotalSize:          report.TotalSize,
		MachineWideClasses: machineWide,
	}
	return textResult(renderCacheStatusText(payload), payload)
}

// renderCacheStatusText renders the readable listing: one heading per class,
// marked where the class is machine-wide, then its elements.
func renderCacheStatusText(p cacheStatusResult) string {
	var b strings.Builder
	if p.WorkspaceRoot != "" {
		fmt.Fprintf(&b, "Caches for %s:\n", p.WorkspaceRoot)
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
			fmt.Fprintf(&b, "    %s  %s", el.Label, cacheops.HumanSize(el.Size))
			if el.Note != "" {
				fmt.Fprintf(&b, "  %s", el.Note)
			}
			b.WriteByte('\n')
		}
	}
	fmt.Fprintf(&b, "\n%d elements, %s\n", p.TotalCount, cacheops.HumanSize(p.TotalSize))
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
type registryListResult struct {
	WorkspaceRoot                string                 `json:"workspace_root"`
	Registries                   []registryops.Registry `json:"registries"`
	CredentialStateIsMachineWide bool                   `json:"credential_state_is_machine_wide"`
}

var registryListToolSpec = toolSpec{
	description: "Report the registries this workspace is configured to reach and the signature trust posture of each, " +
		"reading configuration, writing nothing and contacting no registry. Returns each entry's host, optional alias, whether it is the " +
		"default, its trust posture as discrete fields, and whether a credential for that host is present. The " +
		"credential state reports presence only — never the credential, never its shape, and never whether the registry " +
		"would accept it, which would take a network round trip. It is read from the machine-wide credential store, so " +
		"it is the same answer in every workspace on this machine. Use it to find the host or alias other operations " +
		"accept. Optional `workspace_root` overrides discovery.",
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

	entries := registryops.List(tw.cfg, registryops.AuthResolver(nil))
	if entries == nil {
		entries = []registryops.Registry{}
	}
	payload := registryListResult{
		WorkspaceRoot:                tw.root,
		Registries:                   entries,
		CredentialStateIsMachineWide: true,
	}
	return textResult(renderRegistryListText(payload), payload)
}

// renderRegistryListText renders one line per registry: the host, its alias, the
// trust posture in words, and its credential state.
func renderRegistryListText(p registryListResult) string {
	var b strings.Builder
	if len(p.Registries) == 0 {
		return "No registries are configured for " + p.WorkspaceRoot + "."
	}
	fmt.Fprintf(&b, "Registries for %s (%d):\n", p.WorkspaceRoot, len(p.Registries))
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
type searchResult struct {
	WorkspaceRoot        string               `json:"workspace_root"`
	Records              []searchRecordResult `json:"records"`
	Indexes              []searchIndexResult  `json:"indexes"`
	MissingIndexHosts    []string             `json:"missing_index_hosts"`
	Stale                bool                 `json:"stale"`
	RegistriesConfigured bool                 `json:"registries_configured"`
}

var searchToolSpec = toolSpec{
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
      "description": "Label selector over the entity's catalog labels. A category is a label: component.aggrega.ai/category=compute."
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

	res, searchErr := registryops.Search(tw.rw, registryops.Query{
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
		WorkspaceRoot:        tw.root,
		Records:              records,
		Indexes:              indexes,
		MissingIndexHosts:    missing,
		Stale:                res.Stale,
		RegistriesConfigured: !res.NoRegistriesConfigured,
	}
	return textResult(renderSearchText(payload), payload)
}

// renderSearchText renders the readable result: the matches, then what the answer
// rests on — which snapshots were read and which registries could not be.
func renderSearchText(p searchResult) string {
	var b strings.Builder
	if !p.RegistriesConfigured {
		return "No registries are configured for " + p.WorkspaceRoot + ", so there is no index to search."
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
