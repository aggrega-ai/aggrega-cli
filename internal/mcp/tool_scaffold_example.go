// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"oras.land/oras-go/v2"

	"github.com/aggrega-ai/aggrega-cli/internal/docsbundle"
	"github.com/aggrega-ai/aggrega-cli/internal/exampleops"
	"github.com/aggrega-ai/aggrega-cli/internal/opsurface"
	"github.com/aggrega-ai/aggrega-cli/pkg/docscache"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// docScaffoldReference is the page teaching the scaffold capability, both of its
// forms. A failure to write or list an example points here, because the fix — a
// different directory, a workspace that trusts the documentation's signer, a
// connection or a cached copy — is taught there rather than on any entity kind's
// page.
const docScaffoldReference = docIndex + "/cli/aggrega-scaffold.md"

// scaffoldReach reports how far a call to the scaffold tool would reach: nothing,
// which is what drafting declares, or the workspace where the call writes an
// example into it.
//
// It reads the form from the shared rule every surface taking the capability's
// fields reads (exampleops.RequestForm), so the class a caller is admitted against
// is the form the tool then runs. A call this cannot read, or one the rule refuses,
// is one the tool refuses before anything is written, so neither leaves a write to
// be admitted.
func scaffoldReach(_ *server, args json.RawMessage) opsurface.Reach {
	var a scaffoldArgs
	if err := json.Unmarshal(args, &a); err != nil {
		return opsurface.ReachNone
	}
	form, err := exampleops.RequestForm(a.Kind, a.Name, a.Example, a.Dir, a.List)
	if err != nil || form != exampleops.FormWrite {
		return opsurface.ReachNone
	}
	return opsurface.ReachWorkspace
}

// scaffoldExample answers the scaffold tool's example form: the list of the
// documentation's runnable examples, or one of them written into a new directory.
//
// The directory is resolved by the one helper every path a call names is resolved
// by, before anything is read, so a directory this transport does not let the call
// name is refused with nothing fetched and nothing written; it defaults to a
// directory named after the example, relative to the workspace root. The list, the
// write and every refusal of it — a directory holding anything, a bundle carrying
// a link or an entry that would land outside the directory, a bundle the
// workspace's signature check does not admit, an offline workspace with nothing
// cached — are the shared example layer's, so this tool, the command line and the
// editor daemon write the same files and refuse the same things.
//
// The bundle is the one this server's documentation is read from: its version
// unless a test pinned another, kept under the cache root the session settled, and
// fetched through the target the run stated or the workspace's own registry and
// egress settings.
func (s *server) scaffoldExample(ctx context.Context, form exampleops.Form, a scaffoldArgs) (*callToolResult, *rpcError) {
	tw, err := s.resolveToolWorkspace(a.WorkspaceRoot, nil)
	if err != nil {
		return errorResult(explainToolError("Could not resolve the workspace to write the example in.", err, ""))
	}

	var name, named, dest string
	if form == exampleops.FormWrite {
		name = strings.TrimSpace(a.Example)
		if err := exampleops.CheckName(name); err != nil {
			return errorResult(explainToolErrorAt("The scaffold tool's 'example' names no example.", err,
				docScaffoldReference))
		}
		named = strings.TrimSpace(a.Dir)
		if named == "" {
			named = name
		}
		var refusal string
		if dest, refusal = tw.workspacePath("scaffold", "dir", named); refusal != "" {
			return errorResult(refusal)
		}
	}

	cacheRoot, err := s.docsCacheRoot()
	if err != nil {
		return tw.answer(errorResult(explainToolErrorAt("Could not read the documentation's examples.", err,
			docScaffoldReference)))
	}
	docsCtx := docsbundle.WithCacheRoot(s.docsDecl.Onto(ctx), cacheRoot)
	if s.docsTarget != nil {
		docsCtx = docsbundle.WithTarget(docsCtx, func(_ workspaceconfig.Config, ref, version string, warn func(string)) (oras.ReadOnlyTarget, docscache.VerifyAtPull, error) {
			return s.docsTarget(tw, ref, version, warn)
		})
	}
	_, version := s.docsBundleCoords(tw)
	// Acquisition warnings report on how the documentation was obtained, which is
	// not what the call asked about, so they go to the log stream; the fallback to
	// an earlier version is about the answer, and opens its text.
	warn := func(notice string) {
		if s.logw != nil {
			fmt.Fprintf(s.logw, "mcp: scaffold: %s\n", notice)
		}
	}
	examples, err := exampleops.Open(docsCtx, tw.cfg, version, warn)
	if err != nil {
		return tw.answer(errorResult(explainToolErrorAt("Could not read the documentation's examples.", err,
			docScaffoldReference)))
	}
	note := ""
	if n := examples.Note(); n != "" {
		note = "note: " + n + "\n\n"
	}

	if form == exampleops.FormList {
		list, err := examples.List()
		if err != nil {
			return tw.answer(errorResult(explainToolErrorAt(fmt.Sprintf(
				"The documentation bundle %s@%s carries no readable list of examples.", examples.Ref,
				examples.Version), err, docScaffoldReference)))
		}
		out := make([]scaffoldExample, 0, len(list))
		for _, e := range list {
			out = append(out, scaffoldExample{Name: e.Name, Level: e.Level, Summary: e.Summary})
		}
		return tw.answer(textResult(note+exampleops.ListReport(list), scaffoldResult{Examples: out}))
	}

	written, err := examples.Write(name, dest)
	if err != nil {
		action := "Could not write the example " + name + " into " + named + "."
		if errors.Is(err, docscache.ErrPageNotFound()) {
			action += " Send `list: true` to be answered with the examples there are."
		}
		return tw.answer(errorResult(explainToolErrorAt(action, err, docScaffoldReference)))
	}
	written.Dir = named
	return tw.answer(textResult(note+written.Report(), scaffoldResult{
		Example: written.Name,
		Dir:     named,
		Files:   written.Files,
		Next:    written.Next,
		Version: examples.Version,
	}))
}
