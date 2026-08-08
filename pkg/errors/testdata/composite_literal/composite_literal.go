// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

// Package composite_literal attempts to build an identity by writing its field
// in a composite literal. It must not compile.
package composite_literal

import cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"

// Setting the field directly is how an author would claim a code that another
// package declares.
var forged = &cerrors.sealed{code: "errors-sealing-probe/legitimate"}

func Forged() error { return forged }
