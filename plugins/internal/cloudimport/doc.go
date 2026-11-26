// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

// Package cloudimport is the format-neutral core of the importers that read an
// infrastructure-as-code source — an ARM template, a CloudFormation stack, a
// Terraform configuration — and draft an Aggregate, a Release and the catalog
// entities the source needs.
//
// Each format's reader lifts its source into one model: a [Graph] of resource
// [Node]s whose properties are ordered trees of [Value]s, where a value is a
// literal, a parameter, a reference to another resource's attribute, a
// credential read from a resource, a secret a store holds, a concatenation of
// these, or deploy-time text in the source's own language. The drafts are
// written from that model alone, so the formats share one reading of what a
// source declares:
//
//   - [Edges] lists what each resource consumes from another: every reference
//     and credential in its properties, with the property path it sits at, and
//     every dependency it states.
//   - [Order] places producers before their consumers, ties broken by
//     identifier, so one source drafts one byte sequence whatever order it
//     writes its resources in; a cycle is broken at its smallest identifier.
//   - [Keys] gives each resource the instance key every importer gives a
//     source item, and [Format.Keys] the key a format gives it, made unique
//     the same way.
//   - [SelectRoot] chooses the source an import starts from among several the
//     import is handed, and [Expand] writes a repeated resource as one node per
//     copy where the source states the copies literally.
//   - [Classify] gives each resource one class and one decision: folded into
//     another resource's instance, attached as a Feature on another
//     resource's instance by the format's [AttachRule], substrate left to the
//     operator, existing, mapped to a catalog Component through the format's
//     [Table], drafted as a Component and a Recipe from the source — for a
//     module, a Recipe sourcing it whole — held back, or not carried.
//   - [Aggregate] and [Release] write the drafted Aggregate and Release, with
//     the Manifest naming each realization the Release names.
//
// [Import] runs the whole of it for one request and any [Format]: the
// settings, the reader, the expansion, the classification and the drafts, the
// Aggregate, the Release and the catalog entities, laid out as the importer
// kit lays out every importer's drafts. Any halt drafts nothing.
//
// Every diagnostic the core and its formats raise is made by [Report], which
// gives each code of the vocabulary its one severity.
package cloudimport
