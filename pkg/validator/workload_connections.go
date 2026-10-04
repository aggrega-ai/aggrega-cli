// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package validator

import (
	"fmt"
	"strings"

	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// WorkloadConnection is one connection the component owning a Workload draws, as
// a Workload entry naming it through `connection:` reads it: the socket the
// connection is wired through, and the slots that socket declares for every
// connection wired through it.
type WorkloadConnection struct {
	// Socket is the socket the connection is wired through.
	Socket string
	// Slots are the slots the socket declares, in the Component's own
	// declaration order — the set a connection's attachments fill.
	Slots types.OrderedMap[types.AttachmentSlot]
}

// WorkloadSlots is every slot a Workload's entries may name: the slots the
// owning entity declares for its own attachments, and, for each connection the
// component draws, keyed by the alias the connection is written under, the slots
// that connection's socket declares.
//
// A Workload rendered for an attachment's FeatureRecipe is checked against the
// attached Feature's slots and carries no connection, so an entry there naming
// one is refused as naming a connection the owner does not draw.
type WorkloadSlots struct {
	Own         types.OrderedMap[types.AttachmentSlot]
	Connections types.OrderedMap[WorkloadConnection]
}

// FoldWorkloadConnections rewrites every Workload entry that names a connection
// onto the one slot name the compiler composes for the attachment that connection
// carries in the named slot, and returns the slot set the folded Workload is
// checked against.
//
// Two entry forms name a slot, and both are folded: a container's `attachments`
// entry, whose `from` names the slot, and a container's `envFrom` entry, whose
// `secret` names it — on every container and every init container. An entry
// naming a connection has its slot replaced by the composed name
// [types.JoinConnectionAttachmentName] spells and its `connection` emptied, so
// every check and every provider resolution downstream reads the folded entry
// exactly as it reads an entry naming a slot the Component declares: the slot
// membership and delivery-allowlist check, the referenceability check, and a
// provider's lookup of the slot's published locators all run unchanged against
// the returned set.
//
// The returned set is the owner's own slots followed by, for each connection in
// the order the component draws them, each slot its socket declares under its
// composed name, carrying the socket's own declaration — so the allowlist an
// entry is held to is the one the socket wrote.
//
// Three entries are refused, each as a finding of the slot-declaration stage
// naming what the entry named:
//
//   - one naming a connection the component draws under no such alias, naming
//     the connection and the slot;
//   - one naming a slot the connection's socket does not declare, naming the
//     slot, the connection and the socket;
//   - one naming, without a connection, the composed name of a slot a
//     connection's socket declares. A connection's slot is named by naming the
//     connection, so the composed name stays a name the compiler writes and never
//     a second spelling an author can reach the same slot by.
//
// Nothing is read out of a name: a composed name is recognised by building it
// from a declared alias and a declared slot and comparing whole strings.
//
// The Workload is rewritten in place. It returns a *WorkloadValidationError
// carrying [ErrWorkloadFeaturesSchema] when any entry is refused, and the
// Workload is then not to be used. recipePath / recipeName / recipeVersion
// thread into the error for operator-facing context.
func FoldWorkloadConnections(
	w *types.Workload,
	slots WorkloadSlots,
	recipePath, recipeName, recipeVersion string,
) (types.OrderedMap[types.AttachmentSlot], error) {
	checked := types.OrderedMap[types.AttachmentSlot]{}
	for name, slot := range slots.Own.All() {
		checked.Set(name, slot)
	}
	// composed records, for each name the compiler composes for a connection's
	// slot, the connection and the slot it was composed from — the only way a
	// composed name is recognised.
	type connectionSlot struct{ alias, slot string }
	composed := map[string]connectionSlot{}
	for alias, conn := range slots.Connections.All() {
		for slotName, slot := range conn.Slots.All() {
			name := types.JoinConnectionAttachmentName(alias, slotName)
			checked.Set(name, slot)
			composed[name] = connectionSlot{alias: alias, slot: slotName}
		}
	}
	if w == nil {
		return checked, nil
	}

	var findings []WorkloadValidationFinding

	// fold answers the folded slot name for one entry naming slot through the
	// connection alias, or records a finding and answers "". fieldPath is the
	// entry's own coordinate; slotKey is the key that names its slot.
	fold := func(fieldPath, slotKey, slot, alias string) string {
		if alias == "" {
			if origin, isComposed := composed[slot]; isComposed {
				if _, own := slots.Own.Get(slot); !own {
					findings = append(findings, WorkloadValidationFinding{
						FieldPath:  fieldPath + "." + slotKey,
						Constraint: fmt.Sprintf("names %q, the name the compiler composes for slot %q of connection %q; a slot a connection's socket declares is named by naming the connection", slot, origin.slot, origin.alias),
						Suggestion: fmt.Sprintf("write connection: %s beside %s: %s", origin.alias, slotKey, origin.slot),
					})
					return ""
				}
			}
			return slot
		}
		conn, drawn := slots.Connections.Get(alias)
		if !drawn {
			findings = append(findings, WorkloadValidationFinding{
				FieldPath:  fieldPath + ".connection",
				Constraint: fmt.Sprintf("names connection %q for slot %q, but the component draws no connection under that alias", alias, slot),
				Suggestion: drawnConnectionsSuggestion(slots.Connections),
			})
			return ""
		}
		if _, declared := conn.Slots.Get(slot); !declared {
			findings = append(findings, WorkloadValidationFinding{
				FieldPath:  fieldPath + "." + slotKey,
				Constraint: fmt.Sprintf("names slot %q of connection %q, which socket %q does not declare", slot, alias, conn.Socket),
				Suggestion: declaredSocketSlotsSuggestion(conn),
			})
			return ""
		}
		return types.JoinConnectionAttachmentName(alias, slot)
	}

	foldContainer := func(c *types.ContainerSpec, containerPath string) {
		for key, ref := range c.Attachments.All() {
			folded := fold(fmt.Sprintf("%s.attachments.%s", containerPath, key), "from", ref.From, ref.Connection)
			if folded == "" || ref.Connection == "" {
				continue
			}
			ref.From = folded
			ref.Connection = ""
			c.Attachments.Set(key, ref)
		}
		for i := range c.EnvFrom {
			ef := &c.EnvFrom[i]
			if ef.Secret == "" && ef.Connection == "" {
				continue
			}
			folded := fold(fmt.Sprintf("%s.envFrom[%d]", containerPath, i), "secret", ef.Secret, ef.Connection)
			if folded == "" || ef.Connection == "" {
				continue
			}
			ef.Secret = folded
			ef.Connection = ""
		}
	}

	for name, c := range w.Containers.All() {
		foldContainer(&c, fmt.Sprintf(".containers.%s", name))
		w.Containers.Set(name, c)
	}
	for i := range w.InitContainers {
		foldContainer(&w.InitContainers[i], fmt.Sprintf(".initContainers[%d]", i))
	}

	if len(findings) == 0 {
		return checked, nil
	}
	return checked, &WorkloadValidationError{
		Path:          recipePath,
		RecipeName:    recipeName,
		RecipeVersion: recipeVersion,
		Stage:         "featuresSchema",
		Findings:      findings,
		Err:           ErrWorkloadFeaturesSchema(),
	}
}

// drawnConnectionsSuggestion names the connections a component draws, in the
// order it draws them, so a refused alias is shown beside the ones that exist.
func drawnConnectionsSuggestion(connections types.OrderedMap[WorkloadConnection]) string {
	var aliases []string
	for alias := range connections.All() {
		aliases = append(aliases, alias)
	}
	if len(aliases) == 0 {
		return "the component draws no connection, so remove connection to read a slot the Component declares for its own attachments"
	}
	return fmt.Sprintf("name a connection the component draws (%s), or remove connection to read a slot the Component declares for its own attachments", strings.Join(aliases, ", "))
}

// declaredSocketSlotsSuggestion names the slots a connection's socket declares,
// in declaration order, so a refused slot is shown beside the ones that exist.
func declaredSocketSlotsSuggestion(conn WorkloadConnection) string {
	var declared []string
	for name := range conn.Slots.All() {
		declared = append(declared, name)
	}
	if len(declared) == 0 {
		return fmt.Sprintf("socket %q declares no slot, so a connection wired through it carries no attachment", conn.Socket)
	}
	return fmt.Sprintf("name a slot socket %q declares (%s), or declare the slot in that socket's featuresSchema", conn.Socket, strings.Join(declared, ", "))
}
