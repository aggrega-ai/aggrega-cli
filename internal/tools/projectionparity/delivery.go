// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package projectionparity

import (
	"fmt"
	"go/ast"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// This file reads the REQUEST side of surface parity, where the rest of this
// package reads the result side.
//
// The two are one axis apart and the subject is the same pair — one capability,
// several surfaces — which is why this lives here and reuses the module reader
// above rather than standing up a second parse of the same tree. A second reader
// would agree with this one until one of them was taught about a new import
// form, and the agreement is what would hide the divergence.
//
// The rule it enforces is the one the result side does not reach. A delivery —
// whether a capability writes its product or hands back the content the write
// would have produced — is a REQUEST FIELD, never a property of the surface the
// request arrived on. A capability that offers both offers both everywhere, and
// the caller chooses. So a surface that composes a capability's request and
// leaves the delivery selector out of it has decided, on the caller's behalf,
// something the caller was entitled to decide.
//
// A surface has no standing to decline a delivery, and this read admits no
// exemption for one. That is deliberate and it is not an oversight to be
// repaired by adding a marker grammar like the result side's: a transport
// constraint is a fact about a payload, so it belongs in the answer the
// capability gives, not in a per-surface list of what may be asked. A stated
// decline would make the check green on exactly the state the rule forbids.

// DeliveryKind is what a delivery finding says. Values are the identifiers a
// report prints and a driver matches on.
type DeliveryKind string

const (
	// DeliveryNotOffered is the defect this read exists to find: a surface
	// composes a capability's request and omits the delivery selector, so a
	// caller reaching that capability through that surface cannot choose, and
	// the zero value chooses for it.
	DeliveryNotOffered DeliveryKind = "delivery-not-offered"
)

// DeliveryCapability is one capability whose request carries a delivery
// selector, named by the request type that carries it.
//
// The identity is the request TYPE rather than an operation identifier, and that
// is what makes this read possible without a mapping nobody maintains. Every
// capability that carries a delivery is reached by each surface through one
// shared entry point taking one options struct, so the struct is what the
// surfaces have in common — and it is read from the tree, where an operation
// identifier would have to be linked to a request shape by a table.
//
// Selector is the field a caller fills to choose: either the delivery type
// itself, or the boolean the capability's own package hands to the mapping
// function.
type DeliveryCapability struct {
	// Package is the declaring package's directory, relative to the module root.
	Package string

	// Type is the request struct's own name, and Selector the field on it that
	// carries the caller's choice.
	Type     string
	Selector string

	// File and Line are where the selector is declared, so a finding sends a
	// reader to the declaration rather than to whichever surface reported it.
	File string
	Line int
}

// Qualified is the capability under the package that declares it, which is how
// every report names it.
//
// Two packages in one module may declare a request type of the same name, so the
// bare name identifies nothing on its own — and a reader who cannot tell which
// of two capabilities a finding is about cannot act on it.
func (c DeliveryCapability) Qualified() string { return c.Package + "." + c.Type }

// DeliveryOffer is one surface's composition of one capability's request, and
// whether that composition let the caller choose the delivery.
type DeliveryOffer struct {
	Surface    string
	Capability string
	Selector   string
	File       string
	Line       int

	// Offers reports whether the composition names the selector at all. A
	// request composed with every field supplied positionally offers it too:
	// such a literal fills every field of the struct, the selector included.
	Offers bool
}

// DeliveryFinding is one thing read about one surface's composition of one
// capability's request.
type DeliveryFinding struct {
	Kind       DeliveryKind
	File       string
	Line       int
	Surface    string
	Capability string
	Selector   string

	// Offerers are the surfaces that do let the caller choose, sorted. It is
	// carried so a finding can say the delivery is reachable elsewhere, which is
	// what tells a reader this is an omission rather than a capability that
	// simply has no reporting mode.
	Offerers []string
}

// String renders a finding as one line, and is what a report prints.
func (f DeliveryFinding) String() string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s:%d: %s: ", f.File, f.Line, f.Kind)
	fmt.Fprintf(&b, "%s composes %s and omits %s, so a caller reaching this capability here cannot "+
		"choose its delivery and the zero value chooses for it.", f.Surface, f.Capability, f.Selector)
	if len(f.Offerers) > 0 {
		fmt.Fprintf(&b, " %s let the caller choose, so the capability does carry the mode.",
			strings.Join(f.Offerers, " and "))
	}
	b.WriteString(" A delivery is a request a caller makes, not a capability a surface provides, so " +
		"there is no reason to state and none to write: set the selector from the request. Where the " +
		"transport genuinely cannot carry the answer, that is a fact about the payload and belongs in " +
		"the answer this capability gives.")
	return b.String()
}

// DeliveryReport renders findings one per line.
func DeliveryReport(findings []DeliveryFinding) string {
	var b strings.Builder
	for _, f := range findings {
		b.WriteString(f.String())
		b.WriteByte('\n')
	}
	return b.String()
}

// DeliveryResult is what the delivery read found.
type DeliveryResult struct {
	// Capabilities are the request types carrying a delivery selector, sorted by
	// their qualified name. The census is reported rather than only counted,
	// because a clean verdict over an empty or a shrunken capability set is the
	// one failure this read must not have.
	Capabilities []DeliveryCapability

	// Offers is every composition of a capability's request found on a surface,
	// in a fixed order, whether or not it offers the choice. It is the offered
	// set per surface, and it is reported so a green says what it covered.
	Offers []DeliveryOffer

	// Chokepoints are the sites that map a request to a delivery, as
	// file:line: expression. They are what the capability census is derived
	// from, reported so a reader can check the derivation against the tree.
	Chokepoints []string

	// Delegated are the request types carrying a delivery that another
	// capability's package composes, as qualified name and the site composing
	// it. They are excluded from the census above, and they are reported rather
	// than dropped quietly, because the exclusion is a decision about what this
	// read certifies and its size is what tells a reader how much sits outside
	// the verdict.
	Delegated []string

	// Findings are the surfaces that composed a request without the selector.
	Findings []DeliveryFinding
}

// OfferedBy returns the surfaces that let a caller choose the delivery for the
// named capability, sorted.
func (r DeliveryResult) OfferedBy(capability string) []string {
	seen := map[string]bool{}
	for _, o := range r.Offers {
		if o.Capability == capability && o.Offers {
			seen[o.Surface] = true
		}
	}
	out := make([]string, 0, len(seen))
	for name := range seen {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

// AnalyzeDelivery reads which capabilities carry a delivery selector and which
// surfaces let a caller choose it.
//
// deliveryDir is the package declaring the delivery vocabulary, relative to the
// module root. It is supplied rather than guessed, and a wrong value cannot pass
// quietly: the read below refuses a directory that declares neither the delivery
// type nor the mapping function, so a stale spelling fails naming its own cause
// instead of deriving an empty capability set and reporting it clean.
//
// coreScope bounds where a capability may be declared. A request type outside it
// is published vocabulary shared with consumers this read knows nothing about,
// and a surface composing one is not a surface answering for a capability of
// this module.
//
// It asserts nothing about the findings. Which of them fail is the caller's
// decision, which is why this returns the result rather than judging it.
func AnalyzeDelivery(root, modulePath, coreScope, deliveryDir string, surfaces []Surface) (DeliveryResult, error) {
	var res DeliveryResult

	if len(surfaces) == 0 {
		return res, fmt.Errorf("the delivery read was given no surface, so it would compose no offered set and " +
			"report every capability as uncomposed rather than as unoffered")
	}
	surfaceDirs := map[string]bool{}
	for _, surface := range surfaces {
		if surface.Name == "" {
			return res, fmt.Errorf("a surface was given with no name, so no finding could say which surface it is about")
		}
		if len(surface.Dirs) == 0 {
			return res, fmt.Errorf("the surface %q was given no directory, so its requests would read as absent "+
				"rather than as unread", surface.Name)
		}
		for _, dir := range surface.Dirs {
			surfaceDirs[dir] = true
		}
	}

	s := newSource(root, modulePath)

	// The delivery vocabulary is read before anything is derived from it, so a
	// stale deliveryDir fails here rather than yielding an empty capability set
	// that a clean verdict would then be drawn over.
	deliveryTypeName, deliveryFuncName, err := readDeliveryVocabulary(s, deliveryDir)
	if err != nil {
		return res, err
	}

	coreDirs, err := deliveryCoreDirs(root, coreScope, surfaceDirs)
	if err != nil {
		return res, err
	}

	byQualified := map[string]DeliveryCapability{}
	for _, dir := range coreDirs {
		found, ferr := deliveryCapabilitiesIn(s, dir, deliveryDir, deliveryTypeName, deliveryFuncName)
		if ferr != nil {
			return res, ferr
		}
		for _, c := range found {
			// A request type reached by both shapes — a delivery-typed field and a
			// boolean the package maps — keeps the delivery-typed one, which is the
			// selector a surface fills. Recording both would report one capability
			// twice and make a surface that filled either read as omitting the other.
			if held, ok := byQualified[c.Qualified()]; ok && held.Selector == deliveryTypeName {
				continue
			}
			byQualified[c.Qualified()] = c
		}
	}
	// A request another capability's package composes is a request no CALLER
	// composes, so it is not a delivery a surface could offer or decline. It is
	// the shape a capability reaches for when it delegates its whole job to a
	// second core and carries the caller's already-decided delivery along with
	// the request — the delivery travels with the delegation rather than being
	// re-decided by a second path written for the reporting case.
	//
	// 🔴 Excluding these is not a licence and it opens no escape. A capability
	// whose request is delegated still has its own caller-facing request in this
	// census, and that one is checked on every surface: what is excluded is the
	// inner carrier, one hop below the entry point a caller actually composes.
	// Wrapping a capability in a second core therefore hides nothing — it moves
	// the checked request outward, not away.
	//
	// Left in, each such carrier reports a finding whose own text is false: it
	// names a surface as offering the choice where that surface merely passes a
	// field it never sets, and it accuses a surface of declining a delivery no
	// caller can ask any surface for.
	delegated, err := deliveryDelegatedRequests(s, coreDirs, byQualified)
	if err != nil {
		return res, err
	}
	for _, qualified := range delegated {
		res.Delegated = append(res.Delegated, qualified)
		delete(byQualified, strings.SplitN(qualified, " ", 2)[0])
	}

	for _, c := range byQualified {
		res.Capabilities = append(res.Capabilities, c)
	}
	sort.Slice(res.Capabilities, func(i, j int) bool {
		return res.Capabilities[i].Qualified() < res.Capabilities[j].Qualified()
	})

	// The chokepoint census covers the surfaces as well as the cores, because a
	// surface that maps its own request to a delivery is a site the capability
	// census does not include and a reader checking the derivation needs to see.
	chokeDirs := append([]string(nil), coreDirs...)
	for _, surface := range surfaces {
		chokeDirs = append(chokeDirs, surface.Dirs...)
	}
	sort.Strings(chokeDirs)
	for i, dir := range chokeDirs {
		if i > 0 && dir == chokeDirs[i-1] {
			continue
		}
		sites, serr := deliveryChokepointsIn(s, dir, deliveryDir, deliveryFuncName)
		if serr != nil {
			return res, serr
		}
		res.Chokepoints = append(res.Chokepoints, sites...)
	}

	// Refused rather than reported. A read over a tree in which no capability
	// carries a delivery returns no finding and no error, so an empty capability
	// set is indistinguishable from a clean one from the outside: every surface
	// would be certified as complete against nothing at all.
	if len(res.Capabilities) == 0 {
		return res, fmt.Errorf("the capability derivation found no request type carrying a delivery selector "+
			"under %s, so a clean verdict over it would certify nothing: every surface would pass for composing "+
			"none of an empty set. Either %s no longer declares %s and %s, or no capability under that scope "+
			"carries the mode",
			coreScope, deliveryDir, deliveryTypeName, deliveryFuncName)
	}

	for _, surface := range surfaces {
		for _, dir := range surface.Dirs {
			offers, oerr := deliveryOffersIn(s, dir, surface.Name, byQualified)
			if oerr != nil {
				return res, oerr
			}
			res.Offers = append(res.Offers, offers...)
		}
	}
	sort.Slice(res.Offers, func(i, j int) bool {
		a, b := res.Offers[i], res.Offers[j]
		switch {
		case a.Capability != b.Capability:
			return a.Capability < b.Capability
		case a.Surface != b.Surface:
			return a.Surface < b.Surface
		case a.File != b.File:
			return a.File < b.File
		}
		return a.Line < b.Line
	})

	// The other empty derivation, and it is a different failure from the one
	// above rather than the same one inverted. The capability set can be
	// non-empty while no surface composes any of it — a renamed surface
	// directory, a corpus row pointing at a tree that no longer holds the
	// requests — and that state refuses every capability's offered set into
	// nothing while reporting no finding at all. So it is refused here: a read
	// that reached no surface composition has measured its own corpus, not the
	// tree.
	if len(res.Offers) == 0 {
		return res, fmt.Errorf("the offered-set derivation found no surface composing any of the %d capability "+
			"request(s) carrying a delivery, so no surface's offered set was read and a clean verdict would "+
			"certify nothing. Either the surface directories no longer hold the compositions, or the requests "+
			"are composed somewhere the corpus does not name",
			len(res.Capabilities))
	}

	for _, o := range res.Offers {
		if o.Offers {
			continue
		}
		res.Findings = append(res.Findings, DeliveryFinding{
			Kind:       DeliveryNotOffered,
			File:       o.File,
			Line:       o.Line,
			Surface:    o.Surface,
			Capability: o.Capability,
			Selector:   o.Selector,
			Offerers:   res.OfferedBy(o.Capability),
		})
	}

	return res, nil
}

// readDeliveryVocabulary reads the delivery type and mapping-function names from
// the package that declares them, and refuses a directory declaring neither.
//
// The names are read rather than spelled so a rename reaches this read from the
// declaration. What is spelled is the directory, and this is what keeps a stale
// spelling from passing: a wrong directory declares neither name, and the
// refusal says so instead of deriving nothing and reporting it clean.
func readDeliveryVocabulary(s *source, deliveryDir string) (deliveryType, mapper string, err error) {
	parsed, err := s.read(deliveryDir)
	if err != nil {
		return "", "", fmt.Errorf("read the delivery vocabulary: %w", err)
	}

	// The type is the one whose declared mapping function returns it, so the two
	// are read as a matched pair rather than as two names that happen to sit in
	// one package.
	var mapped string
	for _, file := range parsed.Files {
		for _, decl := range file.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Recv != nil || fn.Type.Results == nil || len(fn.Type.Results.List) != 1 {
				continue
			}
			if fn.Type.Params == nil || len(fn.Type.Params.List) != 1 {
				continue
			}
			if ident, isIdent := fn.Type.Params.List[0].Type.(*ast.Ident); !isIdent || ident.Name != "bool" {
				continue
			}
			result := typeName(fn.Type.Results.List[0].Type)
			if result == "" || typeQualifier(fn.Type.Results.List[0].Type) != "" {
				continue
			}
			mapped = result
			mapper = fn.Name.Name
		}
	}
	if mapper == "" {
		return "", "", fmt.Errorf("%s declares no function taking one boolean and returning one type declared "+
			"there, so the delivery vocabulary this read derives its capability census from is not in that "+
			"directory: either the directory is stale or the mapping function was renamed or removed",
			deliveryDir)
	}
	return mapped, mapper, nil
}

// deliveryCoreDirs lists the package directories a capability may be declared
// in: everything under coreScope that holds non-test Go source, less the
// surfaces themselves.
//
// A surface directory is excluded because a request type declared on a surface
// is that surface's own input shape rather than a capability's, so it has no
// second surface to be compared against.
func deliveryCoreDirs(root, coreScope string, surfaceDirs map[string]bool) ([]string, error) {
	var out []string
	walkErr := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() {
			return nil
		}
		name := d.Name()
		if path != root && (name == "testdata" || name == "vendor" || name == "node_modules" || strings.HasPrefix(name, ".")) {
			return filepath.SkipDir
		}
		rel, rerr := filepath.Rel(root, path)
		if rerr != nil {
			return rerr
		}
		dir := filepath.ToSlash(rel)
		if dir == "." || !strings.HasPrefix(dir, coreScope) || surfaceDirs[dir] {
			return nil
		}
		entries, derr := os.ReadDir(path)
		if derr != nil {
			return derr
		}
		for _, e := range entries {
			if e.IsDir() || !strings.HasSuffix(e.Name(), ".go") || strings.HasSuffix(e.Name(), "_test.go") {
				continue
			}
			out = append(out, dir)
			break
		}
		return nil
	})
	if walkErr != nil {
		return nil, fmt.Errorf("walk %s for capability packages: %w", root, walkErr)
	}
	sort.Strings(out)
	return out, nil
}

// deliveryCapabilitiesIn reads the request types dir declares that carry a
// delivery selector.
//
// Two shapes are read, and both are needed because the tree carries both. A
// field of the delivery type is the selector outright. A boolean field the
// package itself hands to the mapping function is a selector one step removed —
// the capability takes the caller's yes-or-no and maps it — and a read that only
// looked for the delivery type would report such a capability as carrying no
// delivery at all.
//
// 🔴 Neither shape is keyed on the field's NAME, deliberately. The boolean is
// spelled differently across the tree for capabilities that differ only in what
// they call the same choice, so a read keyed on a name would silently miss every
// capability that spelled it otherwise, and it would miss them in the direction
// that leaves a green.
func deliveryCapabilitiesIn(s *source, dir, deliveryDir, deliveryTypeName, deliveryFuncName string) ([]DeliveryCapability, error) {
	parsed, err := s.read(dir)
	if err != nil {
		return nil, err
	}

	structs := map[string]*ast.StructType{}
	for _, file := range parsed.Files {
		for _, decl := range file.Decls {
			gen, ok := decl.(*ast.GenDecl)
			if !ok {
				continue
			}
			for _, spec := range gen.Specs {
				ts, isType := spec.(*ast.TypeSpec)
				if !isType {
					continue
				}
				if st, isStruct := ts.Type.(*ast.StructType); isStruct {
					structs[ts.Name.Name] = st
				}
			}
		}
	}

	var out []DeliveryCapability

	for _, file := range parsed.Files {
		scope := s.packageDirsInScope(file)

		// A field of the delivery type.
		for name, st := range structs {
			for _, field := range st.Fields.List {
				if typeName(field.Type) != deliveryTypeName {
					continue
				}
				qualifier := typeQualifier(field.Type)
				if qualifier == "" {
					if dir != deliveryDir {
						continue
					}
				} else if scope[qualifier] != deliveryDir {
					continue
				}
				for _, ident := range field.Names {
					f, line := s.position(ident.Pos())
					out = append(out, DeliveryCapability{
						Package:  dir,
						Type:     name,
						Selector: ident.Name,
						File:     f,
						Line:     line,
					})
				}
			}
		}

		// A boolean this package maps itself.
		for _, decl := range file.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Body == nil {
				continue
			}
			bound := boundParameterTypes(fn)
			ast.Inspect(fn.Body, func(n ast.Node) bool {
				call, isCall := n.(*ast.CallExpr)
				if !isCall || len(call.Args) != 1 {
					return true
				}
				if !namesDeliveryFunc(call.Fun, scope, dir, deliveryDir, deliveryFuncName) {
					return true
				}
				selector, isSelector := call.Args[0].(*ast.SelectorExpr)
				if !isSelector {
					return true
				}
				receiver, isIdent := selector.X.(*ast.Ident)
				if !isIdent {
					return true
				}
				owner := bound[receiver.Name]
				st, declared := structs[owner]
				if !declared {
					return true
				}
				for _, field := range st.Fields.List {
					for _, ident := range field.Names {
						if ident.Name != selector.Sel.Name {
							continue
						}
						f, line := s.position(ident.Pos())
						out = append(out, DeliveryCapability{
							Package:  dir,
							Type:     owner,
							Selector: ident.Name,
							File:     f,
							Line:     line,
						})
					}
				}
				return true
			})
		}
	}

	sort.Slice(out, func(i, j int) bool {
		if out[i].Type != out[j].Type {
			return out[i].Type < out[j].Type
		}
		return out[i].Selector < out[j].Selector
	})
	return out, nil
}

// deliveryDelegatedRequests names the capability requests that another
// capability's package composes, each with the site composing it, sorted.
//
// The test is the package boundary and nothing softer: a request composed inside
// a core other than the one declaring it was composed by code answering for some
// other capability, which is what makes it a carrier rather than something a
// caller fills. A request composed only inside its OWN package is not delegated —
// a constructor or a default belongs to the capability itself.
func deliveryDelegatedRequests(s *source, coreDirs []string, capabilities map[string]DeliveryCapability) ([]string, error) {
	seen := map[string]string{}
	for _, dir := range coreDirs {
		parsed, err := s.read(dir)
		if err != nil {
			return nil, err
		}
		for _, file := range parsed.Files {
			scope := s.packageDirsInScope(file)
			ast.Inspect(file, func(n ast.Node) bool {
				lit, isLit := n.(*ast.CompositeLit)
				if !isLit || lit.Type == nil {
					return true
				}
				name := typeName(lit.Type)
				if name == "" {
					return true
				}
				owner := dir
				if qualifier := typeQualifier(lit.Type); qualifier != "" {
					resolved, known := scope[qualifier]
					if !known {
						return true
					}
					owner = resolved
				}
				if owner == dir {
					return true
				}
				capability, tracked := capabilities[owner+"."+name]
				if !tracked {
					return true
				}
				if _, held := seen[capability.Qualified()]; held {
					return true
				}
				f, line := s.position(lit.Lbrace)
				seen[capability.Qualified()] = fmt.Sprintf("%s composed at %s:%d", capability.Qualified(), f, line)
				return true
			})
		}
	}

	// Each entry begins with the qualified name, which is what the caller splits
	// on to drop the carrier from its census, so the site is carried in the same
	// string as the name rather than in a second structure keyed on it.
	out := make([]string, 0, len(seen))
	for _, entry := range seen {
		out = append(out, entry)
	}
	sort.Strings(out)
	return out, nil
}

// deliveryChokepointsIn lists the sites in dir that map a request to a delivery,
// as file:line: expression.
func deliveryChokepointsIn(s *source, dir, deliveryDir, deliveryFuncName string) ([]string, error) {
	parsed, err := s.read(dir)
	if err != nil {
		return nil, err
	}
	var out []string
	for _, file := range parsed.Files {
		scope := s.packageDirsInScope(file)
		ast.Inspect(file, func(n ast.Node) bool {
			call, isCall := n.(*ast.CallExpr)
			if !isCall || len(call.Args) != 1 {
				return true
			}
			if !namesDeliveryFunc(call.Fun, scope, dir, deliveryDir, deliveryFuncName) {
				return true
			}
			f, line := s.position(call.Lparen)
			out = append(out, fmt.Sprintf("%s:%d: %s(%s)", f, line, deliveryFuncName, exprText(call.Args[0])))
			return true
		})
	}
	sort.Strings(out)
	return out, nil
}

// deliveryOffersIn reads every composition of a capability's request in dir, and
// whether each names the selector.
func deliveryOffersIn(s *source, dir, surfaceName string, capabilities map[string]DeliveryCapability) ([]DeliveryOffer, error) {
	parsed, err := s.read(dir)
	if err != nil {
		return nil, err
	}

	var out []DeliveryOffer
	for _, file := range parsed.Files {
		scope := s.packageDirsInScope(file)
		ast.Inspect(file, func(n ast.Node) bool {
			lit, isLit := n.(*ast.CompositeLit)
			if !isLit || lit.Type == nil {
				return true
			}
			name := typeName(lit.Type)
			if name == "" {
				return true
			}
			owner := dir
			if qualifier := typeQualifier(lit.Type); qualifier != "" {
				resolved, known := scope[qualifier]
				if !known {
					return true
				}
				owner = resolved
			}
			capability, tracked := capabilities[owner+"."+name]
			if !tracked {
				return true
			}

			f, line := s.position(lit.Lbrace)
			out = append(out, DeliveryOffer{
				Surface:    surfaceName,
				Capability: capability.Qualified(),
				Selector:   capability.Selector,
				File:       f,
				Line:       line,
				Offers:     literalNamesField(lit, capability.Selector),
			})
			return true
		})
	}
	return out, nil
}

// literalNamesField reports whether a composite literal supplies the named
// field.
//
// A literal with elements and no keys supplies every field positionally, the
// named one included, so it offers the choice. An empty literal supplies
// nothing and offers none of it, which is the shape a surface reaches for when
// it composes a request and lets the zero value stand.
func literalNamesField(lit *ast.CompositeLit, field string) bool {
	if len(lit.Elts) == 0 {
		return false
	}
	keyed := false
	for _, elt := range lit.Elts {
		kv, isKV := elt.(*ast.KeyValueExpr)
		if !isKV {
			continue
		}
		keyed = true
		if ident, isIdent := kv.Key.(*ast.Ident); isIdent && ident.Name == field {
			return true
		}
	}
	return !keyed
}

// namesDeliveryFunc reports whether a call expression names the delivery mapping
// function, whether qualified by the declaring package's import or called from
// inside that package.
func namesDeliveryFunc(fun ast.Expr, scope map[string]string, dir, deliveryDir, deliveryFuncName string) bool {
	switch e := fun.(type) {
	case *ast.Ident:
		return dir == deliveryDir && e.Name == deliveryFuncName
	case *ast.SelectorExpr:
		if e.Sel.Name != deliveryFuncName {
			return false
		}
		qualifier, isIdent := e.X.(*ast.Ident)
		if !isIdent {
			return false
		}
		return scope[qualifier.Name] == deliveryDir
	}
	return false
}

// boundParameterTypes maps each name a function's receiver and parameters
// introduce to the bare name of its type, so a field access on one resolves to
// the struct that declares the field.
func boundParameterTypes(fn *ast.FuncDecl) map[string]string {
	bound := map[string]string{}
	add := func(fields *ast.FieldList) {
		if fields == nil {
			return
		}
		for _, field := range fields.List {
			name := typeName(field.Type)
			if name == "" {
				continue
			}
			for _, ident := range field.Names {
				bound[ident.Name] = name
			}
		}
	}
	add(fn.Recv)
	add(fn.Type.Params)
	return bound
}

// exprText renders the shapes a delivery request is written in, so the
// chokepoint census reads as the code does. Anything else is rendered as a
// placeholder rather than approximated, because a census that guessed at an
// expression would print something the tree does not say.
func exprText(expr ast.Expr) string {
	switch e := expr.(type) {
	case *ast.Ident:
		return e.Name
	case *ast.SelectorExpr:
		return exprText(e.X) + "." + e.Sel.Name
	case *ast.UnaryExpr:
		return e.Op.String() + exprText(e.X)
	}
	return "…"
}
