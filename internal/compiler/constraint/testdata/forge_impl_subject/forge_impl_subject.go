// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

// Package forgeimplsubject must NOT compile. A subject kind cannot be
// zero-valued, so this attempts the remaining route: implement the interface.
// The impostor implements every method of the contract, including one named
// exactly like the seal.
//
// It fails because an unexported interface method is qualified by the package
// that declared it, so a method of the same name declared here is a different
// method and satisfies nothing.
package forgeimplsubject

import "github.com/aggrega-ai/aggrega-cli/internal/compiler/constraint"

type impostor struct{}

func (impostor) SourceFile() string { return "aggregate.yaml" }

func (impostor) Locator() string { return ".spec.components.web" }

func (impostor) Raise(error, ...string) constraint.Objection { return constraint.Objection{} }

func (impostor) RaiseAt(error, string, ...string) constraint.Objection {
	return constraint.Objection{}
}

func (impostor) sealed() {}

// Passed claims the impostor satisfies the framework's subject contract.
var Passed constraint.Subject = impostor{}
