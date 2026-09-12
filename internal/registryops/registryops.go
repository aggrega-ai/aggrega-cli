// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

// Package registryops holds the registry inventory — which registries a
// workspace is configured to reach, and what the local index of them offers — as
// data rather than as rendered text.
//
// Every caller-facing surface reaches the same functions here and renders the
// result its own way, so the answer a caller gets cannot depend on which surface
// it asked. A surface that assembled its own listing would be a second reading of
// the same configuration, and the two would agree until the day a field was added
// to one of them.
//
// # What is in the workspace and what is not
//
// Both readings here are anchored to the workspace. The configured set is the
// merged effective configuration a workspace resolution produces — the same one
// resolution and publication consult — and the searchable index is written under
// the workspace's own configured cache directory. Neither reaches a registry: the
// inventory reads configuration, and the index read is offline by construction.
//
// The one thing that is NOT workspace-scoped is credential presence. A credential
// lives in the machine-wide store, so a listing that reports it is reporting a
// machine-wide fact, and it reports presence only — never the credential, never
// its shape, and never whether a registry would accept it, which would take a
// network round trip a listing does not make.
package registryops

import (
	"context"
	"fmt"
	"io"
	"sort"
	"time"

	"oras.land/oras-go/v2/registry/remote/auth"
	"oras.land/oras-go/v2/registry/remote/credentials"

	"github.com/aggrega-ai/aggrega-cli/internal/registryindex"
	"github.com/aggrega-ai/aggrega-cli/pkg/oci"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// The credential states a listing reports for a host. They say whether a
// credential is present, never what it is and never whether the registry would
// accept it — establishing the latter takes a network round trip, which a
// listing does not make.
const (
	// AuthConfigured means the credential store holds an entry for the host.
	AuthConfigured = "configured"
	// AuthNone means it holds none.
	AuthNone = "none"
	// AuthUnknown means the credential store could not be consulted, so presence
	// is genuinely undetermined rather than absent.
	AuthUnknown = "unknown"
)

// Trust is one registry's configured signature trust posture as discrete fields,
// the same configuration a table renders as a plain-language sentence.
//
// Anchor is "keyed" when a public key is pinned, "keyless" when identities are,
// and empty when the entry pins neither.
type Trust struct {
	SignaturesRequired  bool   `json:"signaturesRequired"`
	VerificationSkipped bool   `json:"verificationSkipped,omitempty"`
	Advisory            bool   `json:"advisory,omitempty"`
	Anchor              string `json:"anchor,omitempty"`
	KeylessIdentities   int    `json:"keylessIdentities,omitempty"`
}

// Registry is one configured registry as every rendering sees it.
//
// Host is the identity; Name is the optional friendly alias that resolves to it.
// AuthState reports whether a credential for Host is present in the machine-wide
// credential store. No field here carries credential material, its length, its
// shape, or any rendering of it: presence is the whole of what is reported, which
// is what makes the value safe to log, paste into an issue, or commit.
type Registry struct {
	Host    string `json:"host"`
	Name    string `json:"name,omitempty"`
	Default bool   `json:"default"`
	// AuthState is emitted even when empty. A caller distinguishes "no credential
	// is present" from "the listing did not look" by the value, and an omitted key
	// collapses the two into one absence.
	AuthState string `json:"authState"`
	Trust     Trust  `json:"trust"`
}

// List returns the effective registries in ascending host order, each with its
// default marker and, when authOf is non-nil, its credential state.
//
// The order is a total sort over the host, which is the entry's identity and
// therefore unique across the merged set — so the sequence is fixed for a given
// configuration regardless of which files contributed which entries or in what
// order they merged. Every rendering consumes this one sequence, which is what
// keeps them from disagreeing about order.
//
// authOf is a parameter rather than a default so that a caller which does not
// report credential state opens no credential store at all. That is not an
// optimisation: consulting a machine-wide keychain is a side effect, and a
// listing that does not show the column has no business causing it.
func List(cfg workspaceconfig.Config, authOf func(host string) string) []Registry {
	regs := make([]workspaceconfig.RegistryConfig, len(cfg.Registries))
	copy(regs, cfg.Registries)
	sort.Slice(regs, func(i, j int) bool { return regs[i].Host < regs[j].Host })

	out := make([]Registry, 0, len(regs))
	for _, r := range regs {
		e := Registry{
			Host:    r.Host,
			Name:    r.Name,
			Default: r.Host == cfg.DefaultRegistry,
			Trust:   TrustOf(r),
		}
		if authOf != nil {
			e.AuthState = authOf(r.Host)
		}
		out = append(out, e)
	}
	return out
}

// TrustOf projects a registry's configured trust posture onto the structured
// shape.
func TrustOf(r workspaceconfig.RegistryConfig) Trust {
	t := Trust{
		SignaturesRequired:  r.Signing.Required,
		VerificationSkipped: r.SkipSignatureVerification,
		Advisory:            r.Advisory,
	}
	switch {
	case r.PublicKey != "":
		t.Anchor = "keyed"
	case len(r.TrustedIdentities) > 0:
		t.Anchor = "keyless"
		t.KeylessIdentities = len(r.TrustedIdentities)
	}
	return t
}

// AuthResolver returns a per-host credential-state lookup over store, opening the
// shared credential store when store is nil.
//
// A store that cannot be opened yields AuthUnknown for every host rather than
// failing the listing: the credential state is one column of a read-only
// enumeration, and an unreadable keychain is no reason to withhold the registry
// set. The returned function reports presence by asking the store for the host's
// entry; it never inspects, measures, or retains the credential.
func AuthResolver(store credentials.Store) func(host string) string {
	if store == nil {
		s, err := credentials.NewStoreFromDocker(credentials.StoreOptions{})
		if err != nil {
			return func(string) string { return AuthUnknown }
		}
		store = s
	}
	return func(host string) string {
		cred, err := store.Get(context.Background(), host)
		if err != nil {
			return AuthUnknown
		}
		if cred == auth.EmptyCredential {
			return AuthNone
		}
		return AuthConfigured
	}
}

// IndexStaleAfter is the fixed age beyond which a loaded index is considered
// clearly old and a search advises refreshing it. It is a plain constant, not a
// configurable or tuned threshold: the advisory only nudges; it never forces a
// network refresh.
const IndexStaleAfter = 30 * 24 * time.Hour

// Screening is what one entity's recorded withdrawals mean for the version a
// search reports: the versions screened out of selection, ascending, and the
// version a resolution would take once they are removed.
//
// Selected is empty when every published version was screened, which is the case
// a result must state rather than present as an ordinary row.
type Screening struct {
	Retracted []string `json:"retracted"`
	Selected  string   `json:"selected"`
}

// Query is what a search asks of the local index.
//
// Every field narrows: an empty one imposes no constraint. Registry selects a
// single configured registry by host or friendly alias; empty searches every
// configured registry. Limit caps the result count, with zero meaning no cap.
// IncludeRetracted reports the highest published version whatever its retraction
// status, rather than the version a resolution would take — it changes only the
// reported version, since the screening result is returned either way.
type Query struct {
	Text             string
	Kinds            []string
	Selector         string
	Capabilities     []string
	PluginType       string
	Registry         string
	Limit            int
	IncludeRetracted bool
}

// SearchResult is what a search found, with everything a surface needs in order
// to state the answer's provenance alongside the answer.
//
// NoRegistriesConfigured distinguishes "nothing is configured" from "nothing
// matched", which are different answers to a caller deciding what to do next.
// MissingIndexHosts names the configured registries that have no local index yet,
// so an incomplete answer says which registries it could not consult rather than
// silently answering from fewer.
type SearchResult struct {
	Records                []registryindex.Record
	Screening              map[string]Screening
	Indexes                []registryindex.Loaded
	MissingIndexHosts      []string
	Stale                  bool
	NoRegistriesConfigured bool
}

// IndexesStale reports whether any loaded snapshot is clearly old — its timestamp
// is absent, or older than IndexStaleAfter relative to now — so a surface can
// advise refreshing. It reads only the already-loaded timestamps and contacts no
// network.
func IndexesStale(indexes []registryindex.Loaded, now time.Time) bool {
	for _, li := range indexes {
		if !li.HasTimestamp || now.Sub(li.LastUpdated) > IndexStaleAfter {
			return true
		}
	}
	return false
}

// stopped reports that the caller no longer wants the operation, as an error
// that errors.Is matches against context.Canceled and context.DeadlineExceeded.
// It returns nil while the caller is still waiting, which is every ordinary run.
//
// It is one function rather than an inline check written at each interruption
// point so the sentence a stop produces is stated once for the operations that
// POLL for a withdrawal.
//
// 🔴 It is deliberately NOT the same thing as the check in pinAdvertisedTrust,
// and unifying the two would lose a distinction. This one asks "has the caller
// withdrawn?" at a seam where no error has occurred. That one asks "was this
// fetch error CAUSED by a withdrawal?", in order to keep a cancellation from
// being folded into the tolerated advertises-nothing outcome — a different
// question, asked only on an error path, and its sentence names the host it was
// waiting on.
func stopped(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("aggrega registry: stopped before the operation finished: %w", err)
	}
	return nil
}

// Search reads the local index of the configured registries and returns the
// records matching the query, together with what the withdrawal screening found.
//
// It contacts no network. The index is the offline snapshot the index build
// writes under the workspace's own configured cache directory, so this reading is
// anchored to the workspace exactly as every other reading a session serves is.
//
// Host-level absence handling depends on which path selected the hosts. When the
// caller targets a single registry, a missing index there is a returned error
// naming the index build. When no selection is made, a host with no index yet is
// recorded in MissingIndexHosts and the search continues to the next host. If the
// effective host set is non-empty but not one index loaded, the search fails
// rather than presenting an empty result as a successful search.
//
// notices carries the sub-layer's own per-record commentary — a withdrawal marker
// present but not honoured, and the like. It is a writer rather than returned
// data because the screening constructor owns that wording, and a second copy
// assembled here would be a second description of the same finding.
func Search(ctx context.Context, rw *workspaceconfig.ResolvedWorkspace, q Query, notices io.Writer, now time.Time) (SearchResult, error) {
	var res SearchResult
	cfg := rw.Config()

	singleExplicit := q.Registry != ""
	var hosts []string
	if singleExplicit {
		host, err := cfg.ResolveRegistry(q.Registry)
		if err != nil {
			return res, err
		}
		hosts = []string{host}
	} else {
		for _, r := range cfg.Registries {
			hosts = append(hosts, r.Host)
		}
	}
	if len(hosts) == 0 {
		res.NoRegistriesConfigured = true
		return res, nil
	}

	root := rw.Root()
	cacheDir := cfg.ResolvedArtifactCacheDir()

	var loaded []registryindex.Loaded
	for _, host := range hosts {
		// One interruption point per host. Each iteration reads and decodes that
		// host's whole index from disk, so a host not yet reached is one whose index
		// was never read.
		if serr := stopped(ctx); serr != nil {
			return res, serr
		}
		idx, err := registryindex.LoadHost(root, cacheDir, host)
		if err != nil {
			if registryindex.IsNotBuilt(err) {
				if singleExplicit {
					return res, &IndexNotBuiltError{Host: host}
				}
				res.MissingIndexHosts = append(res.MissingIndexHosts, host)
				continue
			}
			return res, err
		}
		loaded = append(loaded, idx)
	}
	if len(loaded) == 0 {
		// The hosts that could not be consulted travel with the failure rather than
		// being discarded by it. A caller told only that nothing could be searched
		// cannot tell which registries were the reason.
		return res, ErrNoIndexAnywhere
	}

	// Hosts ascending, so the sequence is fixed regardless of configuration order.
	byHost := make([]registryindex.Loaded, len(loaded))
	copy(byHost, loaded)
	sort.Slice(byHost, func(i, j int) bool { return byHost[i].Host < byHost[j].Host })
	res.Indexes = byHost
	res.Stale = IndexesStale(byHost, now)

	records, screening := screenRecords(loaded, q.IncludeRetracted, notices)
	registryindex.SortRecords(records)

	matched, err := filterRecords(records, q)
	if err != nil {
		return res, err
	}
	if q.Limit > 0 && len(matched) > q.Limit {
		matched = matched[:q.Limit]
	}
	res.Records = matched
	res.Screening = screening
	return res, nil
}

// screenRecords applies each host's own recorded withdrawals to that host's
// records, and returns the merged record set together with, per artifact URI,
// what the screening found. A host recording no verified withdrawal forms no
// screen and its records pass through untouched, so a result that meets no
// withdrawal is the result the unscreened path produced.
//
// The withdrawal judgement is not made here. The screen comes from the one
// constructor that turns index records into a screen, which is the same one the
// resolution and plugin-attach paths reach when they build a screen from a loaded
// index — so a version is withdrawn on identical grounds, a verified signature and
// the withdrawal severity, whichever surface is asking. This function only decides
// what to do with the answer.
//
// The screen is addressed with the reference form of each record's URI and asked
// about that record's published versions, then the reported version is re-selected
// from what survives with the same selection primitive the index build used to
// choose the headline in the first place. That is what makes the reported version
// mean "the version a resolution would take" rather than "the headline the index
// happened to record".
//
// includeRetracted keeps the recorded headline instead. It changes only the
// reported version: the screening result is returned either way, so the versions
// left out are named in the result whichever question the reader is asking.
//
// A record carrying no published-version list yields no exclusions and passes
// through unchanged, which is the honest outcome — with nothing to screen there is
// no basis to alter what the record reports.
func screenRecords(indexes []registryindex.Loaded, includeRetracted bool, notices io.Writer) ([]registryindex.Record, map[string]Screening) {
	var records []registryindex.Record
	screening := map[string]Screening{}
	for _, li := range indexes {
		screen := registryindex.NewScreenFromRecords(li.Records, registryindex.IndexProvenance(li.LastUpdated, li.HasTimestamp), notices)
		for _, rec := range li.Records {
			if screen != nil {
				eligible, excluded := screen.ScreenVersions(registryindex.ReferenceForURI(rec.URI), rec.Versions)
				if len(excluded) > 0 {
					retracted := make([]string, 0, len(excluded))
					for _, x := range excluded {
						retracted = append(retracted, x.Version)
					}
					selected := oci.SelectLatestVersion(eligible, false /* includePreRelease */)
					screening[rec.URI] = Screening{Retracted: retracted, Selected: selected}
					if !includeRetracted {
						rec.Version = selected
					}
				}
			}
			records = append(records, rec)
		}
	}
	return records, screening
}
