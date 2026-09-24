// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package entitypin

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"go.yaml.in/yaml/v3"

	"github.com/aggrega-ai/aggrega-cli/internal/dryrunreport"
	"github.com/aggrega-ai/aggrega-cli/pkg/catalog"
	"github.com/aggrega-ai/aggrega-cli/pkg/recipepin"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// Options is what one run of the pin step over a named entity takes.
type Options struct {
	// Workspace is the resolved workspace the caller threads in. Required: the
	// step refuses a nil rather than resolving one of its own, because resolving
	// here would make where it fetches into depend on a directory no caller named.
	Workspace *workspaceconfig.ResolvedWorkspace

	// EntityPath names the entity document to pin.
	EntityPath string

	// DryRun computes what the step would write and writes nothing: the lock the
	// step would leave comes back in Result.Report instead of landing on disk.
	// The resolution still runs, so the content cache still fills — which is
	// what makes the report describe a real effect rather than an empty one.
	DryRun bool
}

// Result is what one run of the pin step over a named entity reports.
type Result struct {
	// EntityPath is the entity document, as an absolute path.
	EntityPath string

	// Kind and Name identify the entity as its document declares it.
	Kind string
	Name string

	// Pinnable reports whether an entity of this kind reads anything a lock
	// records a pin for. A kind that does not has nothing for the step to write,
	// and no lock beside it is touched.
	Pinnable bool

	// Outcome is what the step did, or would do, to the lock. It is meaningful
	// only where Pinnable is set.
	Outcome Outcome

	// Report is, on a dry run, the file the step would write — nil where the lock
	// would not move, and a file with no content where it would be removed.
	Report *dryrunreport.File
}

// pinnedEntityHeader is the part of an entity document the step reads before it
// knows what the document is: the kind that decides whether there is anything to
// pin, and the identity the catalog resolves the entity by.
type pinnedEntityHeader struct {
	Kind     string `yaml:"kind"`
	Metadata struct {
		Name    string `yaml:"name"`
		Version string `yaml:"version"`
	} `yaml:"metadata"`
	Spec struct {
		Capability string `yaml:"capability"`
	} `yaml:"spec"`
}

// Pin runs the pin step over the entity document at opts.EntityPath: it records,
// in the lock beside it, the pin for every input the entity reads from outside
// its own document, and needs no Release to do so.
//
// It reads the entity through a catalog view over the entity's own directory,
// which is what a writable home is for a document named by path: the walk
// records the file it read, so the one predicate every caller of the step reads
// is satisfied here without a case of its own. It then runs [Compute] and writes
// through the plan, which is the one path every caller that pins reaches.
//
// An entity of a kind that reads nothing a lock records — a Component, say —
// reports that it is not pinnable and touches nothing.
func Pin(ctx context.Context, opts Options) (Result, error) {
	res, plan, err := planNamed(ctx, opts, true)
	if err != nil || !res.Pinnable {
		return res, err
	}

	if opts.DryRun {
		outcomes, files, err := plan.Report()
		if err != nil {
			return Result{}, err
		}
		res.Outcome = outcomes[0]
		if len(files) == 1 {
			f := files[0]
			res.Report = &f
		}
		return res, nil
	}
	outcomes, err := plan.Write()
	if err != nil {
		return Result{}, err
	}
	res.Outcome = outcomes[0]
	return res, nil
}

// Hold runs the pin step's resolution over the entity document at
// opts.EntityPath and holds the lock beside it to what the step would record:
// it returns the unpinned-entity halt, [recipepin.ErrEntityNotPinned], where the
// lock records no pin for an input the entity reads or one the input no longer
// matches, and nil where it records every one. It writes nothing whatever it
// finds, so opts.DryRun changes nothing here.
//
// It is the plan [Pin] computes, read the third way a plan is read — checked
// rather than written or reported — so what it refuses is exactly what a
// writing run of the step would have written, and a lock that run wrote is one
// it accepts. remedy is what the operation holding the lock can do instead, in
// its own words; the condition and its composition are the one halt every
// operation that holds a lock raises.
//
// An entity of a kind that reads nothing a lock records has nothing to hold, and
// Hold returns nil for it without resolving anything.
//
// Hold writes nothing, so it claims no lock file and does not itself hold the
// entity to a directory of its own; the publication that calls it has already
// asked [RefuseSharedDirectory] over the same document.
func Hold(ctx context.Context, opts Options, remedy recipepin.Remedy) error {
	res, plan, err := planNamed(ctx, opts, false)
	if err != nil || !res.Pinnable {
		return err
	}
	return plan.Unpinned(remedy)
}

// planNamed reads the entity document at opts.EntityPath and, where it is a
// kind that reads anything a lock records, computes the step's plan for it.
//
// It is the one place a named document becomes a plan, shared by the step that
// writes it and the hold that checks it, so the two cannot come to read an
// entity through different views or resolve its sources differently. It reads
// the entity through a catalog view over the entity's own directory, which is
// what a writable home is for a document named by path: the walk records the
// file it read, so the one predicate every caller of the step reads is
// satisfied here without a case of its own. The returned Result is filled in
// as far as reading the document goes; its Outcome is the caller's to fill.
// forWriting is whether the caller will write or report the plan, which is what
// holds the entity to a directory of its own before anything is resolved.
func planNamed(ctx context.Context, opts Options, forWriting bool) (Result, Plan, error) {
	rw := opts.Workspace
	if rw == nil {
		return Result{}, Plan{}, fmt.Errorf("internal: no resolved workspace supplied — the caller must resolve one and pass it in")
	}
	if strings.TrimSpace(opts.EntityPath) == "" {
		return Result{}, Plan{}, fmt.Errorf("an entity document to pin is required")
	}
	abs, err := filepath.Abs(opts.EntityPath)
	if err != nil {
		return Result{}, Plan{}, fmt.Errorf("resolve %s: %w", opts.EntityPath, err)
	}
	data, err := os.ReadFile(abs)
	if err != nil {
		return Result{}, Plan{}, fmt.Errorf("read %s: %w", opts.EntityPath, err)
	}
	var header pinnedEntityHeader
	if err := yaml.Unmarshal(data, &header); err != nil {
		return Result{}, Plan{}, fmt.Errorf("parse %s: %w", opts.EntityPath, err)
	}
	if header.Kind == "" {
		return Result{}, Plan{}, fmt.Errorf("%s: missing kind field", opts.EntityPath)
	}
	res := Result{EntityPath: abs, Kind: header.Kind, Name: header.Metadata.Name}
	if header.Kind != "Recipe" && header.Kind != "FeatureRecipe" {
		return res, Plan{}, nil
	}
	res.Pinnable = true

	wsCfg := rw.Config()
	root := rw.Root()
	cacheDir := wsCfg.ResolvedArtifactCacheDir()
	view := catalog.NewLazyLookup(root, cacheDir, []string{filepath.Dir(abs)}, nil)

	uri := catalog.RecipeMappingURI(header.Metadata.Name, header.Metadata.Version)
	if header.Spec.Capability != "" {
		uri = header.Spec.Capability + "/" + uri
	}
	// The step is reached from several surfaces, each of which names itself in
	// its own frame, so this one names none.
	if err := ctx.Err(); err != nil {
		return Result{}, Plan{}, fmt.Errorf("stopped before anything was resolved: %w", err)
	}
	plan, err := Compute(PlanInputs{
		View:          view,
		URIs:          []string{uri},
		Resolver:      SourceResolver(ctx, wsCfg),
		WorkspaceRoot: root,
		CacheDir:      cacheDir,
		ForWriting:    forWriting,
	})
	if err != nil {
		return Result{}, Plan{}, err
	}
	if len(plan.entities) != 1 || plan.entities[0].path != abs {
		return Result{}, Plan{}, notReadAsItself(view, abs, plan)
	}
	return res, plan, nil
}

// notReadAsItself explains why the catalog view over an entity's own directory
// did not read the named document as that entity: the walk refused the file,
// which is the refusal a build of it would meet too, or another document in the
// same directory declares the same identity and was read in its place.
func notReadAsItself(view *catalog.LazyLookup, abs string, plan Plan) error {
	if skip, refused := view.Skips()[abs]; refused {
		return skip
	}
	if len(plan.entities) == 1 {
		return fmt.Errorf("%s declares the identity %s %q, which %s also declares; one identity per directory is what "+
			"lets the lock beside it belong to one entity — give one of the two its own directory",
			abs, plan.entities[0].kind, plan.entities[0].name, plan.entities[0].path)
	}
	return fmt.Errorf("%s was not read as a Recipe or FeatureRecipe in its own directory, so there is no entity to pin", abs)
}

// Summary renders what one run of the step did, in the sentence a terminal
// reader is shown.
//
// It is here rather than at each command that runs the step, so the author's own
// command and a publication that pins on the author's behalf say the same thing
// about the same outcome.
func (r Result) Summary(dryRun bool) string {
	if !r.Pinnable {
		return fmt.Sprintf("Nothing to pin: a %s reads no input an %s records, so nothing beside %s was touched",
			r.Kind, recipepin.LockFileName, r.EntityPath)
	}
	o := r.Outcome
	counts := make([]string, 0, len(o.Pins))
	for _, c := range o.Pins {
		counts = append(counts, fmt.Sprintf("%s pins: %d", c.Class, c.Pins))
	}
	detail := strings.Join(counts, ", ")
	switch {
	case !o.Changed:
		return fmt.Sprintf("Already pinned: %s records every pin %s %q needs (%s)", o.LockPath, o.Kind, o.Name, detail)
	case o.Removed && dryRun:
		return fmt.Sprintf("Would remove %s: %s %q reads nothing it records a pin for", o.LockPath, o.Kind, o.Name)
	case o.Removed:
		return fmt.Sprintf("Removed %s: %s %q reads nothing it records a pin for", o.LockPath, o.Kind, o.Name)
	case dryRun:
		return fmt.Sprintf("Would write %s for %s %q (%s); nothing was written", o.LockPath, o.Kind, o.Name, detail)
	default:
		return fmt.Sprintf("Pinned %s %q: wrote %s (%s). Commit it beside the entity", o.Kind, o.Name, o.LockPath, detail)
	}
}
