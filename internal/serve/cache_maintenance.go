// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package serve

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/aggrega-ai/aggrega-cli/internal/cacheops"
)

// cacheScope states how far the answer reaches: the classes the operation
// addressed whose contents are shared across every project on the machine.
//
// Every cache response carries one. A caller that asked a session opened over a
// workspace has every reason to read an answer as being about that workspace,
// and for two of the six classes that reading is wrong — so the response says
// which, rather than leaving the caller to recognise a class name. The field is
// always present and always an array, so "no machine-wide class was touched" is
// an empty list a client can act on rather than an absent key it has to guess at.
type cacheScope struct {
	MachineWideClasses []string `json:"machineWideClasses"`
	// WorkspaceRoot is the workspace this session was opened over, so a caller
	// reading a workspace-scoped removal knows which workspace it was.
	WorkspaceRoot string `json:"workspaceRoot"`
}

// cacheElementResult is one enumerated cache element on the wire.
//
// SizeText is the byte count written in the binary-unit vocabulary every surface
// spells sizes in, and it is the display half of a round trip: the same
// vocabulary is what [cacheops.ParseHumanSize] reads, so a quantity a caller
// shows can be handed straight back to an operation that takes a size. It is
// carried beside the number rather than instead of it — a caller that computes
// needs the number, and a caller that displays needs the text — so that no
// consumer has to reproduce the rounding rule to spell a size the way the rest
// of the product spells it.
type cacheElementResult struct {
	Label    string `json:"label"`
	Size     int64  `json:"size"`
	SizeText string `json:"sizeText"`
	Digest   string `json:"digest,omitempty"`
	Note     string `json:"note,omitempty"`
}

// cacheClassResult is one cache class on the wire: its name, how far its
// contents are shared, whether a prune can reclaim it, and its elements.
//
// The filesystem path of the class root is deliberately absent. A caller asking
// what the caches hold is asking about content, and a session that handed back
// absolute paths into the machine's own directories would be describing the host
// rather than answering the question.
type cacheClassResult struct {
	Name     string               `json:"name"`
	Reach    string               `json:"reach"`
	Prunable bool                 `json:"prunable"`
	Elements []cacheElementResult `json:"elements"`
}

// cacheStatusResult is the aggrega/cacheStatus response: every class with its
// elements, the recorded plugin provenance, and the totals.
//
// Every collection is an array rather than a null, so a client iterates
// unconditionally instead of special-casing an empty cache.
//
// TotalSizeText carries the total in the shared spelling, on the same terms as
// every other rendered size here.
type cacheStatusResult struct {
	Classes       []cacheClassResult      `json:"classes"`
	Provenance    []cacheProvenanceResult `json:"provenance"`
	TotalCount    int                     `json:"totalCount"`
	TotalSize     int64                   `json:"totalSize"`
	TotalSizeText string                  `json:"totalSizeText"`
	Scope         cacheScope              `json:"scope"`
}

// cacheProvenanceResult is one recorded content digest's resolved plugin
// identities. SharedBy is present only where more than one identity resolves to
// the same guest bytes.
type cacheProvenanceResult struct {
	Identities []string `json:"identities"`
	SharedBy   int      `json:"sharedBy,omitempty"`
}

// cacheRemovalResult is one element an operation removed, carrying the reach of
// the class it came from so a caller reading a single removal is told its scope
// without having to consult the summary.
type cacheRemovalResult struct {
	Class    string `json:"class"`
	Label    string `json:"label"`
	Reach    string `json:"reach"`
	Size     int64  `json:"size"`
	SizeText string `json:"sizeText"`
}

// cacheShortfallResult is a set of elements an operation left behind because it
// has no basis to reclaim them, with the operations that would.
type cacheShortfallResult struct {
	Class       string   `json:"class"`
	Count       int      `json:"count"`
	Size        int64    `json:"size"`
	SizeText    string   `json:"sizeText"`
	Reach       string   `json:"reach"`
	ReclaimedBy []string `json:"reclaimedBy"`
}

// cachePruneResult is the aggrega/cachePrune response.
type cachePruneResult struct {
	Removed              []cacheRemovalResult   `json:"removed"`
	RetainedCount        int                    `json:"retainedCount"`
	ReclaimedBytes       int64                  `json:"reclaimedBytes"`
	ReclaimedBytesText   string                 `json:"reclaimedBytesText"`
	UnattributedCount    int                    `json:"unattributedCount"`
	UnattributedSize     int64                  `json:"unattributedSize"`
	UnattributedSizeText string                 `json:"unattributedSizeText"`
	NotPruneEligible     []cacheShortfallResult `json:"notPruneEligible"`
	Scope                cacheScope             `json:"scope"`
}

// cacheEvictParams selects the size cap the compiled-module cache is trimmed to.
//
// MaxSize is spelled as the command line spells it and read by the same parser,
// so a quantity a caller learned on one surface is accepted on the other.
type cacheEvictParams struct {
	MaxSize string `json:"maxSize"`
}

// cacheEvictResult is the aggrega/cacheEvict response.
// CapBytesText closes the loop the cap opened: the cap arrived as text a caller
// wrote, and it leaves as text in the same vocabulary, so the value a caller
// reads back is one it could send again unchanged.
type cacheEvictResult struct {
	WithinCapBefore    bool                 `json:"withinCapBefore"`
	Removed            []cacheRemovalResult `json:"removed"`
	TotalBefore        int64                `json:"totalBefore"`
	TotalBeforeText    string               `json:"totalBeforeText"`
	ReclaimedBytes     int64                `json:"reclaimedBytes"`
	ReclaimedBytesText string               `json:"reclaimedBytesText"`
	RemainingBytes     int64                `json:"remainingBytes"`
	RemainingBytesText string               `json:"remainingBytesText"`
	CapBytes           int64                `json:"capBytes"`
	CapBytesText       string               `json:"capBytesText"`
	Scope              cacheScope           `json:"scope"`
}

// cacheClearedResult is one whole subtree a wipe removed.
type cacheClearedResult struct {
	Class    string `json:"class"`
	Reach    string `json:"reach"`
	Size     int64  `json:"size"`
	SizeText string `json:"sizeText"`
}

// cacheClearResult is the aggrega/cacheClear response.
type cacheClearResult struct {
	Cleared            []cacheClearedResult `json:"cleared"`
	ReclaimedBytes     int64                `json:"reclaimedBytes"`
	ReclaimedBytesText string               `json:"reclaimedBytesText"`
	Scope              cacheScope           `json:"scope"`
}

// cacheLocations resolves the cache roots this session addresses.
//
// The workspace half comes from the workspace the session was opened over; the
// machine-wide half is filled by the shared layer, which is the same fill every
// other surface gets. A session with no resolved workspace still addresses the
// machine-wide caches, which is the honest answer rather than a refusal: those
// caches exist and their contents are reportable.
func (s *Server) cacheLocations() cacheops.Locations {
	var root, cacheDir string
	if s.resolvedWorkspace != nil {
		root = s.resolvedWorkspace.Root()
		cacheDir = s.resolvedWorkspace.Config().ResolvedArtifactCacheDir()
	}
	return cacheops.ResolveLocations(root, cacheDir)
}

// cacheScopeOf assembles the scope statement from the classes an operation
// actually acted on.
//
// It takes the class names rather than deriving them from the operation, because
// the operation is not what decides the reach — the classes are, and they declare
// it where they are assembled. An operation that touched no machine-wide class
// reports an empty list rather than an omitted field.
func (s *Server) cacheScopeOf(machineWide []string) cacheScope {
	if machineWide == nil {
		machineWide = []string{}
	}
	return cacheScope{MachineWideClasses: machineWide, WorkspaceRoot: s.workspaceRoot}
}

// dispatchCacheStatus reports what the on-disk caches hold.
//
// It writes nothing. The reach statement is still carried, because a caller
// deciding whether to reclaim needs to know which of the classes it is reading
// about are shared with every other project on the machine before it acts.
func (s *Server) dispatchCacheStatus(ctx context.Context, req *jsonRPCRequest) error {
	if err := ctx.Err(); err != nil {
		return s.writeError(ctx, req.ID, jsonRPCInternalError, err.Error())
	}
	loc := s.cacheLocations()
	report := cacheops.Status(loc)

	classes := make([]cacheClassResult, 0, len(report.Classes))
	for _, cl := range report.Classes {
		elements := make([]cacheElementResult, 0, len(cl.Elements))
		for _, el := range cl.Elements {
			elements = append(elements, cacheElementResult{
				Label:    el.Label,
				Size:     el.Size,
				SizeText: cacheops.HumanSize(el.Size),
				Digest:   el.DigestHex,
				Note:     el.Note,
			})
		}
		classes = append(classes, cacheClassResult{
			Name:     cl.Name,
			Reach:    string(cl.Reach),
			Prunable: cl.Prunable,
			Elements: elements,
		})
	}

	provenance := make([]cacheProvenanceResult, 0, len(report.Provenance))
	for _, p := range report.Provenance {
		provenance = append(provenance, cacheProvenanceResult{Identities: p.Identities, SharedBy: p.SharedBy})
	}

	return s.writeResult(ctx, req.ID, cacheStatusResult{
		Classes:       classes,
		Provenance:    provenance,
		TotalCount:    report.TotalCount,
		TotalSize:     report.TotalSize,
		TotalSizeText: cacheops.HumanSize(report.TotalSize),
		Scope:         s.cacheScopeOf(cacheops.MachineWideClasses(report.Classes)),
	})
}

// dispatchCachePrune reclaims cache entries no workspace lockfile references.
//
// It writes, and it is carried here for the same reason every other writing
// method is: a caller that sent this has asked for the reclamation, exactly as a
// caller sending a build has asked for artifacts. The rule the daemon runs under
// is about when an operation runs rather than which surface runs it — a
// state-writing operation runs on explicit invocation and never while something
// is being validated or inspected — and nothing here runs during either.
//
// A removal failure part-way through is reported as an error whose data carries
// the removals that did happen, so a caller is never told a partial prune was no
// prune at all.
func (s *Server) dispatchCachePrune(ctx context.Context, req *jsonRPCRequest) error {
	loc := s.cacheLocations()
	res, err := cacheops.Prune(loc)
	if ctx.Err() != nil {
		return nil
	}
	if err != nil {
		return s.writeError(ctx, req.ID, jsonRPCInternalError, err.Error())
	}
	return s.writeResult(ctx, req.ID, cachePruneResult{
		Removed:              cacheRemovals(res.Removed),
		RetainedCount:        res.RetainedCount,
		ReclaimedBytes:       res.ReclaimedBytes,
		ReclaimedBytesText:   cacheops.HumanSize(res.ReclaimedBytes),
		UnattributedCount:    res.UnattributedCount,
		UnattributedSize:     res.UnattributedSize,
		UnattributedSizeText: cacheops.HumanSize(res.UnattributedSize),
		NotPruneEligible:     cacheShortfalls(res.NotPruneEligible),
		Scope:                s.cacheScopeOf(res.MachineWide),
	})
}

// dispatchCacheEvict trims the compiled-module cache to a size cap.
//
// The subject is machine-wide without qualification: every project on the machine
// draws on that one store, so an eviction a session performs reaches workspaces
// this session has never heard of. The response says so, and it is the surface
// that fills that store — once per guest, for every session on the machine — that
// carries the way to trim it.
func (s *Server) dispatchCacheEvict(ctx context.Context, req *jsonRPCRequest) error {
	var params cacheEvictParams
	if len(req.Params) > 0 {
		if err := json.Unmarshal(req.Params, &params); err != nil {
			return s.writeError(ctx, req.ID, jsonRPCInvalidParams, "invalid params: "+err.Error())
		}
	}
	if strings.TrimSpace(params.MaxSize) == "" {
		return s.writeError(ctx, req.ID, jsonRPCInvalidParams,
			"cache evict: maxSize is required (a byte quantity such as 5GiB, 512MiB, or a byte count)")
	}
	capBytes, err := cacheops.ParseHumanSize(params.MaxSize)
	if err != nil {
		return s.writeError(ctx, req.ID, jsonRPCInvalidParams, "cache evict: invalid maxSize: "+err.Error())
	}
	if capBytes <= 0 {
		return s.writeError(ctx, req.ID, jsonRPCInvalidParams, "cache evict: maxSize must be greater than zero")
	}

	res, evictErr := cacheops.Evict(s.cacheLocations(), capBytes)
	if ctx.Err() != nil {
		return nil
	}
	if evictErr != nil {
		return s.writeError(ctx, req.ID, jsonRPCInternalError, evictErr.Error())
	}
	return s.writeResult(ctx, req.ID, cacheEvictResult{
		WithinCapBefore:    res.WithinCapBefore,
		Removed:            cacheRemovals(res.Removed),
		TotalBefore:        res.TotalBefore,
		TotalBeforeText:    cacheops.HumanSize(res.TotalBefore),
		ReclaimedBytes:     res.ReclaimedBytes,
		ReclaimedBytesText: cacheops.HumanSize(res.ReclaimedBytes),
		RemainingBytes:     res.RemainingBytes,
		RemainingBytesText: cacheops.HumanSize(res.RemainingBytes),
		CapBytes:           res.CapBytes,
		CapBytesText:       cacheops.HumanSize(res.CapBytes),
		Scope:              s.cacheScopeOf(res.MachineWide),
	})
}

// dispatchCacheClear wipes every cache this session addresses.
//
// It is the widest of these operations and the response is explicit about it:
// the wipe reaches the workspace's own artifact cache and the two machine-wide
// stores, and the scope names the machine-wide ones it actually removed. The
// command line fronts this with a confirmation because a person is watching it;
// here nobody is, so there is nothing to prompt and no prompt is invented — a
// caller that sent this asked for it, and what the response owes in exchange is
// an accurate statement of what it reached.
func (s *Server) dispatchCacheClear(ctx context.Context, req *jsonRPCRequest) error {
	res, err := cacheops.Clear(s.cacheLocations())
	if ctx.Err() != nil {
		return nil
	}
	if err != nil {
		return s.writeError(ctx, req.ID, jsonRPCInternalError, err.Error())
	}
	cleared := make([]cacheClearedResult, 0, len(res.Cleared))
	for _, c := range res.Cleared {
		cleared = append(cleared, cacheClearedResult{
			Class:    c.Class,
			Reach:    string(c.Reach),
			Size:     c.Size,
			SizeText: cacheops.HumanSize(c.Size),
		})
	}
	return s.writeResult(ctx, req.ID, cacheClearResult{
		Cleared:            cleared,
		ReclaimedBytes:     res.ReclaimedBytes,
		ReclaimedBytesText: cacheops.HumanSize(res.ReclaimedBytes),
		Scope:              s.cacheScopeOf(res.MachineWide),
	})
}

// cacheRemovals projects the shared layer's removals onto the wire shape,
// carrying each one's reach.
func cacheRemovals(in []cacheops.Removal) []cacheRemovalResult {
	out := make([]cacheRemovalResult, 0, len(in))
	for _, r := range in {
		out = append(out, cacheRemovalResult{
			Class:    r.Class,
			Label:    r.Label,
			Reach:    string(r.Reach),
			Size:     r.Size,
			SizeText: cacheops.HumanSize(r.Size),
		})
	}
	return out
}

// cacheShortfalls projects the shared layer's shortfalls onto the wire shape.
func cacheShortfalls(in []cacheops.Shortfall) []cacheShortfallResult {
	out := make([]cacheShortfallResult, 0, len(in))
	for _, s := range in {
		out = append(out, cacheShortfallResult{
			Class:       s.Class,
			Count:       s.Count,
			Size:        s.Size,
			SizeText:    cacheops.HumanSize(s.Size),
			Reach:       string(s.Reach),
			ReclaimedBy: s.ReclaimedBy,
		})
	}
	return out
}
