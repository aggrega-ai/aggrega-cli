// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package tfimport

import "slices"

// ReadIn is a resource's attribute referencing, as its whole value, a
// resource of another type the referencing one owns: the owned resource's
// attributes are read in place of the reference, beneath that attribute.
type ReadIn struct {
	// Parent is the referencing resource's type, such as aws_ecs_service.
	Parent string
	// Path is the parent's attribute holding the reference, its segments
	// joined by dots, such as task_definition; a nested block's item is a
	// segment of its index, and a segment * stands for any item.
	Path string
	// Child is the type of the resource the attribute references, such as
	// aws_ecs_task_definition.
	Child string
}

var readIns = []ReadIn{
	{Parent: "aws_ecs_service", Path: "task_definition", Child: "aws_ecs_task_definition"},
	{Parent: "aws_scheduler_schedule", Path: "target.*.ecs_parameters.*.task_definition_arn",
		Child: "aws_ecs_task_definition"},
}

// ReadIns returns the read-in rules: each attribute whose referenced resource
// is read in place of the reference — the task definition an Amazon ECS
// service runs, and the one an EventBridge Scheduler schedule starts. The
// list is the caller's own.
func ReadIns() []ReadIn {
	return slices.Clone(readIns)
}
