// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package clisurface

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/aggrega-ai/aggrega-cli/internal/opsurface"
)

// Attach registers cmd under parent as the command carrying the given
// operation, and is the one way a capability reaches the command tree.
//
// It refuses three things, and each is a way a command could otherwise reach the
// tree without anybody having ruled that it should. An identifier that is not an
// operation: the tree would then carry a capability the operation set does not
// know about. An operation this surface declares unavailable: the reason on that
// row is what keeps the command off the tree, so attaching one anyway would make
// the reason a note beside a command that exists. And a command whose resulting
// invocation path is not one the row gives: the path is written in the table and
// read from it, so a command that spells itself differently is a second spelling
// of a name that has one home.
//
// The path is read back from cobra after attaching rather than composed here,
// because cobra is what computes a command's invocation path and a second
// computation of it would be the drift this function exists to prevent. A
// refusal detaches the command again, so a caller that recovers from one is left
// with the tree it had.
//
// Attach does not construct the command. What a command needs in order to run —
// its flags, its arguments, whatever its body requires — stays with the command,
// because a registrar that assembled those would have to speak about every
// command at once, and the only shape that does is one that requires nothing.
func Attach(parent, cmd *cobra.Command, id string) error {
	if parent == nil || cmd == nil {
		return fmt.Errorf("clisurface: attaching the %q operation needs both a parent and a command", id)
	}

	// projection-omits internal/opsurface.Operation.Reach: the class bounds a
	// caller who does not own the host an operation runs on, and the caller here
	// is the person at this machine's keyboard. There is nothing for it to decide
	// on this surface, so it is dropped rather than carried unread.
	op, known := opsurface.Lookup(id)
	if !known {
		return fmt.Errorf("clisurface: %q is not an operation, so no command can be attached for it: "+
			"either the operation was renamed or removed, or the identifier is misspelled", id)
	}

	availability, declared := operationAvailability[id]
	if !declared {
		return fmt.Errorf("clisurface: the command line declares nothing about the %q operation (%s), "+
			"so it has no command path to attach one at: declare it in the availability table, either "+
			"exposed at the path this command will have or unavailable with the reason it is not carried",
			id, op.Summary)
	}
	if !availability.IsExposed() {
		return fmt.Errorf("clisurface: the command line does not carry the %q operation, because %s; "+
			"attaching a command for it would leave that reason standing beside a command that exists",
			id, availability.UnavailabilityReason)
	}

	parent.AddCommand(cmd)
	path := cmd.CommandPath()
	for _, declaredPath := range availability.Names {
		if declaredPath == path {
			return nil
		}
	}
	parent.RemoveCommand(cmd)
	return fmt.Errorf("clisurface: the command line exposes the %q operation at %s, and this command's "+
		"invocation path is %q: rename the command to the declared path, or change the path the "+
		"availability table gives",
		id, quotedList(availability.Names), path)
}

// AttachGroup registers a command that only holds subcommands, which is no
// capability and therefore names no operation.
//
// It exists so that a grouping command is attached by saying it is one, rather
// than by reaching past the registrar. It refuses a runnable command: a command
// with a body does work, and work is a capability the operation set has to know
// about.
func AttachGroup(parent, cmd *cobra.Command) error {
	if parent == nil || cmd == nil {
		return fmt.Errorf("clisurface: attaching a grouping command needs both a parent and a command")
	}
	if cmd.Runnable() {
		return fmt.Errorf("clisurface: %q is runnable, so it is a capability rather than a grouping "+
			"command: attach it with Attach and the operation it carries", cmd.Name())
	}
	parent.AddCommand(cmd)
	return nil
}

// AttachContributed registers a command a configured plugin contributes.
//
// A contributed command is the plugin's capability rather than the compiler's,
// so the operation set does not enumerate it and this surface's table says
// nothing about it. It has its own entry point rather than sharing one with the
// grouping commands, so that the exception is declared where it is taken and a
// census of what bypasses the operation table finds it by name.
func AttachContributed(parent, cmd *cobra.Command) error {
	if parent == nil || cmd == nil {
		return fmt.Errorf("clisurface: attaching a contributed command needs both a parent and a command")
	}
	parent.AddCommand(cmd)
	return nil
}

// quotedList renders names for a refusal message, so a row giving several paths
// reports all of them rather than only the first.
func quotedList(names []string) string {
	quoted := make([]string, 0, len(names))
	for _, n := range names {
		quoted = append(quoted, fmt.Sprintf("%q", n))
	}
	return strings.Join(quoted, " or ")
}
