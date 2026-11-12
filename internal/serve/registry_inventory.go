// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package serve

import (
	"context"
	"io"
	"time"

	"github.com/aggrega-ai/aggrega-cli/internal/registryindex"
	"github.com/aggrega-ai/aggrega-cli/internal/registryops"
)

// registryListResult is the aggrega/registryList response: every registry the
// session's resolved configuration reaches, in ascending host order.
//
// The slice is always an array, empty rather than absent, so a workspace with
// nothing configured reads as an empty set rather than as a missing field.
//
// CredentialStateIsMachineWide is stated rather than assumed. Every other field
// here is a property of the workspace's own configuration cascade, and the
// credential state is not: it is read from the machine-wide credential store, so
// two sessions over different workspaces see the same answer for the same host.
// A caller told the two apart can reason about which of them a colleague would
// also see.
type registryListResult struct {
	Registries                   []registryops.Registry `json:"registries"`
	CredentialStateIsMachineWide bool                   `json:"credentialStateIsMachineWide"`
}

// dispatchRegistryList reports the registries this workspace is configured to
// reach and the signature trust posture of each.
//
// The subject is the merged effective configuration the session already resolved
// — the same cascade the effective-configuration method reports the provenance of
// — so this is a reading of what the session holds rather than of somewhere else.
// It contacts no registry: a trust posture is what the configuration pins, and
// asking a registry whether it agrees would be a network round trip a listing
// does not make.
//
// It does consult the machine-wide credential store, for presence only. No field
// carries credential material, its length, its shape, or any rendering of it, and
// whether the registry would accept it is not asked.
func (s *Server) dispatchRegistryList(ctx context.Context, req *jsonRPCRequest) error {
	// A method with no parameters of its own still decodes, so a field a caller
	// sent is refused rather than dropped. See noParams for why an answer that
	// ignored an argument is worse than a refusal naming it.
	if err := decodeParams(req, &noParams{}); err != nil {
		return s.writeRefusal(ctx, req.ID, jsonRPCInvalidParams, "invalid params: ", err)
	}

	if err := ctx.Err(); err != nil {
		return s.writeRefusal(ctx, req.ID, jsonRPCInternalError, "", err)
	}
	if s.resolvedWorkspace == nil {
		return s.writeError(ctx, req.ID, jsonRPCInternalError,
			"the daemon holds no resolved workspace to report configured registries for")
	}
	entries := registryops.List(s.resolvedWorkspace.Config(), registryops.AuthResolver(ctx, nil))
	if entries == nil {
		entries = []registryops.Registry{}
	}
	return s.writeResult(ctx, req.ID, registryListResult{
		Registries:                   entries,
		CredentialStateIsMachineWide: true,
	})
}

// registrySearchParams narrows a search of the local index.
//
// Every field narrows and every one is optional: an absent field imposes no
// constraint, so an empty params object asks for everything the index holds. The
// spellings are the ones the command line uses, so a caller that learned the
// facets on one surface knows them on the other.
type registrySearchParams struct {
	Query            string   `json:"query"`
	Kinds            []string `json:"kinds"`
	Selector         string   `json:"selector"`
	Capabilities     []string `json:"capabilities"`
	PluginType       string   `json:"pluginType"`
	Registry         string   `json:"registry"`
	Limit            int      `json:"limit"`
	IncludeRetracted bool     `json:"includeRetracted"`
}

// registrySearchRecord is one matched entity: the index record, plus the versions
// screened out of selection.
//
// The record is embedded rather than nested, so every field it carries is emitted
// where a reader of the index's own shape expects it. Retracted names the
// versions a resolution would refuse — the product's own verdict, rather than
// something a consumer re-derives from the raw deprecation entries, since a
// re-derivation is free to disagree.
type registrySearchRecord struct {
	registryindex.Record
	Retracted []string `json:"retracted,omitempty"`
}

// registrySearchIndex is one searched host's freshness.
//
// LastUpdated is absent rather than zero when the snapshot carries no timestamp,
// so a caller distinguishes "not recorded" from "the epoch".
type registrySearchIndex struct {
	Host        string `json:"host"`
	LastUpdated string `json:"lastUpdated,omitempty"`
}

// registrySearchResult is the aggrega/search response.
//
// The provenance travels with the answer rather than beside it. A search is
// answered from an offline snapshot, so a caller acting on the result needs to
// know which registries were consulted, how old each snapshot is, whether any is
// stale enough to be worth refreshing, and which configured registries could not
// be consulted at all. Reporting the records alone would present a partial answer
// as a complete one.
type registrySearchResult struct {
	Records           []registrySearchRecord `json:"records"`
	Indexes           []registrySearchIndex  `json:"indexes"`
	MissingIndexHosts []string               `json:"missingIndexHosts"`
	Stale             bool                   `json:"stale"`
	// RegistriesConfigured is false when the workspace configures no registry at
	// all, which is a different answer from a search that matched nothing.
	RegistriesConfigured bool `json:"registriesConfigured"`
}

// dispatchRegistrySearch searches the local index of the configured registries.
//
// It contacts no network and it needs no credential: the index is the offline
// snapshot written under the workspace's own configured cache directory, which is
// a subject this session already holds. A refresh of that snapshot does reach
// registries, and is a different operation.
func (s *Server) dispatchRegistrySearch(ctx context.Context, req *jsonRPCRequest) error {
	var params registrySearchParams
	if len(req.Params) > 0 {
		if err := decodeParams(req, &params); err != nil {
			return s.writeRefusal(ctx, req.ID, jsonRPCInvalidParams, "invalid params: ", err)
		}
	}
	if s.resolvedWorkspace == nil {
		return s.writeError(ctx, req.ID, jsonRPCInternalError,
			"the daemon holds no resolved workspace to search the configured registries of")
	}

	// The screening layer's own per-record commentary is discarded here rather
	// than folded into the response. It is prose written for a person watching a
	// terminal, and this surface answers with the verdict itself — the screened
	// versions travel on each record.
	res, err := registryops.Search(ctx, s.resolvedWorkspace, registryops.Query{
		Text:             params.Query,
		Kinds:            params.Kinds,
		Selector:         params.Selector,
		Capabilities:     params.Capabilities,
		PluginType:       params.PluginType,
		Registry:         params.Registry,
		Limit:            params.Limit,
		IncludeRetracted: params.IncludeRetracted,
	}, io.Discard, time.Now())
	if ctx.Err() != nil {
		return nil
	}
	if err != nil {
		return s.writeRefusal(ctx, req.ID, jsonRPCInvalidParams, "", err)
	}

	records := make([]registrySearchRecord, 0, len(res.Records))
	for _, rec := range res.Records {
		records = append(records, registrySearchRecord{Record: rec, Retracted: res.Screening[rec.URI].Retracted})
	}
	indexes := make([]registrySearchIndex, 0, len(res.Indexes))
	for _, li := range res.Indexes {
		e := registrySearchIndex{Host: li.Host}
		if li.HasTimestamp {
			e.LastUpdated = li.LastUpdated.Format(time.RFC3339)
		}
		indexes = append(indexes, e)
	}
	missing := res.MissingIndexHosts
	if missing == nil {
		missing = []string{}
	}

	return s.writeResult(ctx, req.ID, registrySearchResult{
		Records:              records,
		Indexes:              indexes,
		MissingIndexHosts:    missing,
		Stale:                res.Stale,
		RegistriesConfigured: !res.NoRegistriesConfigured,
	})
}
