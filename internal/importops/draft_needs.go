// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package importops

import (
	"fmt"
	"slices"
	"strconv"

	"go.yaml.in/yaml/v3"

	"github.com/aggrega-ai/aggrega-abi/go/pkg/abi"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/topology"
	"github.com/aggrega-ai/aggrega-cli/pkg/resolver"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// typeUse is one type the drafts need realized and the names of the instances —
// or, for a feature type, the attachments — of it.
type typeUse struct {
	typ   string
	names []string
}

// typeUses lists the types of one kind the drafts need realized, in the order
// they first declare each.
type typeUses []typeUse

// add records that the instance or attachment name is of typ. A name written
// with no type — a Release's sizing of an attachment the Aggregate declares —
// records nothing: the type is recorded where it is written.
func (u *typeUses) add(typ, name string) {
	if typ == "" {
		return
	}
	for i := range *u {
		if (*u)[i].typ == typ {
			if !slices.Contains((*u)[i].names, name) {
				(*u)[i].names = append((*u)[i].names, name)
			}
			return
		}
	}
	*u = append(*u, typeUse{typ: typ, names: []string{name}})
}

// typesOf returns the types of the instances or attachments named name, in
// order: one for a component instance, and for an attachment name as many as
// the components attaching something under it attach.
func (u typeUses) typesOf(name string) []string {
	var out []string
	for _, use := range u {
		if slices.Contains(use.names, name) {
			out = append(out, use.typ)
		}
	}
	return out
}

// draftNeeds is what the drafts need realized: the component types of their
// instances and the feature types of their attachments, each with the names a
// Manifest's name tier addresses them by — a component by the id it carries in
// the flattened graph, an attachment by its name on the component, an
// attachment a connection carries by its composed name.
type draftNeeds struct {
	components typeUses
	features   typeUses
}

// readDraftNeeds reads what the drafted Aggregate and, where one was drafted,
// the drafted Release need realized, and the catalog Aggregates the drafted
// Aggregate composes from the catalog recipes reads.
//
// The drafts are decoded as the entity types a build decodes them as, and read
// through the walks every other reader of those types takes — the subsystem and
// boundary trees, the connection attachment enumerations, and the id and name
// compositions — so the names recorded here are the ones a build addresses. A
// module the catalog does not hold draws a warning and contributes nothing.
func readDraftNeeds(aggregateFile abi.ScaffoldFile, releaseFile *abi.ScaffoldFile, recipes RecipeIndex) (draftNeeds,
	[]abi.Diagnostic, error) {
	var needs draftNeeds
	var diags []abi.Diagnostic
	var aggregate types.Aggregate
	if err := yaml.Unmarshal([]byte(aggregateFile.Content), &aggregate); err != nil {
		return needs, nil, fmt.Errorf("read the drafted Aggregate %q to draft its Manifest: %w", aggregateFile.Path, err)
	}
	var lookup resolver.AggregateLookup
	if recipes != nil {
		lookup = recipes.AggregateLookup()
	}
	needs.addAggregate(&aggregate, "", nil, lookup, &diags)
	if releaseFile != nil {
		var release types.Release
		if err := yaml.Unmarshal([]byte(releaseFile.Content), &release); err != nil {
			return needs, nil, fmt.Errorf("read the drafted Release %q to draft its Manifest: %w", releaseFile.Path, err)
		}
		needs.addRelease(&release)
	}
	return needs, diags, nil
}

// addAggregate records what agg's instances need — their types, their own
// attachments, the attachments their connections carry and their subsystems'
// attachments — each component under the id prefix gives it, and then, in the
// order agg composes them, what each catalog Aggregate it composes needs, under
// the module's own prefix. chain is the module types already being read, outer
// first.
//
// 🔴 DELIBERATE NEAR-DUPLICATE of the build's module recursion (flattenAggregate
// in internal/compiler/topology) and of the lock-time one (DescendSubAggregates in
// pkg/closure): each walks the same composition for a different reader, and this
// one cannot call the build's. The build's walk reads documents already rendered
// against their parameters and halts on anything it cannot place — an unrendered
// toggle, a module it cannot load, a cycle, an identity two modules both claim —
// while this one reads drafts an author has not finished, where any of those is
// ordinary, and must still say which types the finished drafts will need. So it
// follows the build where the build's answer is fixed by what is written, and
// stays lenient where the build would halt:
//
//   - a module or a component whose `active` toggle is written as false is left
//     out, as the build prunes it; a toggle that is not a boolean literal yet — a
//     parameter template a build renders first — is kept, since the finished
//     drafts may well deploy it;
//   - modules are followed no deeper than the build follows them
//     ([topology.MaxModuleDepth]); deeper is the build's to refuse;
//   - a module composing one already being read is not followed again, where the
//     build halts on the cycle;
//   - a module the catalog does not hold draws a warning rather than a halt.
//
// Unifying them would mean giving the build's walk a lenient mode or this one the
// build's halts; neither belongs to either reader.
func (n *draftNeeds) addAggregate(agg *types.Aggregate, prefix string, chain []string, lookup resolver.AggregateLookup,
	diags *[]abi.Diagnostic) {
	normalized := types.Normalize(agg).Aggregate()
	// The visitor cannot fail, so neither can the walk.
	_ = types.WalkInstanceTree(normalized.Spec.Subsystems, types.SubsystemTree(), "", struct{}{},
		types.InstanceTreeVisitor[types.Subsystem, types.ComponentInstance, struct{}]{
			Visit: func(ci types.ComponentInstance, name, _ string, _ bool, _ struct{}) error {
				if writtenInactive(ci.Active) {
					return nil
				}
				n.components.add(ci.Type, types.JoinQualifiedID(prefix, name))
				for attachment, fi := range ci.Features.All() {
					n.features.add(fi.Type, attachment)
				}
				for conn, att := range ci.ConnectionAttachments() {
					n.features.add(att.Type, types.JoinConnectionAttachmentName(conn.Alias, conn.Attachment))
				}
				return nil
			},
			Leave: func(sys types.Subsystem, _, _ string, _ struct{}) {
				for attachment, fi := range sys.Features.All() {
					n.features.add(fi.Type, attachment)
				}
			},
		})
	if len(chain) >= topology.MaxModuleDepth {
		return
	}
	for key, sub := range normalized.Spec.Aggregates.All() {
		if sub.Type == "" || writtenInactive(sub.Active) || slices.Contains(chain, sub.Type) {
			continue
		}
		var module resolver.ResolvedAggregate
		found := false
		if lookup != nil {
			module, found = lookup(sub.Type)
		}
		if !found || module.Aggregate == nil {
			*diags = append(*diags, warnDiagnostic(fmt.Sprintf("the drafted Manifest maps none of the types the "+
				"Aggregate %s composed as %s needs, since the workspace's catalog holds no Aggregate of that name",
				strconv.Quote(sub.Type), strconv.Quote(types.JoinQualifiedID(prefix, key)))))
			continue
		}
		n.addAggregate(module.Aggregate, types.JoinQualifiedID(prefix, key), append(slices.Clone(chain), sub.Type),
			lookup, diags)
	}
}

// writtenInactive reports whether an `active` toggle is written as the boolean
// false, which a build prunes, read through the build's own reading of the
// literal ([topology.ActiveLiteral]). An omitted toggle, one written true, and one
// not yet a boolean literal — a parameter template — all read as present.
func writtenInactive(active *types.ActiveFlag) bool {
	present, _ := topology.ActiveLiteral(active)
	return !present
}

// addRelease records the feature types release attaches — on each boundary at
// every depth, on each component a boundary holds, whether named directly or
// routed in through a module, and on those components' connections. An
// attachment written with no type sizes one the Aggregate declares, whose type
// is recorded there.
func (n *draftNeeds) addRelease(release *types.Release) {
	normalized := types.NormalizeRelease(release).Release()
	// The visitor cannot fail, so neither can the walk.
	_ = types.WalkInstanceTree(normalized.Spec.Boundaries, types.BoundaryTree(), "", struct{}{},
		types.InstanceTreeVisitor[types.Boundary, types.BoundaryComponent, struct{}]{
			Visit: func(bc types.BoundaryComponent, _, _ string, _ bool, _ struct{}) error {
				for attachment, bf := range bc.Features.All() {
					n.features.add(bf.Type, attachment)
				}
				for conn, att := range bc.ConnectionAttachments() {
					n.features.add(att.Type, types.JoinConnectionAttachmentName(conn.Alias, conn.Attachment))
				}
				return nil
			},
			Leave: func(b types.Boundary, _, _ string, _ struct{}) {
				for attachment, bf := range b.Features.All() {
					n.features.add(bf.Type, attachment)
				}
			},
		})
}
