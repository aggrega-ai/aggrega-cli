// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package gotmpl

import (
	"sort"
	"strings"
	"text/template/parse"

	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// CollectTemplateFieldAccesses parses a Go template source with the
// default `{{ }}` delimiters and returns the set of dot-qualified paths
// it references (e.g., ".values.port", ".infra.replicas",
// ".context.inject.env.X"). Thin wrapper over
// CollectTemplateFieldAccessesWithDelims preserving the default-delimiter
// call shape for ghost-infrastructure tracking and other callers that
// don't carry custom delimiters.
//
// Empty template text returns (nil, nil). Parse errors are returned so
// callers can decide whether to surface them (normal rendering already
// reports parse errors — tracker callers typically swallow them and skip
// warning emission).
func CollectTemplateFieldAccesses(tmplText string) ([]string, error) {
	return CollectTemplateFieldAccessesWithDelims(tmplText, "{{", "}}")
}

// CollectTemplateFieldAccessesWithDelims parses a Go template with
// caller-controlled delimiters and returns the dot-qualified field-access
// set. Used by the artifact pre-check when an external-handler engine
// (today: helm-chart) declares non-default delimiters via
// ArtifactsTemplateDelims — the pre-check must parse with the same
// delimiters Aggrega's pre-templating uses, so downstream-template syntax
// inside the file (e.g., Helm's `{{ ... }}` blocks when the engine's
// Aggrega-side delimiters are `[[ ]]`) is treated as opaque text rather
// than as parse-error-inducing template directives.
func CollectTemplateFieldAccessesWithDelims(tmplText, leftDelim, rightDelim string) ([]string, error) {
	if tmplText == "" {
		return nil, nil
	}
	// The tracker parses through the same helper the reference checks use, so a
	// template referencing catalog functions (trimSuffix, toJson, until, int,
	// default, …) parses here identically to the way it renders. Go's parser fails
	// on any undefined function identifier, so a tracker parsing with a bare
	// funcmap would reject templates the evaluator accepts — which is why the parse
	// has one home rather than one copy per surface. The funcmap only affects
	// whether Parse succeeds; the FieldNode/ChainNode walk below is unchanged.
	root, err := ParseTree(tmplText, leftDelim, rightDelim)
	if err != nil {
		return nil, err
	}
	return TreeFieldAccesses(root), nil
}

// TreeFieldAccesses returns the dot-qualified field-access set of a template that
// is already parsed, by the same over-collecting walk CollectTemplateFieldAccesses
// runs over authored text. It serves a caller holding the very tree it is about to
// execute — or has just executed — so the references it reads are the ones that
// tree makes, under the delimiters and the function set it was parsed with, rather
// than those of a second parse that could differ from it.
//
// A template handing on the whole of its data — `toJson .`, a bare `$` — names no
// field, so it contributes nothing to this set; TreeDataReads is the reading that
// reports it.
func TreeFieldAccesses(root parse.Node) []string {
	return walkCensus(root).fieldPaths()
}

// dataRoot is how TreeDataReads spells a read of the whole of the data a template
// was handed: the dot with nothing after it.
const dataRoot = "."

// TreeDataReads returns what a parsed template reads of the data it was handed,
// for a reading whose job is to WITHHOLD something: every path TreeFieldAccesses
// returns, and the data root itself, spelled ".", wherever the template hands on
// the whole of that data — a bare dot while the dot is still that data, or a bare
// $ anywhere. `{{ fail (toJson .) }}` reads every datum it was given without
// naming one, so a reading that answered from the named paths alone would call it
// harmless.
//
// The two readings come from ONE walk and differ only in whether a read naming no
// field is reported, which is not a choice of direction: both over-collect. The
// readers of the field-access set ask about named paths — which pillar a reference
// names, which keys no template reads, which paths a Recipe reads — and an entry
// naming no path answers none of those questions, so it is reported only to the
// readings that ask whether a datum can reach what a template produces.
//
// A dot rebound by a range or a with body is not the data: it is the value its
// pipeline named, and every path that pipeline reads is recorded already, so a bare
// dot there contributes nothing of its own. A defined template's body is walked as
// its own tree, with its dot standing for whatever an invocation hands it; so the
// whole data handed to an invocation — `{{ template "name" . }}` — is recorded at
// the invocation, which covers a reader that walks only the tree it parsed.
func TreeDataReads(root parse.Node) []string {
	c := walkCensus(root)
	paths := c.fieldPaths()
	if c.wholeData {
		paths = append([]string{dataRoot}, paths...)
	}
	return paths
}

// census is what one walk of a parsed template records: every dot-qualified field
// access, and whether the template hands on the whole of the data it was given.
type census struct {
	paths     map[string]bool
	wholeData bool
}

// walkCensus walks a parsed template from its root, where the dot is the data the
// template was handed.
func walkCensus(root parse.Node) *census {
	c := &census{paths: map[string]bool{}}
	collectFromNode(root, c, true)
	return c
}

// fieldPaths returns the recorded field accesses, sorted.
func (c *census) fieldPaths() []string {
	paths := make([]string, 0, len(c.paths))
	for p := range c.paths {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	return paths
}

// collectFromNode walks a parse.Node tree and records every field access it
// encounters, wherever it sits. Dot-qualified paths are joined with "." and
// prefixed with ".". The structure is walked by [WalkPipelines], the one walk
// every reader needing each pipeline shares; what is recorded from each pipeline
// is this census's own, in [census.recordPipe].
//
// This walk deliberately OVER-collects, and the direction is load-bearing rather
// than incidental. It records a field read inside a range or with body, where the
// dot names an element rather than the context root, and it records both halves
// of a chained expression. Two consumers want exactly that: a read-set census
// reports a key hidden inside a rebound body as read, and a check that must
// withhold something cannot let the thing hide inside a rebound body. Neither
// consumer may be given the under-collecting direction, and this walk must never
// grow a flag that would let a caller ask for it.
//
// dotIsData reports whether the dot at n is still the data the template was handed:
// true at the root, through an if and every else branch, and in the pipeline a range
// or a with evaluates; false in a range or with body, where the dot is rebound.
func collectFromNode(n parse.Node, c *census, dotIsData bool) {
	walkPipelinesFrom(n, dotIsData, c.recordPipe)
}

// collectFromPipe records what one pipeline, and every pipeline inside its
// arguments, reads.
func collectFromPipe(p *parse.PipeNode, c *census, dotIsData bool) {
	walkPipeline(p, dotIsData, c.recordPipe)
}

// recordPipe records what one pipeline's arguments read, apart from the pipelines
// inside them, which the walk visits on their own.
func (c *census) recordPipe(p *parse.PipeNode, dotIsData bool) {
	for _, cmd := range p.Cmds {
		if cmd == nil {
			continue
		}
		for _, arg := range cmd.Args {
			collectFromArg(arg, c, dotIsData)
		}
	}
}

// collectFromArg records what one argument reads. A pipeline among the arguments,
// or as a chain's receiver, contributes nothing here: the walk visits it as a
// pipeline of its own, with the same dot, and records it there.
func collectFromArg(arg parse.Node, c *census, dotIsData bool) {
	switch a := arg.(type) {
	case *parse.DotNode:
		// A bare dot hands on whatever the dot is. Where that is still the data the
		// template was handed, every datum in it is read; where a range or a with
		// rebound it, the pipeline that named the value is recorded already.
		if dotIsData {
			c.wholeData = true
		}
	case *parse.FieldNode:
		if len(a.Ident) > 0 {
			c.paths["."+strings.Join(a.Ident, ".")] = true
		}
	case *parse.VariableNode:
		// $ names the context root under any rebinding, so $.values.x is a read of
		// the same datum .values.x is and is recorded under the same spelling — the
		// one spelling every consumer of this census compares against, and a bare $
		// hands on the whole of the data. Any other variable names a value the
		// template itself declared, and what it was bound from is recorded where it
		// was bound.
		if len(a.Ident) > 0 && a.Ident[0] == "$" {
			if len(a.Ident) == 1 {
				c.wholeData = true
				return
			}
			c.paths["."+strings.Join(a.Ident[1:], ".")] = true
		}
	case *parse.ChainNode:
		// Both halves are recorded. The trailing fields hang off the receiver's
		// value rather than off the dot, and the receiver is itself an expression
		// that can reach an authored key — so a defensive wrapper such as
		// (default dict .values.tls).ca has read .values.tls, and a census that
		// recorded only the trailing .ca would report that key unread and would
		// let it hide from a check that must withhold it.
		if len(a.Field) > 0 {
			c.paths["."+strings.Join(a.Field, ".")] = true
		}
		// A receiver that is a pipeline is visited by the walk; any other receiver is
		// an argument in its own right.
		collectFromArg(a.Node, c, dotIsData)
	}
}

// UnreadKeys returns top-level keys of `data` whose `<root>.<key>` path is
// NOT referenced by any entry in `references`. Used to emit "unused key"
// warnings for Ghost Infrastructure — catalog authors add infra/values entries
// that the Recipe's valuesMapping never consumes.
func UnreadKeys(references []string, root string, data types.OrderedMap[any]) []string {
	if data.Len() == 0 {
		return nil
	}
	// Reference paths begin with ".". Build the "readset" of top-level subkeys
	// of root that appear in any reference. A reference like ".values.port"
	// marks "port" as read. A reference like ".values" alone (whole-object
	// iteration via `range`) marks every key as read.
	rootWithDot := root
	if !strings.HasPrefix(rootWithDot, ".") {
		rootWithDot = "." + rootWithDot
	}
	readAll := false
	readset := map[string]bool{}
	for _, ref := range references {
		if ref == rootWithDot {
			readAll = true
			break
		}
		if !strings.HasPrefix(ref, rootWithDot+".") {
			continue
		}
		rest := strings.TrimPrefix(ref, rootWithDot+".")
		// Take only the first segment — nested reads (e.g. ".values.db.port") mark "db" as read.
		seg := rest
		if i := strings.Index(rest, "."); i >= 0 {
			seg = rest[:i]
		}
		readset[seg] = true
	}
	if readAll {
		return nil
	}
	var unread []string
	for _, key := range data.Keys() {
		if !readset[key] {
			unread = append(unread, key)
		}
	}
	sort.Strings(unread)
	return unread
}
