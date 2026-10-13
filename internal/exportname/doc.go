// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

// Package exportname spells, once each, the exports the host calls a guest with
// a payload.
//
// The plugin-type registry's host-export lists hold these names, and its
// minimum-export rules and governance role-to-export rule read the same
// constants, so no rule names an export the lists spell differently. The code
// generator's plugin-wire registrations read them too: a registration that names
// which of its plugin type's listed exports a payload shape serves names it by the
// constant here, and generation refuses a name the type's list does not hold. So
// each name is spelled in this package and nowhere a person edits besides.
//
// A constant here is a name, not a way to call a guest. The host's
// payload-carrying invocation takes an export only as a value generated from the
// host-export lists, so a name reaches a guest that way only once a list holds it.
package exportname
