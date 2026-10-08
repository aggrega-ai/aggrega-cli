// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

// Guards the two-home rule list: the rule identifiers this package dispatches
// and the identifiers the publishing-linter guest answers to. The two lists live
// in different languages and cannot import each other, so they were kept in step
// by a comment. A comment is unobservable to every gate: a rule registered on one
// side only produced either a dispatched identifier the guest reports as
// unimplemented, or a guest rule nothing ever asks for. This reads the guest's
// dispatch table as text and compares the sets.
//
// A second axis covers the same failure from the module side: a rule module the
// guest carries that no dispatch arm routes to. The identifier comparison cannot
// see that one — a module authored with neither an arm nor a host identifier
// leaves both lists lacking the rule and therefore agreeing — and neither can
// the Rust compiler, because a module declared public in a library crate is
// public API, so nothing about it is unused.

package lintops

import (
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// guestDispatchSource is the guest's rule-dispatch table.
const guestDispatchSource = "../../plugins/publishing-linter/src/lib.rs"

// ruleModuleDir is the guest's rule-module directory. Every module in it that
// exposes the classifier entry point is expected to be reachable from the
// dispatch table in guestDispatchSource.
const ruleModuleDir = "../../plugins/publishing-linter/src/rules"

// ruleModuleEntryPoint is the signature prefix that tells a rule module apart
// from a helper sharing the same directory: a rule classifies, a helper (a
// traversal routine several rules call) exposes no classifier. The criterion is
// textual, not semantic — a helper that happened to expose a function of this
// name would be read as a rule.
const ruleModuleEntryPoint = "pub fn classify("

// guestDispatchArm matches one arm of the guest's dispatch table: a quoted rule
// identifier followed by the match arrow.
var guestDispatchArm = regexp.MustCompile(`"([a-z0-9-]+)"\s*=>`)

// guestDispatchArmTarget matches one arm together with the module path its
// classifier call names: the quoted rule identifier, the match arrow, the
// optional opening brace of a block-bodied arm, then the path ending in the
// classifier call. The module path is the only correspondence available, because
// rule identifiers and module names diverge — one rule is dispatched under an
// identifier that is not its module's name — so deriving one from the other by
// name transform would be wrong.
var guestDispatchArmTarget = regexp.MustCompile(`"([a-z0-9-]+)"\s*=>\s*\{?\s*((?:[a-z0-9_]+::)*[a-z0-9_]+)::classify`)

// dispatchArmsOutsideRuleModuleDir names, individually and with its reason, each
// dispatch arm whose classifier lives outside ruleModuleDir. Naming each one
// keeps the next such arm visible: a blanket exclusion would absorb it in
// silence. A second entry here is itself a signal — the exception has become a
// class, and a class needs a stated rule for what qualifies rather than a list.
var dispatchArmsOutsideRuleModuleDir = map[string]string{
	"schema-evolution": "classified by the sibling rubric module rather than by a module under the rule-module directory",
}

// TestRuleIDsMatchGuestDispatchTable pins the rule set this package dispatches
// against the set the guest implements. The full set is the pre-publish entity
// list (default mode omits only the rule that needs previously-published bytes)
// together with the rules that read a whole Release, which no entity list holds,
// so that union is the set compared.
//
// It then pins the guest's rule modules against the same dispatch table, in one
// direction only: every module exposing the classifier entry point has an arm.
// The reverse does not hold and must not be asserted — an arm may legitimately
// classify from outside the rule-module directory, and those arms are named
// individually in dispatchArmsOutsideRuleModuleDir.
func TestRuleIDsMatchGuestDispatchTable(t *testing.T) {
	t.Parallel()
	raw, err := os.ReadFile(filepath.FromSlash(guestDispatchSource))
	if err != nil {
		t.Skipf("guest source not reachable at %s (standalone checkout): %v", guestDispatchSource, err)
	}
	source := string(raw)

	entry := strings.Index(source, "pub fn lint_rules(")
	if entry < 0 {
		t.Fatalf("the guest's dispatch entry point was not found in %s; the guest's shape moved and this guard needs re-aiming", guestDispatchSource)
	}
	guest := map[string]bool{}
	for _, m := range guestDispatchArm.FindAllStringSubmatch(source[entry:], -1) {
		guest[m[1]] = true
	}
	if len(guest) == 0 {
		t.Fatalf("no dispatch arms parsed out of %s; the guard would pass vacuously", guestDispatchSource)
	}

	host := map[string]bool{}
	for _, id := range append(defaultRuleIDs(true), releaseRuleIDs()...) {
		host[id] = true
	}

	var hostOnly, guestOnly []string
	for id := range host {
		if !guest[id] {
			hostOnly = append(hostOnly, id)
		}
	}
	for id := range guest {
		if !host[id] {
			guestOnly = append(guestOnly, id)
		}
	}
	sort.Strings(hostOnly)
	sort.Strings(guestOnly)

	if len(hostOnly) > 0 {
		t.Errorf("dispatched but not implemented by the guest: %v — the guest answers an unknown identifier with an unimplemented finding, so the rule silently never runs", hostOnly)
	}
	if len(guestOnly) > 0 {
		t.Errorf("implemented by the guest but never dispatched: %v — add the identifier to defaultRuleIDs or the rule is dead", guestOnly)
	}

	// Second axis: every rule module the guest carries is routed to by an arm.
	entries, err := os.ReadDir(filepath.FromSlash(ruleModuleDir))
	if err != nil {
		t.Fatalf("rule modules not reachable at %s: %v", ruleModuleDir, err)
	}
	var ruleModules []string
	for _, e := range entries {
		if e.IsDir() {
			t.Fatalf("%s/%s is a directory; this reads flat module files only, and widening it is the precondition for a nested rule module being visible here at all", ruleModuleDir, e.Name())
		}
		// The module list itself is never a rule. Skipping it by name is
		// deliberate: it carries no classifier either, so the entry-point
		// criterion alone would exclude it only by coincidence.
		if e.Name() == "mod.rs" || !strings.HasSuffix(e.Name(), ".rs") {
			continue
		}
		body, readErr := os.ReadFile(filepath.Join(filepath.FromSlash(ruleModuleDir), e.Name()))
		if readErr != nil {
			t.Fatalf("rule module %s not readable: %v", e.Name(), readErr)
		}
		if !strings.Contains(string(body), ruleModuleEntryPoint) {
			continue
		}
		ruleModules = append(ruleModules, strings.TrimSuffix(e.Name(), ".rs"))
	}
	if len(ruleModules) == 0 {
		t.Fatalf("no module under %s exposes %q; nothing was globbed and this axis would pass vacuously", ruleModuleDir, ruleModuleEntryPoint)
	}

	modulePathPrefix := path.Base(ruleModuleDir) + "::"
	dispatched := map[string]bool{}
	armTarget := map[string]string{}
	for _, m := range guestDispatchArmTarget.FindAllStringSubmatch(source[entry:], -1) {
		armTarget[m[1]] = m[2]
		if mod := strings.TrimPrefix(m[2], modulePathPrefix); mod != m[2] {
			dispatched[mod] = true
		}
	}
	if len(dispatched) == 0 {
		t.Fatalf("no arm in %s names a module under %s; the dispatch shape moved and this axis would pass vacuously", guestDispatchSource, ruleModuleDir)
	}

	var unreadable, unnamedOutside, staleException, undispatched []string
	// An arm whose classifier target cannot be read drops out of the two checks
	// below, so it is reported rather than skipped.
	for id := range guest {
		if _, ok := armTarget[id]; !ok {
			unreadable = append(unreadable, id)
		}
	}
	for id, target := range armTarget {
		if strings.HasPrefix(target, modulePathPrefix) {
			continue
		}
		if _, named := dispatchArmsOutsideRuleModuleDir[id]; !named {
			unnamedOutside = append(unnamedOutside, id+" → "+target)
		}
	}
	for id, reason := range dispatchArmsOutsideRuleModuleDir {
		if _, ok := armTarget[id]; !ok {
			staleException = append(staleException, id+" ("+reason+")")
		}
	}
	for _, mod := range ruleModules {
		if !dispatched[mod] {
			undispatched = append(undispatched, mod)
		}
	}
	sort.Strings(unreadable)
	sort.Strings(unnamedOutside)
	sort.Strings(staleException)
	sort.Strings(undispatched)

	if len(unreadable) > 0 {
		t.Errorf("dispatch arms whose classifier target could not be read: %v — this axis reads the module path an arm names, so an unread target hides that arm from it", unreadable)
	}
	if len(unnamedOutside) > 0 {
		t.Errorf("dispatch arms classifying outside %s without being named as exceptions: %v — name each one with its reason in dispatchArmsOutsideRuleModuleDir, and if that list is now a class rather than a list, state the rule for what qualifies", ruleModuleDir, unnamedOutside)
	}
	if len(staleException) > 0 {
		t.Errorf("named exceptions with no dispatch arm: %v — the arm was renamed or removed and the exception outlived it", staleException)
	}
	if len(undispatched) > 0 {
		t.Errorf("rule modules exposing %q that no arm in %s routes to: %v — a module declared public in a library crate is public API, so the compiler reports nothing and the rule simply never runs", ruleModuleEntryPoint, guestDispatchSource, undispatched)
	}
}
