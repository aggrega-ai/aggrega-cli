// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

// Package name_the_type attempts to name the identity type from outside the
// package that declares it. It must not compile.
package name_the_type

import cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"

// Naming the type is the first move an author makes towards declaring an
// identity of their own.
var forged *cerrors.sealed

func Forged() error { return forged }
