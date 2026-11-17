// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

// Package cloudformation is the CloudFormation format of the schema drafting
// core: it reads a CloudFormation provider schema, or the output of
// aws cloudformation describe-type holding one, and drafts a cloudformation
// Recipe declaring one resource of the type it describes.
package cloudformation
