// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package main

import (
	"fmt"
	"io"
	"strings"

	"github.com/spf13/cobra"
	"go.yaml.in/yaml/v3"

	"github.com/aggrega-ai/aggrega-cli/internal/authoredcontent"
	"github.com/aggrega-ai/aggrega-cli/internal/editops"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// newEditDocumentCmd builds the edit-document command, which applies one
// bounded structured edit to a document in the workspace and preserves every
// byte the edit does not touch.
//
// The edit itself — where the entry goes, how it is rendered, the splice, and
// the validation the write is gated on — comes from the shared editing layer
// every front end drives, so the bytes a person gets at a terminal are the bytes
// an editor gets from a session and an assistant gets from the tool protocol.
// This command names the edit and passes the request through; it decides nothing
// about what may be applied.
func newEditDocumentCmd() *cobra.Command {
	opts := &editDocumentFlags{}
	c := &cobra.Command{
		Use:   "edit-document <edit> <file>",
		Short: "Apply one bounded structured edit to a document, preserving every byte it does not touch",
		Long: "Apply one bounded structured edit to a document in the workspace.\n\n" +
			"The edit is named first and the document second. One call adds one entry under one " +
			"container, or sets one scalar, and it can do nothing else:\n\n" +
			"  add-component   add a component instance under a components container\n" +
			"  attach-feature  attach a Feature to the node you name\n" +
			"  wire-edge       wire a connection inside one of that node's sockets\n" +
			"  set-value       set one value on that node\n\n" +
			"There is no edit that deletes, replaces a block you already wrote, reorders anything, " +
			"or writes bytes you supply verbatim. The edits this command applies are " +
			editDocumentKindList() + ", and a name outside that set is refused rather than " +
			"interpreted.\n\n" +
			"Name the node with --node, once per path segment, from the document root. It is a " +
			"segment at a time rather than one dotted string because a dot is a legal character " +
			"in a name you type, so a joined path could address a key nobody wrote and the entry " +
			"would be created there rather than refused:\n\n" +
			"  aggrega edit-document add-component app.yaml \\\n" +
			"    --node spec --node components --name web --value example/service\n\n" +
			"  aggrega edit-document wire-edge app.yaml \\\n" +
			"    --node spec --node components --node web \\\n" +
			"    --socket database --name db --value spec.components.store\n\n" +
			"For add-component the path names the components container itself, because the entry " +
			"being added is the node. For the other three it names the node the edit is about, and " +
			"the container follows from the edit.\n\n" +
			"--name is the key the entry is written under: the instance name, the attachment's " +
			"name, the connection's alias, or the value's key. --value is what that entry carries, " +
			"and what it means follows from the edit — the entity the entry requests, the node a " +
			"connection points at, or the scalar a value is set to. It is read as a YAML scalar, " +
			"so --value 3 sets a number, --value true a boolean, and --value '\"3\"' the string; " +
			"omitting it sets nothing at all.\n\n" +
			"Every byte the edit does not touch survives. The document is parsed only to locate the " +
			"container and the line its block ends at, so comments, blank lines, quoting style, " +
			"folded and literal scalars and the trailing-newline shape all carry through, and the " +
			"nesting step is measured from the document's own indentation rather than imposed. The " +
			"same request against the same document produces the same bytes.\n\n" +
			"The result has to be a document you could have typed, and that is what the write is " +
			"gated on: the composed document is checked against its own kind's schema and reaches " +
			"disk only if that passes. A request that would produce something the schema refuses is " +
			"refused, naming the property the schema would not admit and where, and the file is left " +
			"byte-for-byte as it was — so a refused edit is indistinguishable from one never " +
			"attempted and you can retry a corrected request against the document you started " +
			"with.\n\n" +
			"An entry that is already there, carrying what you asked for, is a no-op. An entry of " +
			"that name carrying something else is refused rather than replaced.\n\n" +
			"With --report the edited document is written to standard output as content, naming the " +
			"file it is for, and nothing is created — for a caller that puts the result in front of " +
			"someone before it is saved. Without it the document is written.\n\n" +
			"Whether what you wire is semantically admissible is not decided here. Whether a socket " +
			"is one the producer declares, or whether a Feature's applicability is satisfied, is " +
			"what aggrega validate answers.",
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			rw, err := workspaceFromContext(cmd.Context())
			if err != nil {
				return err
			}
			return editDocumentRun(editDocumentConfig{
				Edit: args[0],
				File: args[1],
				Node: opts.node,

				Socket: opts.socket,
				Name:   opts.name,
				Value:  opts.value,
				// Read from the flag set rather than from the value, so a
				// deliberate empty value is distinguishable from an absent one.
				ValueGiven: cmd.Flags().Changed("value"),
				Report:     opts.report,

				ws:  rw,
				Out: cmd.OutOrStdout(),
			})
		},
	}
	c.Flags().StringArrayVar(&opts.node, "node", nil,
		"One segment of the structural path to the node the edit is about, from the document root. Repeat it once per segment, outermost first. It takes a segment at a time rather than one dotted path because a dot is a legal character in a name you type, so a joined path could address a key nobody wrote")
	c.Flags().StringVar(&opts.socket, "socket", "",
		"The dependency socket a connection is wired inside. Read for wire-edge and ignored otherwise")
	c.Flags().StringVar(&opts.name, "name", "",
		"The key the new entry is written under: the component instance name, the attachment's name, the connection's alias, or the value's key")
	c.Flags().StringVar(&opts.value, "value", "",
		"What the entry carries, read as a YAML scalar. Its meaning follows from the edit: the entity the entry requests, the node a connection points at, or the scalar a value is set to")
	c.Flags().BoolVar(&opts.report, "report", false,
		"Report the edited document as content, naming the file it is for, and write nothing. Without it the document is written")
	return c
}

// editDocumentFlags carries the command's flag state. It is constructed per
// command so two invocations in one process cannot observe each other's flag
// values.
type editDocumentFlags struct {
	node   []string
	socket string
	name   string
	value  string
	report bool
}

// editDocumentConfig is the input contract for `aggrega edit-document`.
type editDocumentConfig struct {
	// Edit names which of the bounded edits to apply.
	Edit string
	// File names the document, relative to the workspace root or by absolute
	// path.
	File string
	// Node is the structural path to the node the edit is about, from the
	// document root, one element per segment. It is a list rather than a joined
	// string for the reason every surface carrying this path keeps it a list: a
	// dot is legal inside an authored name, so nothing outside the document can
	// tell a structural dot from one inside a name.
	Node []string
	// Socket is the dependency socket a connection is wired inside.
	Socket string
	// Name is the key the new entry is written under.
	Name string
	// Value is what the entry carries, as the caller spelled it. It is resolved
	// as a YAML scalar rather than taken as text, so the value a request carries
	// is the value the same characters would carry had they been typed into the
	// document.
	Value string
	// ValueGiven says whether a value was supplied at all, which the text cannot
	// convey: an omitted value and one set to the empty string are different
	// requests and only the flag set knows which arrived.
	ValueGiven bool
	// Report asks for the edited document as content instead of as a write.
	Report bool

	ws *workspaceconfig.ResolvedWorkspace
	// Out carries the answer: the reported document, or what reached disk.
	Out io.Writer
}

// editDocumentRun binds the command's arguments to the shared editing layer and
// renders what it did.
//
// The composition, the bound and the re-validation live in that layer, so this
// surface and a session perform one edit rather than each performing its own.
// Nothing here decides where an entry goes or whether the result is admissible.
func editDocumentRun(cfg editDocumentConfig) error {
	rw, err := workspaceOrResolve(cfg.ws)
	if err != nil {
		return fmt.Errorf("workspaceconfig: %w", err)
	}
	value, err := editDocumentValue(cfg.Value, cfg.ValueGiven)
	if err != nil {
		return err
	}
	delivery := authoredcontent.DeliveryFor(cfg.Report)
	res, err := editops.Apply(rw.Root(), editops.Options{
		File:     cfg.File,
		Kind:     editops.Kind(cfg.Edit),
		Node:     cfg.Node,
		Socket:   cfg.Socket,
		Name:     cfg.Name,
		Value:    value,
		Delivery: delivery,
	})
	if err != nil {
		return err
	}
	return writeEditDocumentOutcome(cfg.Out, delivery, res)
}

// editDocumentValue resolves what the caller spelled into the value the entry
// carries.
//
// The text is resolved as a YAML scalar rather than passed through as a string,
// and that is what keeps this surface from narrowing the capability. A value may
// be a string, a boolean, a number or nothing at all; a command line carries
// only characters, so without a resolution step every value a terminal could set
// would be a string and the surface would silently lack a shape every other
// surface has. Resolving through the document's own language means the value a
// request carries is the value those same characters would carry had they been
// typed into the document — quote them and it is text, as it is in the document.
//
// An absent value is nothing at all rather than the empty string, because the
// two are different requests and only the flag set knows which arrived.
func editDocumentValue(text string, given bool) (any, error) {
	if !given {
		return nil, nil
	}
	var v any
	if err := yaml.Unmarshal([]byte(text), &v); err != nil {
		return nil, fmt.Errorf("--value %q is not a well-formed scalar: %w", text, err)
	}
	return v, nil
}

// writeEditDocumentOutcome renders what an edit did on the command's own output
// stream.
//
// Under a reporting delivery the stream carries the reported document and
// nothing else, written by the reported form's own writer so this surface does
// not compose a second framing of it.
//
// Under a writing delivery there are two outcomes and they are reported
// differently on purpose. A write names the file that reached disk. An edit whose
// entry was already there, carrying what was asked, wrote nothing — and saying
// nothing at all would leave that indistinguishable from a write that failed
// quietly, so it says which document already carried the entry.
//
// Both writing outcomes then say where the entry sits, and an edit that had to
// bring container levels into being says which ones. A person at a terminal has
// nothing else that will tell them: the answer stream is all they are shown, and
// an edit that quietly wrote two nested blocks the document did not carry is
// otherwise indistinguishable from one that added a single entry to a block the
// author wrote. Where the whole container path was already there — the ordinary
// case — there is nothing created to name and the second sentence is absent.
func writeEditDocumentOutcome(out io.Writer, delivery authoredcontent.Delivery, res editops.Result) error {
	if out == nil {
		return nil
	}
	if delivery.Reports() {
		return authoredcontent.Report(out, res.Documents)
	}
	if len(res.Written) == 0 {
		for _, doc := range res.Documents {
			fmt.Fprintf(out, "%s already carries that entry; nothing was written\n", doc.Path)
		}
	} else {
		for _, name := range res.Written {
			fmt.Fprintf(out, "wrote %s\n", name)
		}
	}
	if len(res.Container) != 0 {
		fmt.Fprintf(out, "  the entry sits under %s\n", editDocumentPathText(res.Container))
	}
	if len(res.CreatedContainers) != 0 {
		fmt.Fprintf(out, "  this edit created %s, which the document did not carry\n",
			editDocumentPathText(res.CreatedContainers))
	}
	return nil
}

// editDocumentPathText renders a structural path for a person to read.
//
// The segments are joined with dots, which is the spelling every other sentence
// about a place in a document already uses — a refusal naming the container an
// entry could not go in spells it that way, so a reader who has seen one
// recognises the other rather than learning a second notation.
//
// Requesting a path is the opposite case and takes segments one at a time, since
// a dot is legal inside a name an author types and a joined request could
// address a key nobody wrote. Nothing is parsed back out of this, so the reading
// aid costs no ambiguity: it is shown, never taken.
func editDocumentPathText(segments []string) string {
	return strings.Join(segments, ".")
}

// editDocumentKindList renders the admissible edits for the help text, so the
// sentence stating the bound names the set the shared layer actually applies.
//
// The list comes from that layer's own enumeration rather than from a literal
// here: a restatement would be a second statement of the bound, and the two
// would agree until a kind was added to one of them.
//
// The block of descriptions above it in the help is a DELIBERATE near-duplicate
// and is recorded as one. It says what each edit does, which the enumeration
// cannot yield — the enumeration carries names — so it is prose that teaches
// rather than a second statement of the bound, and the derived sentence beside
// it is what a reader can rely on if the two ever disagree.
func editDocumentKindList() string {
	kinds := editops.Kinds()
	names := make([]string, 0, len(kinds))
	for _, k := range kinds {
		names = append(names, string(k))
	}
	return strings.Join(names, ", ")
}
