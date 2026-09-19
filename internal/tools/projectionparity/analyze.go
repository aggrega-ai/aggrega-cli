// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package projectionparity

import (
	"fmt"
	"go/ast"
	"go/token"
	"regexp"
	"slices"
	"strings"
)

// Kind is what a finding says. Values are the identifiers a report prints and a
// driver matches on.
type Kind string

const (
	// DroppedField is the defect this package exists to find: one surface
	// carries a field the shared computation produced and another, which calls
	// the same computation, does not, with no reason stated where it drops it.
	DroppedField Kind = "dropped-field"

	// ResultDiscarded is a surface that calls a shared computation and throws
	// its structured result away, keeping only the error. Every fact the
	// computation produced is dropped at once, so it is reported as one finding
	// about the result rather than as one per field.
	ResultDiscarded Kind = "result-discarded"

	// UnreadableSite is a surface whose use of a shared result this read cannot
	// follow, so no parity verdict is drawn for that surface and that result.
	// The value escapes to somewhere the reading does not go.
	UnreadableSite Kind = "unreadable-site"

	// UnreadableCore is a shared computation whose declaration or result struct
	// could not be read, so the field set it hands its callers is unknown and
	// nothing may be concluded from a surface's reads of it.
	UnreadableCore Kind = "unreadable-core"

	// ExemptionUnsupported is a stated exemption the site does not bear out: it
	// names a field the surface does read, or one no shared result at the site
	// carries, or it states no reason, or it sits away from any projection of
	// the result it names. An exemption for a whole result adds two ways to be
	// unsupported, both of which would have it clear more than a field exemption
	// could: the surface never discarded that result, or it does project fields
	// from it and so does not omit the whole of it.
	//
	// Three more concern the name itself rather than the claim: a bare result
	// name two packages both declare, which does not say which of them is meant;
	// a package-qualified name whose package declares no shared result of that
	// name; and a marker whose shape names neither form. Each is reported rather
	// than passed over, because a marker nothing recognises is a reason an author
	// believes they gave.
	//
	// The declaration is the one way a dropped field is cleared here, so it is
	// read back against the site rather than taken on trust. A declaration
	// nothing checks is one a later author applies by habit, and at that point a
	// surface dropping a fact for a reason is indistinguishable from one that
	// dropped it by accident.
	ExemptionUnsupported Kind = "exemption-unsupported"
)

// Finding is one thing read about one surface's projection of one shared result.
//
// Kind is what the finding says, and File and Line are where the surface's
// projection sits. Surface names the surface, Core the shared computation as
// package directory and function name, and Result the struct it hands back.
// ResultPackage is the package directory declaring Result, which is what
// separates two results of the same name; Result alone is the struct's own name,
// and more than one package in a module may declare it. Field is the field at
// issue, empty for a finding about a whole result. Carriers are the surfaces
// that do carry the field, sorted. Detail carries whatever the kind needs beyond
// those.
type Finding struct {
	Kind          Kind
	File          string
	Line          int
	Surface       string
	Core          string
	Result        string
	ResultPackage string
	Field         string
	Carriers      []string
	Detail        string
}

// qualifiedResult is the result under the package that declares it, which is
// the spelling every marker this package prescribes names it by.
//
// The prescription is always qualified, and never the shorter bare name even
// where the bare name happens to be unique today. Whether a bare name is
// unambiguous is a property of the whole module rather than of the site, so an
// author cannot see it from where they write the marker, and a bare marker that
// is accepted today becomes unsupported the day a second package declares a
// result of that name — with nothing at the site having changed. A prescription
// that is only conditionally valid is a prescription for a latent failure.
func (f Finding) qualifiedResult() string {
	if f.ResultPackage == "" {
		return f.Result
	}
	return f.ResultPackage + "." + f.Result
}

// String renders a finding as one line, and is what a report prints.
func (f Finding) String() string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s:%d: %s: ", f.File, f.Line, f.Kind)
	switch f.Kind {
	case DroppedField:
		fmt.Fprintf(&b, "%s drops %s.%s, which %s computes and %s carries. "+
			"Carry it, or say where it is dropped why this surface cannot: "+
			"// projection-omits %s.%s: <reason>",
			f.Surface, f.Result, f.Field, f.Core, strings.Join(f.Carriers, " and "),
			f.qualifiedResult(), f.Field)
	case ResultDiscarded:
		fmt.Fprintf(&b, "%s calls %s and discards its %s, so every fact it computed is dropped at once, "+
			"while %s carries %s. Carry it, or say where it is discarded why this surface cannot: "+
			"// projection-omits %s: <reason>",
			f.Surface, f.Core, f.Result, strings.Join(f.Carriers, " and "), f.Detail, f.qualifiedResult())
	case UnreadableSite:
		fmt.Fprintf(&b, "%s's use of the %s from %s %s, so no parity verdict is drawn for it here",
			f.Surface, f.Result, f.Core, f.Detail)
	case UnreadableCore:
		fmt.Fprintf(&b, "%s hands back %s and %s, so what its callers are given cannot be compared",
			f.Core, f.Result, f.Detail)
	case ExemptionUnsupported:
		// Echoed as the author spelled it, qualifier and all, so a refusal about
		// a name is readable against the marker it is about.
		omitted := f.qualifiedResult()
		if f.Field != "" {
			omitted += "." + f.Field
		}
		fmt.Fprintf(&b, "%s states that it omits %s, but %s", f.Surface, omitted, f.Detail)
	}
	return b.String()
}

// Report renders findings as text, one per line, in the order given.
func Report(findings []Finding) string {
	var b strings.Builder
	for _, f := range findings {
		b.WriteString(f.String())
		b.WriteByte('\n')
	}
	return b.String()
}

// Result is what one read produced: its findings, and the census that says what
// it reached.
//
// The census is part of the answer rather than diagnostics beside it. A read
// over no shared computation returns no finding, which is also what a clean read
// returns, so the counts are what make an empty finding list mean something.
type Result struct {
	// Findings are ordered by file, then position, then kind, then result and
	// field — an order that depends on the sources and not on a map's iteration,
	// so the same input reports the same bytes.
	Findings []Finding

	// SharedComputations are the producers called from two or more surfaces,
	// each as package directory and function name, sorted.
	SharedComputations []string

	// ResultTypes are the structs those producers hand back, each as package
	// directory and type name, sorted. Only structs carrying at least one
	// exported field appear: a result with no field has nothing to project.
	ResultTypes []string

	// Projections is how many producer results the read followed all the way to
	// a field set: one per call site and result position that was neither
	// unreadable nor discarded. A surface calling one computation twice counts
	// twice, because each call is a projection that had to be read.
	Projections int

	// UnreadResultTypes are the shared results NO surface projects readably, as
	// package directory and type name, sorted.
	//
	// They are the read's own reach reported as data rather than as findings. No
	// parity claim is made about them in either direction, and no finding is
	// emitted for them: with no readable side there is no comparison for an
	// unreadable site to have suppressed. Naming them keeps a green from being
	// read as covering them.
	UnreadResultTypes []string
}

// exemptionRE matches an at-site exemption. The result is always named, because
// one function can project two results and an exemption naming only a field
// would be ambiguous exactly where it mattered.
//
// The field is optional, and its absence is what distinguishes the two things a
// surface can legitimately omit: one field of a result it otherwise carries, or
// the result as a whole. Both are reported as findings — a dropped field and a
// discarded result — so both need a way to be answered at the site, and the two
// forms are read back against the site by different checks because they claim
// different things.
//
// The result may be named bare or under the package directory that declares it,
// and both forms carry the optional field:
//
//	projection-omits Answer.Hint: …
//	projection-omits internal/core.Answer.Hint: …
//
// The qualified form exists because a bare name does not always identify one
// result: a module may declare that name in more than one package, and then the
// bare marker names all of them and identifies none. The qualified form is what
// every finding prescribes, and the bare form stays accepted wherever it does
// identify one result — an author who can see that it does may write it.
//
// A qualifier is present exactly when the first token carries a path separator,
// which is what makes the parse unambiguous with no naming convention behind it:
// a directory and a result name are both single tokens, so nothing else could
// tell `Answer.Hint` from `core.Answer`. 🔴 One bound follows and is stated
// rather than hidden: a shared computation declared in a package directory with
// no separator in it — which is to say directly at the module root — has no
// qualified form available, and where such a result's name is also shared, no
// marker can distinguish it. No core reachable by this read sits there, because
// a caller bounds the cores to a subtree.
var exemptionRE = regexp.MustCompile(`projection-omits\s+([A-Za-z_][A-Za-z0-9_/]*)(?:\.([A-Za-z_][A-Za-z0-9_]*))?(?:\.([A-Za-z_][A-Za-z0-9_]*))?\s*:\s*(.*)$`)

// maxFollowDepth bounds how far a value handed on to a helper is followed.
// Beyond it the site is reported as unreadable rather than followed further: an
// unbounded follow would keep opening bodies until it ran out of tree, and a
// follow that quietly gave up would return a clean result over a body it never
// saw.
//
// It is a budget over HOPS rather than over package boundaries, and the same
// budget covers a hop inside the caller's own package and a hop across a package
// boundary. What it bounds is how far a value has travelled from the binding a
// finding names, which is what a reader has to hold in their head to check the
// finding — and that distance is the same whether the intervening helper was
// declared next door or one import away.
//
// The cycle bound is separate and is not a depth at all: [analysis.readFields]
// carries a set of the function-and-value pairs it has already opened, keyed by
// the package directory as well as the function, so a value passed around a ring
// of helpers is met a second time and answered rather than followed again. Depth
// alone would terminate such a ring only by exhausting the budget, and would
// report the exhaustion as a site it could not read rather than as the complete
// answer it is.
//
// A ring of direct calls cannot span two packages in valid Go — the two would
// have to import each other — so the shape that reaches this read is a hop
// across a package boundary INTO a ring, never a ring made of such hops. The set
// is keyed by directory all the same, because the alternative is a key that
// happens to be unique and stops being so the day two packages declare a helper
// of one name.
const maxFollowDepth = 3

// Analyze reads every surface's projection of every shared computation's result
// and reports what it finds.
//
// root is the module root and modulePath its own import path, which is what
// turns a call's package qualifier into a directory in this module. surfaces are
// the caller-facing surfaces and the directories their projections live in.
// coreScope bounds which package directories count as shared computation cores,
// empty meaning all of them.
//
// It refuses rather than reports over an empty corpus: fewer than two surfaces
// leaves nothing to compare, so a clean result would describe the arguments
// rather than the tree.
func Analyze(root, modulePath, coreScope string, surfaces []Surface) (Result, error) {
	if len(surfaces) < 2 {
		return Result{}, fmt.Errorf("parity needs two surfaces to compare and %d was given, so a clean result would certify nothing",
			len(surfaces))
	}

	s := newSource(root, modulePath)
	surfaceDirs := map[string]bool{}
	for _, surface := range surfaces {
		if surface.Name == "" {
			return Result{}, fmt.Errorf("a surface was given with no name, so no finding could say which surface it is about")
		}
		if len(surface.Dirs) == 0 {
			return Result{}, fmt.Errorf("the surface %q was given no directory, so its projections would read as absent rather than unread",
				surface.Name)
		}
		for _, dir := range surface.Dirs {
			surfaceDirs[dir] = true
		}
	}

	a := &analysis{
		source:        s,
		surfaces:      surfaces,
		reads:         map[string]map[string]bool{},
		consumes:      map[string]bool{},
		incomplete:    map[string]bool{},
		discarded:     map[string]Finding{},
		participating: map[string][]posRange{},
		firstSite:     map[string]Finding{},
		resultTypes:   map[string]resultType{},
		reportedCore:  map[string]bool{},
	}

	sites, err := collectCallSites(s, surfaces, surfaceDirs, coreScope)
	if err != nil {
		return Result{}, err
	}
	shared := sharedProducers(sites)

	producers := map[string]producer{}
	for key := range shared {
		coreDir, funcName := splitProducerKey(key)
		p, perr := readProducer(s, coreDir, funcName, 0)
		if perr != nil {
			return Result{}, perr
		}
		if p.skip {
			delete(shared, key)
			continue
		}
		producers[key] = p
	}

	// Sorted, so a tree with more than one defect reports them in the same order
	// on every run rather than in whichever order a map handed them over.
	slices.SortStableFunc(sites, func(x, y callSite) int {
		xf, xl := s.position(x.call.Pos())
		yf, yl := s.position(y.call.Pos())
		if xf != yf {
			return strings.Compare(xf, yf)
		}
		return xl - yl
	})

	for _, site := range sites {
		key := site.coreDir + "." + site.funcName
		if _, ok := shared[key]; !ok {
			continue
		}
		a.readSite(site, producers[key])
	}

	a.verifyExemptions()
	a.reportDivergence()

	return a.finish(shared), nil
}

// posRange is one function's extent, for deciding whether an exemption sits at
// a projection of the result it names.
type posRange struct {
	from, to token.Pos
}

type analysis struct {
	source   *source
	surfaces []Surface

	// reads maps a surface-and-result pair to the fields that surface reads.
	reads map[string]map[string]bool

	// consumes records that a surface calls a producer handing back the result.
	consumes map[string]bool

	// incomplete records a surface-and-result pair whose read did not complete,
	// so no parity verdict may be drawn over it.
	incomplete map[string]bool

	// discarded holds, per surface-and-result pair the surface threw away
	// whole, the finding that reports it — held rather than emitted, because a
	// result every surface discards is not a divergence and the aggregate is
	// what says which case this is.
	discarded map[string]Finding

	// participating records the functions a surface's read of a result passed
	// through, so an exemption can be placed against them.
	participating map[string][]posRange

	// firstSite is a template finding per surface-and-result pair, carrying the
	// position a divergence is reported at.
	firstSite map[string]Finding

	resultTypes  map[string]resultType
	reportedCore map[string]bool

	findings []Finding

	// pending holds an unreadable-site finding until the aggregate says whether
	// it suppressed anything. A result type NO surface projects readably has no
	// comparison to suppress, so a finding there would report the reach of this
	// read rather than a fact about the tree; the census carries those instead.
	pending []pendingFinding

	exemptions  []exemption
	verified    map[string]bool
	projections int
}

// pendingFinding is a finding held until the result type it concerns is known
// to have a readable projection somewhere.
type pendingFinding struct {
	finding   Finding
	resultKey string
}

// exemption is one stated at-site exemption.
type exemption struct {
	surface string
	file    string
	line    int
	pos     token.Pos

	// pkg is the package directory the marker named the result under, empty
	// where the marker named it bare.
	pkg    string
	result string
	field  string
	reason string

	// malformed is non-empty where the marker's shape names neither the bare
	// nor the qualified form. It is carried rather than dropped so the marker is
	// refused out loud: an author who wrote a reason and had it silently ignored
	// believes a fact is excused when nothing excuses it.
	malformed string
}

// parseExemption reads the result, its package and its field out of a matched
// marker, or says why the marker's shape names none of them.
func parseExemption(first, second, third string) (pkg, result, field, malformed string) {
	if strings.Contains(first, "/") {
		if second == "" {
			return "", "", "", fmt.Sprintf("it names the package %s and no result in it — write the result after the package, as in // projection-omits %s.SomeResult: <reason>",
				first, first)
		}
		return first, second, third, ""
	}
	if third != "" {
		return "", "", "", fmt.Sprintf("it names three parts, %s.%s.%s, with no package among them — a result and a field are two, so either drop one or qualify the result with the package directory that declares it",
			first, second, third)
	}
	return "", first, second, ""
}

func pairKey(surface, resultKey string) string { return surface + "\x00" + resultKey }

func fieldKey(surface, resultKey, field string) string {
	return surface + "\x00" + resultKey + "\x00" + field
}

func splitProducerKey(key string) (coreDir, funcName string) {
	i := strings.LastIndex(key, ".")
	return key[:i], key[i+1:]
}

// readSite reads one call to a shared computation from one surface.
func (a *analysis) readSite(site callSite, p producer) {
	file, line := a.source.position(site.call.Pos())
	core := site.coreDir + "." + site.funcName

	if p.coreDir != "" {
		// A re-exported producer was followed to the package that declares it,
		// so the result struct is read from there rather than from the package
		// the surface named.
		core = p.coreDir + "." + p.funcName
	}

	if p.unreadable != "" {
		if !a.reportedCore[core] {
			a.reportedCore[core] = true
			a.findings = append(a.findings, Finding{
				Kind: UnreadableCore, File: file, Line: line, Surface: site.surface,
				Core: core, Result: "no readable result", Detail: p.unreadable,
			})
		}
		return
	}
	if len(p.structResults) == 0 {
		return
	}

	binding, bound := bindingFor(site.fn, site.call)

	indices := make([]int, 0, len(p.structResults))
	for index := range p.structResults {
		indices = append(indices, index)
	}
	slices.Sort(indices)

	for _, index := range indices {
		name := p.structResults[index]
		resultKey := p.coreDir + "." + name

		rt, ok := a.resultTypes[resultKey]
		if !ok {
			read, err := readResultType(a.source, p.coreDir, name)
			if err != nil {
				read = resultType{coreDir: p.coreDir, name: name, unreadable: err.Error()}
			}
			a.resultTypes[resultKey] = read
			rt = read
		}
		if rt.unreadable != "" {
			if !a.reportedCore[resultKey] {
				a.reportedCore[resultKey] = true
				a.findings = append(a.findings, Finding{
					Kind: UnreadableCore, File: file, Line: line, Surface: site.surface,
					Core: core, Result: name, ResultPackage: p.coreDir, Detail: rt.unreadable,
				})
			}
			continue
		}
		if len(rt.fields) == 0 {
			continue
		}

		pair := pairKey(site.surface, resultKey)
		a.consumes[pair] = true
		if _, held := a.firstSite[pair]; !held {
			a.firstSite[pair] = Finding{
				File: file, Line: line, Surface: site.surface, Core: core,
				Result: name, ResultPackage: p.coreDir,
			}
		}
		a.participating[pair] = append(a.participating[pair], funcRange(site.fn))

		unreadable := func(detail string) {
			a.incomplete[pair] = true
			a.pending = append(a.pending, pendingFinding{
				finding: Finding{
					Kind: UnreadableSite, File: file, Line: line, Surface: site.surface,
					Core: core, Result: name, ResultPackage: p.coreDir, Detail: detail,
				},
				resultKey: resultKey,
			})
		}

		if !bound {
			unreadable("is not bound to a name at the call, so what it reads from it is not in this body")
			continue
		}
		ident, held := binding[index]
		if !held || ident == "" {
			unreadable("is assigned to something other than a plain name, which this read does not follow")
			continue
		}
		if ident == "_" {
			a.incomplete[pair] = true
			if _, held := a.discarded[pair]; !held {
				a.discarded[pair] = Finding{
					Kind: ResultDiscarded, File: file, Line: line, Surface: site.surface,
					Core: core, Result: name, ResultPackage: p.coreDir,
				}
			}
			continue
		}

		fields, escape, visited := a.readFields(site.dir, ident, site.fn, 0, map[string]bool{})
		a.participating[pair] = append(a.participating[pair], visited...)
		if escape != "" {
			unreadable(escape)
			continue
		}
		if a.reads[pair] == nil {
			a.reads[pair] = map[string]bool{}
		}
		for _, field := range fields {
			a.reads[pair][field] = true
		}
		a.projections++
	}
}

func funcRange(fn *ast.FuncDecl) posRange {
	from := fn.Pos()
	if fn.Doc != nil {
		from = fn.Doc.Pos()
	}
	return posRange{from: from, to: fn.End()}
}

// bindingFor is the identifier each of a call's results is bound to, where the
// call is the whole right-hand side of an assignment or a declaration.
func bindingFor(fn *ast.FuncDecl, call *ast.CallExpr) (map[int]string, bool) {
	out := map[int]string{}
	found := false
	ast.Inspect(fn.Body, func(node ast.Node) bool {
		if found {
			return false
		}
		switch st := node.(type) {
		case *ast.AssignStmt:
			if len(st.Rhs) != 1 || st.Rhs[0] != call {
				return true
			}
			for i, target := range st.Lhs {
				out[i] = identName(target)
			}
			found = true
		case *ast.ValueSpec:
			if len(st.Values) != 1 || st.Values[0] != call {
				return true
			}
			for i, target := range st.Names {
				out[i] = target.Name
			}
			found = true
		}
		return !found
	})
	return out, found
}

func identName(expr ast.Expr) string {
	if ident, ok := expr.(*ast.Ident); ok {
		return ident.Name
	}
	return ""
}

// argRef is one call and the position a value was passed at.
type argRef struct {
	call  *ast.CallExpr
	index int
}

// followedCallee is a function a value was handed to whose body this read can
// open: the declaration, the package directory declaring it, and the name a
// finding calls it by.
type followedCallee struct {
	dir  string
	name string
	decl *ast.FuncDecl
}

// resolveCallee is the function a call names — in the calling package, or in
// another package of this module — together with the directory that declares it.
//
// why is non-empty where no such body was reached. It is a phrase the caller
// completes rather than a sentence of its own, because the site's report names
// the value first and the reason reads as the continuation of that sentence.
//
// 🔴 The bound is the MODULE, and it is a real bound rather than a convenience. A
// call into the standard library or into a dependency names a body this read has
// no source for, so the value stays unfollowed and the site stays unreadable —
// which is the honest answer, because a field set assembled without that body
// would be short, and a short field set cannot support a claim that some other
// surface dropped something. Inside the module the body IS there to be read, so
// declining to read it would report this read's own reach as though it were a
// fact about the tree: that is what a qualified call cost before, and two
// surfaces handing one shared result to one shared writer were both abandoned at
// the call while the writer sat in the same module, one import away.
//
// Deliberately NOT bounded to the directories already parsed. Which directories
// those are depends on the order the sites happened to be walked in, so a bound
// drawn there would make one site's verdict depend on another site's position and
// the same tree would report different findings on a different walk. The parse is
// lazy and cached, and reaching a new directory is the same read of the same
// tree.
//
// Three shapes are refused, and each is refused by name rather than as one
// undifferentiated failure, because they call for different repairs. A bare name
// the calling package does not declare is a call this read could not resolve at
// all. A qualified name whose qualifier is no package of this module is a body
// outside the tree — and a method call on a value reads identically here, since
// telling the two apart needs type resolution this read does not carry. A
// qualified name that does resolve to a package of this module but to no
// top-level function in it is a type conversion, a package-level function value
// or a method: each is a call syntactically and none is a body to open.
func (a *analysis) resolveCallee(dir string, fn *ast.FuncDecl, call *ast.CallExpr, decls map[string]*ast.FuncDecl) (followedCallee, string) {
	if name := identName(call.Fun); name != "" {
		decl, known := decls[name]
		if !known {
			return followedCallee{}, fmt.Sprintf("to a call this read cannot resolve to a function in %s", dir)
		}
		return followedCallee{dir: dir, name: name, decl: decl}, ""
	}

	qualifier, funcName, qualified := selectorCall(call)
	if !qualified {
		return followedCallee{}, fmt.Sprintf("to a call this read cannot resolve to a function in %s", dir)
	}

	targetDir, inModule := a.source.scopeAt(fn.Pos())[qualifier]
	if !inModule {
		return followedCallee{}, fmt.Sprintf("to %s.%s, which names no package of this module — a call into another module and "+
			"a method on a value read alike here, and neither is a body this read can open", qualifier, funcName)
	}

	parsed, err := a.source.read(targetDir)
	if err != nil {
		return followedCallee{}, fmt.Sprintf("to %s.%s in %s, which could not be parsed: %v", qualifier, funcName, targetDir, err)
	}
	decl, known := funcDeclsByName(parsed.Files)[funcName]
	if !known {
		return followedCallee{}, fmt.Sprintf("to %s.%s, which %s declares as no top-level function — a type conversion, a "+
			"package-level function value and a method each read alike here", qualifier, funcName, targetDir)
	}
	return followedCallee{dir: targetDir, name: targetDir + "." + funcName, decl: decl}, ""
}

// readFields collects the fields a function reads from ident, following the
// value one hop at a time into any helper of this module it is handed to whole —
// in the calling package or in another, resolved by [analysis.resolveCallee].
//
// So "this surface carries the field" means the surface's own body names it, or a
// body in this module that the surface hands the value to names it. The boundary
// that ends the follow is the MODULE rather than the package, and it is drawn
// there because it is the boundary where the source stops: inside it the body is
// readable, so declining to read it would report this read's reach as a fact
// about the tree.
//
// escape is non-empty for the first use the reading cannot follow, in which case
// the field set is incomplete and no verdict may be drawn from it. visited are
// the function extents the reading passed through, so an exemption stated in a
// helper counts as stated at the site.
//
// Following across a package boundary widens `visited` across packages too, and
// that widens nothing an exemption can excuse. A marker is collected only from
// the directories a surface declares, and it is read back against the extents
// recorded for the surface whose directory holds it — so extents added to one
// surface's reading cannot place another surface's marker, and a marker in a
// package no surface declares is never collected at all.
func (a *analysis) readFields(dir, ident string, fn *ast.FuncDecl, depth int, seen map[string]bool) (fields []string, escape string, visited []posRange) {
	if fn.Body == nil {
		return nil, "is read in a function with no body", nil
	}
	marker := dir + "." + fn.Name.Name + "(" + ident + ")"
	if seen[marker] {
		return nil, "", nil
	}
	seen[marker] = true
	visited = append(visited, funcRange(fn))

	selectorBase := map[*ast.Ident]string{}
	argOf := map[*ast.Ident]argRef{}
	binding := map[*ast.Ident]bool{}
	nilTest := map[*ast.Ident]bool{}
	var uses []*ast.Ident

	target := func(expr ast.Expr) {
		if id, ok := expr.(*ast.Ident); ok && id.Name == ident {
			binding[id] = true
		}
	}
	ast.Inspect(fn.Body, func(node ast.Node) bool {
		switch n := node.(type) {
		case *ast.SelectorExpr:
			if base, ok := n.X.(*ast.Ident); ok && base.Name == ident {
				selectorBase[base] = n.Sel.Name
			}
		case *ast.BinaryExpr:
			// A test against nil reads no field and lets nothing out of the
			// body, so it is neither a read nor an escape. Every surface tests
			// whether it was handed anything at all; treating that as an escape
			// would report the shape common to all of them.
			if n.Op != token.EQL && n.Op != token.NEQ {
				break
			}
			if identName(n.Y) == "nil" {
				target := n.X
				if id, ok := target.(*ast.Ident); ok && id.Name == ident {
					nilTest[id] = true
				}
			}
			if identName(n.X) == "nil" {
				if id, ok := n.Y.(*ast.Ident); ok && id.Name == ident {
					nilTest[id] = true
				}
			}
		case *ast.CallExpr:
			for i, arg := range n.Args {
				if id, ok := arg.(*ast.Ident); ok && id.Name == ident {
					argOf[id] = argRef{call: n, index: i}
				}
			}
		case *ast.AssignStmt:
			for _, lhs := range n.Lhs {
				target(lhs)
			}
		case *ast.ValueSpec:
			for _, name := range n.Names {
				target(name)
			}
		case *ast.RangeStmt:
			target(n.Key)
			target(n.Value)
		case *ast.Ident:
			if n.Name == ident {
				uses = append(uses, n)
			}
		}
		return true
	})

	// A name bound more than once in one body no longer identifies one value, so
	// a field read from it after the second binding may belong to something else
	// entirely. The reading declines rather than attributing the reads to the
	// shared result.
	if len(binding) > 1 {
		return nil, fmt.Sprintf("binds %s more than once, so a field read from it need not be the shared computation's result", ident), visited
	}

	parsed, err := a.source.read(dir)
	if err != nil {
		return nil, fmt.Sprintf("sits in %s, which could not be parsed: %v", dir, err), visited
	}
	decls := funcDeclsByName(parsed.Files)

	for _, use := range uses {
		if binding[use] || nilTest[use] {
			// The occurrence that binds the name is not a use of the value, and
			// a test against nil reads nothing from it.
			continue
		}
		if field, isSelectorBase := selectorBase[use]; isSelectorBase {
			fields = append(fields, field)
			continue
		}
		ref, isArg := argOf[use]
		if !isArg {
			if escape == "" {
				escape = fmt.Sprintf("uses %s as a value rather than reading a field from it, so the fields it reads are not all in this body", ident)
			}
			continue
		}
		callee, why := a.resolveCallee(dir, fn, ref.call, decls)
		if why != "" {
			if escape == "" {
				escape = "hands " + ident + " " + why
			}
			continue
		}
		if depth+1 >= maxFollowDepth {
			if escape == "" {
				escape = fmt.Sprintf("hands %s on to %s beyond the depth this read follows", ident, callee.name)
			}
			continue
		}
		params, variadic := flatParamNames(callee.decl)
		if ref.index >= len(params) || (variadic && ref.index >= len(params)-1) {
			if escape == "" {
				escape = fmt.Sprintf("hands %s to %s at a position this read cannot bind to a parameter", ident, callee.name)
			}
			continue
		}
		param := params[ref.index]
		if param == "" {
			// The callee cannot name the value, so it reads no field from it.
			// That is a complete answer rather than an escape.
			continue
		}
		deeper, deeperEscape, deeperVisited := a.readFields(callee.dir, param, callee.decl, depth+1, seen)
		visited = append(visited, deeperVisited...)
		if deeperEscape != "" {
			if escape == "" {
				escape = fmt.Sprintf("hands %s to %s, which %s", ident, callee.name, deeperEscape)
			}
			continue
		}
		fields = append(fields, deeper...)
	}

	slices.Sort(fields)
	return slices.Compact(fields), escape, visited
}

// collectExemptions reads every stated exemption from every surface's own
// source.
func (a *analysis) collectExemptions() {
	for _, surface := range a.surfaces {
		for _, dir := range surface.Dirs {
			parsed, err := a.source.read(dir)
			if err != nil {
				continue
			}
			for _, file := range parsed.Files {
				for _, group := range file.Comments {
					for _, c := range group.List {
						match := exemptionRE.FindStringSubmatch(c.Text)
						if match == nil {
							continue
						}
						path, line := a.source.position(c.Pos())
						pkg, result, field, malformed := parseExemption(match[1], match[2], match[3])
						a.exemptions = append(a.exemptions, exemption{
							surface: surface.Name, file: path, line: line, pos: c.Pos(),
							pkg: pkg, result: result, field: field,
							reason: strings.TrimSpace(match[4]), malformed: malformed,
						})
					}
				}
			}
		}
	}
	slices.SortStableFunc(a.exemptions, func(x, y exemption) int {
		if x.file != y.file {
			return strings.Compare(x.file, y.file)
		}
		return x.line - y.line
	})
}

// resultsNamed are the collected result types a marker's stated name reaches,
// sorted, each as package directory and type name.
//
// A qualified name reaches at most one, because that pair is the key this read
// holds a result type under. A bare name reaches every result of that name, and
// more than one is what the ambiguity refusal is drawn from — the set is
// deliberately NOT narrowed to the results this surface calls. Narrowing it
// would make a marker's validity depend on which computations its own surface
// happens to reach, so the same words would be accepted on one surface and
// refused on another, and an author could not tell from the marker which it was.
func (a *analysis) resultsNamed(pkg, name string) []string {
	if pkg != "" {
		key := pkg + "." + name
		if _, held := a.resultTypes[key]; held {
			return []string{key}
		}
		return nil
	}
	var matches []string
	for key, rt := range a.resultTypes {
		if rt.name == name {
			matches = append(matches, key)
		}
	}
	slices.Sort(matches)
	return matches
}

// verifyExemptions reads every stated exemption back against what the surface
// was measured to do, and records the ones the site bears out.
func (a *analysis) verifyExemptions() {
	a.collectExemptions()
	a.verified = map[string]bool{}

	for _, e := range a.exemptions {
		reject := func(detail string) {
			a.findings = append(a.findings, Finding{
				Kind: ExemptionUnsupported, File: e.file, Line: e.line, Surface: e.surface,
				Result: e.result, ResultPackage: e.pkg, Field: e.field, Detail: detail,
			})
		}

		if e.malformed != "" {
			reject(e.malformed)
			continue
		}

		matches := a.resultsNamed(e.pkg, e.result)
		switch len(matches) {
		case 0:
			if e.pkg != "" {
				if elsewhere := a.resultsNamed("", e.result); len(elsewhere) > 0 {
					reject(fmt.Sprintf("%s declares no shared result named %s; the shared results of that name are %s",
						e.pkg, e.result, strings.Join(elsewhere, ", ")))
					continue
				}
			}
			reject("no shared computation this surface calls hands back a result of that name")
			continue
		case 1:
		default:
			// The list is what says which results are meant, so the count is not
			// restated beside it: a transcribed count disagreeing with the list
			// it introduces is the shape this instrument exists to remove.
			//
			// The example is built from the first match because the form is what
			// it demonstrates, and it says "the one it means" so it cannot be read
			// as the answer — this read has no way to know which of the matches an
			// author meant, and the list beside it is what makes that choice.
			suggestion := matches[0]
			if e.field != "" {
				suggestion += "." + e.field
			}
			reject(fmt.Sprintf("the shared results named %s are %s, so the exemption does not say which of them it means — "+
				"name the one it means under the package that declares it, in the form // projection-omits %s: <reason>",
				e.result, strings.Join(matches, ", "), suggestion))
			continue
		}
		// Past this point the result is settled, so every refusal below names it
		// by the key rather than by whatever the marker spelled. The header
		// already echoes the author's own spelling, and a detail naming a bare
		// name beside a qualified header would leave a reader of a module with two
		// results of that name checking the wrong one — which is the very
		// confusion the qualified form exists to remove.
		resultKey := matches[0]
		rt := a.resultTypes[resultKey]

		if e.field != "" && !slices.Contains(rt.fields, e.field) {
			reject(fmt.Sprintf("%s carries no field of that name (it carries %s)", resultKey, strings.Join(rt.fields, ", ")))
			continue
		}
		// The subject is what the exemption claims to omit, and restating it is
		// the one reason a declaration can give that says nothing at all.
		subject := e.field
		if subject == "" {
			subject = e.result
		}
		if e.reason == "" || strings.EqualFold(strings.TrimSuffix(e.reason, "."), subject) {
			reject("it states no reason, and a declaration satisfiable by saying nothing says nothing")
			continue
		}

		pair := pairKey(e.surface, resultKey)
		if !a.consumes[pair] {
			reject(fmt.Sprintf("this surface calls nothing that hands back %s, so it drops no field of one", resultKey))
			continue
		}
		// Each form is held against what it actually claims. A field exemption is
		// stale where the surface reads that field; a whole-result exemption is
		// unsupported where the surface never discarded the result, and stale
		// where it does project fields from it — which is the reading that keeps
		// the broader form from clearing more than the narrow one could.
		if e.field != "" {
			if a.reads[pair][e.field] {
				reject(fmt.Sprintf("this surface does read %s.%s, so the exemption is stale — delete it rather than leaving a reason nothing is dropped for",
					resultKey, e.field))
				continue
			}
		} else {
			if _, discards := a.discarded[pair]; !discards {
				reject(fmt.Sprintf("this surface does not discard the %s it is handed, so there is no whole result here for it to excuse — name the field it omits instead",
					resultKey))
				continue
			}
			if len(a.reads[pair]) > 0 {
				reject(fmt.Sprintf("this surface does project %s (it reads %s), so it does not omit the whole of it — name the field it omits instead",
					resultKey, strings.Join(sortedKeys(a.reads[pair]), ", ")))
				continue
			}
		}
		if !withinAny(e.pos, a.participating[pair]) {
			reject(fmt.Sprintf("it sits away from every projection of %s on this surface, which makes it a list entry rather than a statement at the site",
				resultKey))
			continue
		}
		// A whole-result exemption is recorded under the empty field, which is
		// the same key the discarded finding is cleared against below.
		a.verified[fieldKey(e.surface, resultKey, e.field)] = true
	}
}

// sortedKeys are a set's members in order, so a refusal naming them reads the
// same on every run rather than in whichever order a map handed them over.
func sortedKeys(set map[string]bool) []string {
	out := make([]string, 0, len(set))
	for key := range set {
		out = append(out, key)
	}
	slices.Sort(out)
	return out
}

func withinAny(pos token.Pos, ranges []posRange) bool {
	for _, r := range ranges {
		if pos >= r.from && pos <= r.to {
			return true
		}
	}
	return false
}

// reportDivergence names every field one surface carries and another drops.
func (a *analysis) reportDivergence() {
	resultKeys := make([]string, 0, len(a.resultTypes))
	for key := range a.resultTypes {
		resultKeys = append(resultKeys, key)
	}
	slices.Sort(resultKeys)

	for _, resultKey := range resultKeys {
		rt := a.resultTypes[resultKey]
		if rt.unreadable != "" || len(rt.fields) == 0 {
			continue
		}

		var consumers []string
		for _, surface := range a.surfaces {
			if a.consumes[pairKey(surface.Name, resultKey)] {
				consumers = append(consumers, surface.Name)
			}
		}
		if len(consumers) < 2 {
			continue
		}

		// A result one surface threw away whole is reported once, naming what the
		// OTHER surfaces carry. One exclusion, and it is the difference between a
		// divergence and a shape every surface shares: a result no surface reads
		// any field of is a computation nobody projects, so there is no carrier
		// for the discarding surface to disagree with.
		//
		// Deliberately NOT excluded: a surface that discards the result at one
		// call and reads it at another. The call that discarded it still carries
		// nothing of what it was handed, and which call a reader reaches is not
		// something this read can know — so the discard is reported and the
		// surface answers it at the site if the choice was deliberate. Its
		// per-field findings are withheld separately, because a read that met a
		// discarded binding did not follow that projection to a full field set.
		var carriedAnywhere []string
		for _, surface := range consumers {
			if len(a.reads[pairKey(surface, resultKey)]) > 0 {
				carriedAnywhere = append(carriedAnywhere, surface)
			}
		}
		for _, surface := range consumers {
			finding, held := a.discarded[pairKey(surface, resultKey)]
			if !held {
				continue
			}
			if a.verified[fieldKey(surface, resultKey, "")] {
				// The surface stated at the site why it consumes the presence of
				// this result rather than its content, and the statement was read
				// back against the site.
				continue
			}
			var others, carried []string
			for _, carrier := range carriedAnywhere {
				if carrier == surface {
					continue
				}
				others = append(others, carrier)
				for field := range a.reads[pairKey(carrier, resultKey)] {
					carried = append(carried, field)
				}
			}
			if len(others) == 0 {
				continue
			}
			slices.Sort(carried)
			finding.Carriers = others
			finding.Detail = strings.Join(slices.Compact(carried), ", ")
			a.findings = append(a.findings, finding)
		}

		for _, field := range rt.fields {
			var carriers, droppers []string
			for _, surface := range consumers {
				if a.reads[pairKey(surface, resultKey)][field] {
					carriers = append(carriers, surface)
					continue
				}
				droppers = append(droppers, surface)
			}
			if len(carriers) == 0 {
				// No surface projects it. The shared computation may be
				// producing something nobody carries, which is a different
				// defect with a different repair.
				continue
			}
			for _, surface := range droppers {
				pair := pairKey(surface, resultKey)
				if a.incomplete[pair] {
					// The read of this surface's projection did not complete
					// and has already been reported. A verdict over a value
					// only partly followed would be a clean result certifying
					// nothing.
					continue
				}
				if a.verified[fieldKey(surface, resultKey, field)] {
					continue
				}
				finding := a.firstSite[pair]
				finding.Kind = DroppedField
				finding.Field = field
				finding.Carriers = carriers
				a.findings = append(a.findings, finding)
			}
		}
	}
}

// releasePending emits each held unreadable-site finding whose result type some
// surface does project readably, and names the rest in the census.
//
// The distinction is what the finding would mean. Where one surface's reads are
// known, a site this read could not follow is a comparison it declined to draw,
// and passing over it would leave a clean result certifying nothing. Where NO
// surface's reads are known, there was never a comparison to draw: reporting it
// would describe how far this read reaches rather than anything about the tree,
// and the corpus is where that belongs.
func (a *analysis) releasePending() []string {
	readable := map[string]bool{}
	for pair := range a.reads {
		if len(a.reads[pair]) > 0 {
			readable[pair[strings.Index(pair, "\x00")+1:]] = true
		}
	}
	unread := map[string]bool{}
	for _, held := range a.pending {
		if readable[held.resultKey] {
			a.findings = append(a.findings, held.finding)
			continue
		}
		unread[held.resultKey] = true
	}

	out := make([]string, 0, len(unread))
	for key := range unread {
		out = append(out, key)
	}
	slices.Sort(out)
	return out
}

// finish orders the findings and assembles the census.
func (a *analysis) finish(shared map[string]map[string]bool) Result {
	unread := a.releasePending()
	out := Result{Findings: a.findings, Projections: a.projections, UnreadResultTypes: unread}

	for key := range shared {
		out.SharedComputations = append(out.SharedComputations, key)
	}
	slices.Sort(out.SharedComputations)

	for key, rt := range a.resultTypes {
		if rt.unreadable == "" && len(rt.fields) > 0 {
			out.ResultTypes = append(out.ResultTypes, key)
		}
	}
	slices.Sort(out.ResultTypes)

	slices.SortStableFunc(out.Findings, func(x, y Finding) int {
		if x.File != y.File {
			return strings.Compare(x.File, y.File)
		}
		if x.Line != y.Line {
			return x.Line - y.Line
		}
		if x.Kind != y.Kind {
			return strings.Compare(string(x.Kind), string(y.Kind))
		}
		if x.Result != y.Result {
			return strings.Compare(x.Result, y.Result)
		}
		return strings.Compare(x.Field, y.Field)
	})
	return out
}
