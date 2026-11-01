// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package pipeline

import (
	stderrors "errors"
	"fmt"

	"github.com/aggrega-ai/aggrega-abi-go/pkg/abi"

	"github.com/aggrega-ai/aggrega-cli/pkg/catalog"
	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
	"github.com/aggrega-ai/aggrega-cli/pkg/resolver"
)

// judgeProviderBindings asks the catalog about every provider binding the
// workspace configuration composes, whether or not a Recipe in this build asks
// for its capability, and reports what it answers: a binding naming a plugin
// that does not serve its capability halts the run, and a capability-only
// binding naming a capability no plugin the build reads serves is displayed and
// recorded as an advisory while the run proceeds.
//
// # Why it reads the configuration rather than the lockfile
//
// A build resolves its rendering plugins from the bindings the lockfile
// records, and a pull records only the bindings a Recipe resolved through. A
// binding no Recipe consults therefore never reaches the lockfile, so the one
// place it can be judged is the composed configuration the run was handed. The
// judgement changes nothing about which plugin renders anything: it reads the
// bindings, reports on them, and leaves resolution to the lockfile.
//
// # Why here
//
// Every build and every Release-scoped validation passes through the shared
// steps-one-to-four entry, and this runs inside it, so the command line, the
// daemon, the editor and the assistant tools report a binding the same way. It
// runs below the graph entry's refusal of an absent configuration, which is why
// it reads the configuration without a nil test of its own.
//
// The walk is in the composed list's order, so the advisories are recorded and
// the first halting binding is named deterministically. A catalog lookup that
// supplies no judgement judges nothing.
func judgeProviderBindings(inputs Inputs, catalogLookup resolver.CatalogLookup) error {
	if catalogLookup.ProviderBindingFault == nil {
		return nil
	}
	for _, binding := range inputs.WorkspaceConfig.Plugins.Providers {
		err := catalogLookup.ProviderBindingFault(binding.Capability, binding.URI)
		switch {
		case err == nil:
			continue
		case stderrors.Is(err, catalog.ErrProviderBindingCapabilityUnserved()):
			// The display and the record in one statement group, so the recorded
			// order is the displayed order by construction.
			code, _ := cerrors.CodeOf(err)
			d := abi.Diagnostic{Severity: abi.SeverityWarn, Message: err.Error(), Code: code}
			fmt.Fprintln(inputs.display(), WarningLine(d))
			inputs.AdvisoryLog.record(d)
		default:
			return err
		}
	}
	return nil
}
