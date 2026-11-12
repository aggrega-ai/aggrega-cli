// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package main

import (
	"fmt"
	"go/token"
	"os"
	"sort"
	"strings"
	"sync"
)

// The caller read: which surfaces reach which stages, and therefore which
// declared checks each surface can exercise.
//
// A check's stage says where the rule runs. It does not say whether the surface a
// user is standing in ever gets that far, and that second question is the one an
// author arrives with when a document a command accepts is refused by another. The
// two together are what makes the surfaces that miss checks enumerable instead of
// discovered one report at a time.
//
// # 🔴 REACHABLE FROM, NEVER REACHED BY — and the coarseness is honest
//
// Every figure this read produces answers "reachable from the entry this surface
// calls". It does NOT answer "reached on this call". An entry may reach several
// stages, and whether a particular call gets to the deepest of them can depend on
// a value the caller supplies rather than on the shape of the code. A reader who
// takes the stronger reading will believe a check is exercised when the surface is
// only positioned where that entry could reach it.
//
// The coarseness is not a concession to what this read can see. For the editor's
// inspection path the true answer *is* "depends on a value the caller sets": the
// pipeline stops before the render machinery when the caller says so, so a
// declaration naming one stage for that surface would be wrong half the time.
// Reachability is the strongest claim the code actually supports, which is why it
// is the claim made rather than a weaker one dressed up.
//
// Two values are therefore reported in their own right rather than smoothed away.
// A stage reachable only past a cut the caller controls is reported as subject to
// that value, naming it. A surface that reaches no stage at all is reported as
// reaching none — not omitted, because an omitted surface reads as one nobody
// looked at.
//
// # What a surface and an entry are, derived rather than listed
//
// A **stage site** is a production function that calls a framework traversal
// entry; its stages are the stages of the checks named there.
//
// The **pipeline package** is the one package declaring a stage site from which
// every stage any production path reaches is itself reachable. It is asserted
// unique: two candidates would mean the ladder has no single home and the derived
// entry set would depend on which was picked.
//
// An **entry** is an exported function of that package that reaches a stage site,
// which selects the pipeline's own ladder without listing its rungs. Beside them
// sit the exported functions of the packages that call an entry, kept where they
// reach the loader — the package holding the earliest stage's site. That second
// tier is what makes a surface reaching the loader but no stage a row reading "no
// stage reached" rather than an absence.
//
// A **surface** is a production call to one of those entries from outside the
// entry's own package, attributed to the function whose body contains it.
//
// # The bound on the reach itself
//
// The reach follows resolved calls only. A call through a name the same body binds
// to a function is resolved to the functions that body names, which keeps a call
// attributed to the operation the source names where a body holds that operation
// in a variable. A call through any other value — a method on a receiver, a
// function held in a field, one supplied as a parameter, one reached through an
// interface, one through a name bound to a closure — is followed nowhere, so a
// reported reach is a lower bound: a surface may touch a stage this read does not
// attribute to it, and never fewer.

// stageSite is one production function invoking rules, and what it invokes.
type stageSite struct {
	// Func is the containing function's declaration key.
	Func string

	// Check is the check named at the call.
	Check string

	// Stage is the stage the check's declared view fixes.
	Stage string
}

// callerRow is one surface, the entry it calls, and what that entry reaches.
type callerRow struct {
	// File is the calling file, relative to the module root.
	File string

	// Func is the calling function's own name.
	Func string

	// Entry is the entry called.
	Entry string

	// EntryPackage is the directory declaring the entry.
	EntryPackage string

	// Stages are the stages reachable from the entry, in chain order.
	Stages []string

	// ConditionalStages are the reachable stages a value the caller supplies
	// decides, in chain order. They are a subset of Stages.
	ConditionalStages []string

	// Gates are the caller-supplied fields those stages depend on, sorted.
	Gates []string

	// Checks counts the invoked checks whose stage site is reachable from the
	// entry.
	Checks int

	// FirmChecks counts the same without crossing a cut the caller controls.
	FirmChecks int
}

// callerCoverage is what the caller read establishes.
type callerCoverage struct {
	// PipelinePackage is the derived home of the entry ladder.
	PipelinePackage string

	// LoaderPackage is the package holding the earliest stage's site.
	LoaderPackage string

	// Rows are the surfaces, ordered by file, then function, then entry.
	Rows []callerRow

	// Sites are the stage sites, ordered by function then check.
	Sites []stageSite

	// InvokedChecks is how many invoked checks the read found a stage site for.
	// It is the denominator every row's count is read against.
	InvokedChecks int

	// UnresolvedCalls counts the calls the read could not resolve to a
	// declaration, which is why a reach is a lower bound. Carried so the bound is
	// reportable as a figure rather than only as a sentence.
	UnresolvedCalls int
}

// readCallerCoverage derives the caller coverage once per process.
//
// 🔴 Every figure it returns answers "reachable from the entry the surface calls"
// and none of them answers "reached by that call". An entry reaches several
// stages, and whether one call gets to the deepest of them can rest on a value the
// caller supplies — so a row says where a surface is positioned, never that a
// check ran. The coarseness is the honest answer rather than a limit of the read:
// where a surface's depth is set by a value its caller passes, "it depends on that
// value" is what is true, and a single stage recorded for it would be wrong
// whenever the value went the other way.
func readCallerCoverage(c chain) (*callerCoverage, error) {
	coverageOnce.Do(func() {
		coverageRead, coverageErr = deriveCallerCoverage(c)
	})
	return coverageRead, coverageErr
}

var (
	coverageOnce sync.Once
	coverageRead *callerCoverage
	coverageErr  error
)

func deriveCallerCoverage(c chain) (*callerCoverage, error) {
	inv, err := readInvocationState()
	if err != nil {
		return nil, err
	}
	graph, err := readCallState()
	if err != nil {
		return nil, err
	}

	stageOfView := make(map[string]string, len(c.order))
	for _, s := range c.order {
		stageOfView[viewName(s.Name)] = s.Name
	}
	stageOfCheck := map[string]string{}
	invoked := map[string]bool{}
	for _, ck := range inv.Checks {
		if stage := stageOfView[ck.View]; stage != "" {
			stageOfCheck[ck.Name] = stage
		}
		if ck.Invoked {
			invoked[ck.Name] = true
		}
	}

	cov := &callerCoverage{}

	// The stage sites, indexed so a reach can be carried as a bit set rather than
	// as a set of names copied at every union.
	indexOf := map[string]int{}
	for _, key := range graph.Order {
		node := graph.Nodes[key]
		for _, t := range node.Traversals {
			stage := stageOfCheck[t.Check]
			if t.Check == "" || stage == "" {
				continue
			}
			id := key + "\x00" + t.Check
			if _, seen := indexOf[id]; seen {
				continue
			}
			indexOf[id] = len(cov.Sites)
			cov.Sites = append(cov.Sites, stageSite{Func: key, Check: t.Check, Stage: stage})
		}
		for _, e := range node.Calls {
			if e.Target == "" {
				cov.UnresolvedCalls++
			}
		}
	}
	if len(cov.Sites) == 0 {
		return nil, fmt.Errorf(
			"the caller read found no function invoking a declared check, so every surface would " +
				"report reaching nothing for want of a site rather than for want of a path")
	}
	distinct := map[string]bool{}
	for _, site := range cov.Sites {
		if invoked[site.Check] {
			distinct[site.Check] = true
		}
	}
	cov.InvokedChecks = len(distinct)

	// Two reaches per function: one ignoring every cut, one crossing none. Their
	// difference is exactly what a value the caller supplies decides.
	full := reachFixedPoint(graph, cov.Sites, indexOf, false)
	firm := reachFixedPoint(graph, cov.Sites, indexOf, true)
	gates := gateFixedPoint(graph, full, firm)

	cov.PipelinePackage, err = derivePipelinePackage(graph, cov.Sites, full, c)
	if err != nil {
		return nil, err
	}
	cov.LoaderPackage, err = deriveLoaderPackage(cov.Sites, c)
	if err != nil {
		return nil, err
	}

	entries := map[string]bool{}
	for _, key := range graph.Order {
		node := graph.Nodes[key]
		if node.Dir == cov.PipelinePackage && node.Exported && !full[key].empty() {
			entries[key] = true
		}
	}
	if len(entries) == 0 {
		return nil, fmt.Errorf(
			"the caller read found no exported entry in %s reaching a stage site, so it has "+
				"measured its own derivation of the pipeline package rather than the tree",
			cov.PipelinePackage)
	}

	// The second tier: a package that calls an entry is a boundary a surface
	// reaches the compiler through, so its own exported functions are entries too
	// where they reach the loader. Without this tier a surface that resolves
	// documents and runs no rule at all is invisible rather than reported as
	// reaching nothing.
	boundary := map[string]bool{}
	for _, key := range graph.Order {
		node := graph.Nodes[key]
		if node.Dir == cov.PipelinePackage {
			continue
		}
		for _, e := range node.Calls {
			if entries[e.Target] {
				boundary[node.Dir] = true
				break
			}
		}
	}
	reachesLoader := loaderReachFixedPoint(graph, cov.LoaderPackage)
	for _, key := range graph.Order {
		node := graph.Nodes[key]
		if boundary[node.Dir] && node.Exported && reachesLoader[key] {
			entries[key] = true
		}
	}

	for _, key := range graph.Order {
		node := graph.Nodes[key]
		for _, e := range node.Calls {
			if !entries[e.Target] {
				continue
			}
			target := graph.Nodes[e.Target]
			if target == nil || target.Dir == node.Dir {
				continue
			}
			row := callerRow{
				File:         node.File,
				Func:         node.Name,
				Entry:        target.Name,
				EntryPackage: target.Dir,
			}
			fullSet, firmSet := full[e.Target], firm[e.Target]
			row.Stages = stagesOf(fullSet, cov.Sites, c)
			row.ConditionalStages = stagesOf(fullSet.without(firmSet), cov.Sites, c)
			row.Checks = countInvoked(fullSet, cov.Sites, invoked)
			row.FirmChecks = countInvoked(firmSet, cov.Sites, invoked)
			if len(row.ConditionalStages) > 0 {
				row.Gates = sorted(keysOf(gates[e.Target]))
			}
			cov.Rows = append(cov.Rows, row)
		}
	}
	// One surface may call one entry more than once; the row says which stages the
	// entry reaches, which is the same answer for each call, so the duplicate is
	// collapsed rather than printed twice.
	sort.SliceStable(cov.Rows, func(i, j int) bool {
		a, b := cov.Rows[i], cov.Rows[j]
		switch {
		case a.File != b.File:
			return a.File < b.File
		case a.Func != b.Func:
			return a.Func < b.Func
		default:
			return a.Entry < b.Entry
		}
	})
	deduped := cov.Rows[:0]
	for i, r := range cov.Rows {
		if i > 0 {
			p := cov.Rows[i-1]
			if p.File == r.File && p.Func == r.Func && p.Entry == r.Entry {
				continue
			}
		}
		deduped = append(deduped, r)
	}
	cov.Rows = deduped
	if len(cov.Rows) == 0 {
		return nil, fmt.Errorf(
			"the caller read found no production call into %s from outside it, so a report that no "+
				"surface reaches the compiler would describe the instrument rather than the tree",
			cov.PipelinePackage)
	}
	sort.Slice(cov.Sites, func(i, j int) bool {
		if cov.Sites[i].Func != cov.Sites[j].Func {
			return cov.Sites[i].Func < cov.Sites[j].Func
		}
		return cov.Sites[i].Check < cov.Sites[j].Check
	})
	return cov, nil
}

// derivePipelinePackage is the one package declaring a stage site from which
// every stage any production path reaches is itself reachable.
//
// It is derived rather than named so that moving the ladder moves this answer with
// it. Uniqueness is asserted: with two candidates the entry set would depend on
// which one was picked, which is the hand-wired choice this whole read replaces.
func derivePipelinePackage(graph *callState, sites []stageSite, full map[string]bitset, c chain) (string, error) {
	everyStage := map[string]bool{}
	for _, s := range sites {
		everyStage[s.Stage] = true
	}
	declaring := map[string]bool{}
	for _, s := range sites {
		if node := graph.Nodes[s.Func]; node != nil {
			declaring[node.Dir] = true
		}
	}
	reachOf := map[string]bitset{}
	for _, key := range graph.Order {
		node := graph.Nodes[key]
		if !declaring[node.Dir] {
			continue
		}
		reachOf[node.Dir] = reachOf[node.Dir].union(full[key])
	}
	var candidates []string
	for dir := range declaring {
		stages := map[string]bool{}
		for _, name := range stagesOf(reachOf[dir], sites, c) {
			stages[name] = true
		}
		if len(stages) == len(everyStage) {
			candidates = append(candidates, dir)
		}
	}
	switch len(candidates) {
	case 1:
		return candidates[0], nil
	case 0:
		return "", fmt.Errorf(
			"the caller read found no package invoking rules from which every invoked stage is "+
				"reachable; the %d package(s) that invoke rules (%s) each reach only part of the "+
				"ladder, so there is no single home for it to derive entries from",
			len(declaring), strings.Join(sorted(keysOf(declaring)), ", "))
	default:
		return "", fmt.Errorf(
			"the caller read found %d packages from which every invoked stage is reachable (%s); the "+
				"entry set would depend on which was picked, which is the hand-wired choice this "+
				"derivation exists to remove",
			len(candidates), strings.Join(sorted(candidates), ", "))
	}
}

// deriveLoaderPackage is the package holding the site of the earliest stage any
// production path invokes rules at.
func deriveLoaderPackage(sites []stageSite, c chain) (string, error) {
	for _, s := range c.order {
		for _, site := range sites {
			if site.Stage != s.Name {
				continue
			}
			if dir := targetDir(site.Func); dir != "" {
				return dir, nil
			}
		}
	}
	return "", fmt.Errorf(
		"the caller read found no stage site for any of the %d stages in the chain, so it has no "+
			"loader package to measure a surface's reach into", len(c.order))
}

// stagesOf is the stages a reach touches, in chain order.
func stagesOf(set bitset, sites []stageSite, c chain) []string {
	present := map[string]bool{}
	for i, s := range sites {
		if set.has(i) {
			present[s.Stage] = true
		}
	}
	var out []string
	for _, s := range c.order {
		if present[s.Name] {
			out = append(out, s.Name)
		}
	}
	return out
}

// countInvoked is how many invoked checks a reach touches.
func countInvoked(set bitset, sites []stageSite, invoked map[string]bool) int {
	seen := map[string]bool{}
	for i, s := range sites {
		if set.has(i) && invoked[s.Check] && !seen[s.Check] {
			seen[s.Check] = true
		}
	}
	return len(seen)
}

// reachFixedPoint is, per function, the stage sites reachable from it.
//
// It is iterated to a fixed point rather than walked recursively because the call
// graph carries cycles, and a recursive walk over one either loops or has to
// choose a truncation, which would make the answer depend on where the walk
// started.
//
// With respectCuts set, an edge positioned at or after the function's earliest
// cut is not followed: that reach is what the function delivers whatever its
// caller supplies.
func reachFixedPoint(graph *callState, sites []stageSite, indexOf map[string]int, respectCuts bool) map[string]bitset {
	words := (len(sites) + 63) / 64
	own := make(map[string]bitset, len(graph.Nodes))
	for _, key := range graph.Order {
		node := graph.Nodes[key]
		limit := cutLimit(node, respectCuts)
		set := newBitset(words)
		for _, t := range node.Traversals {
			if t.Pos >= limit {
				continue
			}
			if i, ok := indexOf[key+"\x00"+t.Check]; ok {
				set.set(i)
			}
		}
		own[key] = set
	}

	reach := make(map[string]bitset, len(graph.Nodes))
	for key, set := range own {
		reach[key] = set.clone()
	}
	for changed := true; changed; {
		changed = false
		for _, key := range graph.Order {
			node := graph.Nodes[key]
			limit := cutLimit(node, respectCuts)
			set := reach[key]
			for _, e := range node.Calls {
				if e.Target == "" || e.Pos >= limit {
					continue
				}
				if target, ok := reach[e.Target]; ok && set.absorb(target) {
					changed = true
				}
			}
		}
	}
	return reach
}

// gateFixedPoint is, per function, the caller-supplied fields its conditional
// reach depends on.
//
// A gate propagates only along edges a function takes whatever its caller
// supplies, and only from a function whose own reach is in fact conditional — so a
// function reaching every stage firmly inherits no gate from anything it calls.
func gateFixedPoint(graph *callState, full, firm map[string]bitset) map[string]map[string]bool {
	gates := make(map[string]map[string]bool, len(graph.Nodes))
	for _, key := range graph.Order {
		node := graph.Nodes[key]
		set := map[string]bool{}
		if !full[key].equals(firm[key]) {
			for _, cut := range node.Cuts {
				set[cut.Field] = true
			}
		}
		gates[key] = set
	}
	for changed := true; changed; {
		changed = false
		for _, key := range graph.Order {
			node := graph.Nodes[key]
			if full[key].equals(firm[key]) {
				continue
			}
			limit := cutLimit(node, true)
			for _, e := range node.Calls {
				if e.Target == "" || e.Pos >= limit {
					continue
				}
				for field := range gates[e.Target] {
					if !gates[key][field] {
						gates[key][field] = true
						changed = true
					}
				}
			}
		}
	}
	return gates
}

// loaderReachFixedPoint reports, per function, whether it reaches a declaration
// in the loader package.
func loaderReachFixedPoint(graph *callState, loaderDir string) map[string]bool {
	reach := make(map[string]bool, len(graph.Nodes))
	for _, key := range graph.Order {
		if graph.Nodes[key].Dir == loaderDir {
			reach[key] = true
		}
	}
	for changed := true; changed; {
		changed = false
		for _, key := range graph.Order {
			if reach[key] {
				continue
			}
			for _, e := range graph.Nodes[key].Calls {
				if e.Target != "" && (reach[e.Target] || targetDir(e.Target) == loaderDir) {
					reach[key] = true
					changed = true
					break
				}
			}
		}
	}
	return reach
}

// targetDir is the package directory a declaration key names.
func targetDir(key string) string {
	if i := strings.Index(key, "::"); i > 0 {
		return key[:i]
	}
	return ""
}

// cutLimit is the position past which a function's calls are conditional on a
// value its caller supplied, or the end of the file set when they are not.
func cutLimit(node *funcNode, respectCuts bool) token.Pos {
	if !respectCuts || len(node.Cuts) == 0 {
		return token.Pos(1) << 62
	}
	limit := node.Cuts[0].Pos
	for _, cut := range node.Cuts[1:] {
		if cut.Pos < limit {
			limit = cut.Pos
		}
	}
	return limit
}

// bitset is a set of stage-site indices.
type bitset []uint64

func newBitset(words int) bitset { return make(bitset, words) }

func (b bitset) clone() bitset {
	out := make(bitset, len(b))
	copy(out, b)
	return out
}

func (b bitset) set(i int) { b[i/64] |= 1 << (i % 64) }

func (b bitset) has(i int) bool {
	if i/64 >= len(b) {
		return false
	}
	return b[i/64]&(1<<(i%64)) != 0
}

func (b bitset) empty() bool {
	for _, w := range b {
		if w != 0 {
			return false
		}
	}
	return true
}

// absorb adds every member of other, reporting whether that changed b.
func (b bitset) absorb(other bitset) bool {
	changed := false
	for i := range b {
		if i >= len(other) {
			break
		}
		merged := b[i] | other[i]
		if merged != b[i] {
			b[i] = merged
			changed = true
		}
	}
	return changed
}

func (b bitset) union(other bitset) bitset {
	if len(b) == 0 {
		return other.clone()
	}
	out := b.clone()
	out.absorb(other)
	return out
}

func (b bitset) without(other bitset) bitset {
	out := b.clone()
	for i := range out {
		if i < len(other) {
			out[i] &^= other[i]
		}
	}
	return out
}

func (b bitset) equals(other bitset) bool {
	for i := range b {
		var o uint64
		if i < len(other) {
			o = other[i]
		}
		if b[i] != o {
			return false
		}
	}
	return true
}

// writeCallerCoverageSection renders the caller-coverage section of the placement
// table.
//
// 🔴 The section states the reachable-from bound in its own prose and in its own
// column heading, not only in this comment. A number presented without its
// predicate is how the stronger "reached by" reading gets assumed, and a reader who
// assumes it will believe a check is exercised when the surface is only positioned
// where that entry could reach it.
func writeCallerCoverageSection(b *strings.Builder, c chain) error {
	cov, err := readCallerCoverage(c)
	if err != nil {
		return err
	}

	b.WriteString("## Which stages each surface reaches\n\n")
	b.WriteString("Derived by parsing this module's own source: the functions that invoke rules,\n")
	b.WriteString("the entries the compiler exposes above them, and every production call into one\n")
	b.WriteString("of those entries. Nothing here is kept in step with the tree by hand.\n\n")

	b.WriteString("🔴 **Every figure below answers *reachable from*, and none of them answers\n")
	b.WriteString("*reached by*.** An entry reaches several stages, and whether one call gets to\n")
	b.WriteString("the deepest of them can rest on a value the caller supplies rather than on the\n")
	b.WriteString("shape of the code. So a row says where a surface is positioned, never that a\n")
	b.WriteString("check ran on any particular build.\n\n")
	b.WriteString("**That coarseness is the honest answer rather than a limit of this read.** For a\n")
	b.WriteString("surface whose depth is set by a value its caller passes, the true answer *is*\n")
	b.WriteString("\"it depends on that value\" — a single stage recorded for it would be wrong\n")
	b.WriteString("whenever the value went the other way. Reachability is the strongest claim the\n")
	b.WriteString("code supports, so it is the claim made.\n\n")

	fmt.Fprintf(b, "| Surface | Calling function | Entry | Stages reachable from the entry | Invoked checks reachable |\n")
	b.WriteString("|---|---|---|---|---|\n")
	for _, r := range cov.Rows {
		fmt.Fprintf(b, "| `%s` | `%s` | `%s` (`%s`) | %s | %s |\n",
			r.File, r.Func, r.Entry, r.EntryPackage,
			reachableStageCell(r), reachableCheckCell(r, cov.InvokedChecks))
	}
	fmt.Fprintf(b, "\nTotal: **%d surface(s)** reaching the compiler, against **%d** invoked checks\n"+
		"the read found a rule site for.\n\n", len(cov.Rows), cov.InvokedChecks)

	b.WriteString("The criterion, so the rows are readable rather than merely printed. A **rule\n")
	b.WriteString("site** is a production function that calls one of the framework's traversal\n")
	b.WriteString("entries; its stages are the stages of the checks named there. The **pipeline\n")
	b.WriteString("package** is the one package declaring a rule site from which every invoked\n")
	fmt.Fprintf(b, "stage is itself reachable — derived, and asserted unique, and here that is\n"+
		"`%s`.\n", cov.PipelinePackage)
	b.WriteString("An **entry** is an exported function of that package reaching a rule site, which\n")
	b.WriteString("selects the ladder without listing its rungs; beside those sit the exported\n")
	b.WriteString("functions of the packages that call an entry, kept where they reach the package\n")
	fmt.Fprintf(b, "holding the earliest stage's rule site, which is\n`%s`.\n", cov.LoaderPackage)
	b.WriteString("A **surface** is a production call to one of those entries from outside the\n")
	b.WriteString("entry's own package, attributed to the function whose body contains it rather\n")
	b.WriteString("than to the nearest declaration above it.\n\n")

	b.WriteString("Three bounds, stated rather than left for a reader to discover. **A reach is a\n")
	b.WriteString("lower bound.** It follows calls this read can resolve — a bare call into the\n")
	b.WriteString("calling file's own package, a package-qualified call resolved through that\n")
	b.WriteString("file's imports, and a call through a name the same body binds to one of those,\n")
	b.WriteString("which is what keeps a surface attributed to the operation its own source names\n")
	b.WriteString("when it holds that operation in a variable. It follows no call through any\n")
	b.WriteString("other value, so a method on a receiver, a function held in a field, one\n")
	b.WriteString("supplied as a parameter and one reached through an interface are each followed\n")
	b.WriteString("nowhere; and a name a body binds to such a value as well as to a function\n")
	b.WriteString("carries that call as unresolved beside the function it names, rather than\n")
	b.WriteString("resting on the binding this read could see. So a surface may touch a stage no\n")
	b.WriteString("row here attributes to it, and never fewer. How many calls the read left\n")
	b.WriteString("unresolved is printed by the generator when it runs and is deliberately absent\n")
	b.WriteString("here: it moves with every file added anywhere in the module, and a drift-gated\n")
	b.WriteString("page that churns on unrelated edits trains its readers to regenerate without\n")
	b.WriteString("reading.\n\n")
	b.WriteString("**A stage a value decides is reported as such, naming the value**, because a row\n")
	b.WriteString("claiming it plainly would assert a depth the code does not fix. **And a surface\n")
	b.WriteString("that reaches no stage is a row reading so, never an omission** — an omitted\n")
	b.WriteString("surface reads as one nobody looked at, which is the opposite of what it means.\n\n")
	b.WriteString("The read walks past test files, test data, vendored source, any directory\n")
	b.WriteString("carrying its own module, and any package every importer of which is a test file.\n")
	b.WriteString("That last exclusion is what keeps a test harness — whose files carry no test\n")
	b.WriteString("suffix, because a test could not import them if they did — out of the surface\n")
	b.WriteString("listing. It is drawn from the import graph rather than from a name or a path\n")
	b.WriteString("list: a name rule would exclude a production package on a spelling, and a path\n")
	b.WriteString("list is a second copy of what the imports already state. A package nothing\n")
	b.WriteString("imports at all is deliberately kept, so a command's own entry package stays in;\n")
	b.WriteString("what the exclusion would wrongly drop is a production package nothing has\n")
	b.WriteString("imported yet outside a test, or one reached by a mechanism no import records.\n\n")
	b.WriteString("The read refuses to report at all on a corpus it did not reach: no production\n")
	b.WriteString("file, no function invoking a rule, no single pipeline package, no entry, or no\n")
	b.WriteString("surface halts generation rather than rendering a clean result. So a zero above\n")
	b.WriteString("is a statement about the tree and not about the instrument.\n\n")

	return nil
}

// reachableStageCell is a row's stage cell: the stages in chain order, with the
// ones a caller-supplied value decides marked and that value named.
func reachableStageCell(r callerRow) string {
	if len(r.Stages) == 0 {
		return "**no stage reached**"
	}
	conditional := map[string]bool{}
	for _, s := range r.ConditionalStages {
		conditional[s] = true
	}
	cells := make([]string, 0, len(r.Stages))
	for _, s := range r.Stages {
		if conditional[s] {
			cells = append(cells, "`"+s+"` (option-dependent)")
			continue
		}
		cells = append(cells, "`"+s+"`")
	}
	out := strings.Join(cells, ", ")
	if len(r.Gates) > 0 {
		quoted := make([]string, len(r.Gates))
		for i, g := range r.Gates {
			quoted[i] = "`" + g + "`"
		}
		out += " — the option-dependent stage(s) rest on " + strings.Join(quoted, ", ")
	}
	return out
}

// reachableCheckCell is a row's count cell, carrying both the reach and the part
// of it no caller-supplied value can withhold.
func reachableCheckCell(r callerRow, total int) string {
	if r.Checks == 0 {
		return "**none**"
	}
	if r.FirmChecks == r.Checks {
		return fmt.Sprintf("%d of %d", r.Checks, total)
	}
	return fmt.Sprintf("%d of %d (%d whatever the caller sets)", r.Checks, total, r.FirmChecks)
}

// reportCallerInstrument prints what the caller read reached, on every run and
// whatever the target.
//
// These are figures about the instrument rather than about the tree's rules, so
// they are printed rather than rendered into the drift-gated page, for the same
// reason the invocation read's are: a page carrying them would go stale on every
// file added anywhere, and a page that fails its gate for unrelated reasons
// teaches its readers to regenerate without reading.
func reportCallerInstrument(w *os.File) {
	c, err := deriveChain()
	if err != nil {
		return
	}
	graph, gerr := readCallState()
	if gerr != nil {
		fmt.Fprintf(w, "factgen: the call read did not complete: %v\n", gerr)
		return
	}
	m, merr := readModuleSource()
	if merr != nil {
		fmt.Fprintf(w, "factgen: the module read did not complete: %v\n", merr)
		return
	}
	cov, cerr := readCallerCoverage(c)
	if cerr != nil {
		fmt.Fprintf(w, "factgen: the caller read did not complete: %v\n", cerr)
		return
	}
	fmt.Fprintf(w,
		"factgen: caller read — %d production function(s), %d unresolved call(s), "+
			"%d rule site(s), pipeline package %s, loader package %s, %d surface(s)\n",
		len(graph.Nodes), cov.UnresolvedCalls, len(cov.Sites),
		cov.PipelinePackage, cov.LoaderPackage, len(cov.Rows))
	if len(m.TestOnlyDirs) > 0 {
		fmt.Fprintf(w, "factgen: %d package(s) excluded as test support (nothing outside a test imports them): %s\n",
			len(m.TestOnlyDirs), strings.Join(m.TestOnlyDirs, ", "))
	}
}
