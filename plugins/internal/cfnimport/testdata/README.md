# CloudFormation reader fixtures

Every file here is synthetic, written for the reader's tests; none is copied from a published template.

- `sections/template.yaml` — one template writing every top-level section the reader reads: a transform, Metadata,
  parameters of each kind (allowed values, an allowed pattern, length and value bounds, a `NoEcho` parameter with a
  default, two resource-pointer types), Mappings, Conditions, Rules, resources with a condition, dependencies written
  as one name and as a list, Metadata, a deletion policy and short-form values, one resource that is no object, and
  outputs exported under a literal name and under a computed one.
- `values/template.yaml` — two resources, one reading the other with `!Ref`, which the YAML parser reads as plain
  text.
- `params/template.yaml` — a template declaring three parameters.
- `params/list.json`, `params/pairs.json`, `params/object.json` — one set of values for those parameters in each form
  the AWS CLI reads a parameters file in: a `ParameterKey`/`ParameterValue` list, a list of `Key=Value` strings (one
  value holding `=`), and a `Parameters` object.
- `params/previous.json` — a list keeping one parameter at its previous value with `UsePreviousValue`.
- `params/tags.json`, `params/policy.json` — a `Parameters` object beside stack tags, and beside a stack policy.
- `params/undeclared.json` — a list setting one parameter the template does not declare.
- `params/env.json` — a list of `Key=Value` strings setting an environment variable no template declares.
- `params/extra.json` — a list of `Key=Value` strings setting one declared parameter and one undeclared.
- `linked/` — a root template linking a nested stack by `./network.yaml`, a serverless application by
  `services/api.yaml`, which links `../shared/queue.yaml`; none of the linked templates declares a format version.
  The root also links a template by URL, an application by its ID, and a path leaving the directory.
- `cycle/a.yaml`, `cycle/b.yaml` — two templates each linking the other.
- `sam/` — a SAM project after `sam build`: its template, the template the build writes under
  `.aws-sam/build/`, a test event, a truncated test event the template reader refuses, and a `package.json`.
- `refused/template.yaml` — a template whose YAML breaks off, which the template reader refuses.
- `nested/flat/` — a root template deploying `network.yaml` as a nested stack, binding its parameters (one to a root
  parameter whole, one to text composed of one, one to text) and reading its outputs; `network.yaml` declares a
  parameter with a default, one with none, a mapping and a condition of the same names as the root's, and deploys
  `subnets.yaml` as a nested stack of its own.
- `nested/application/` — a SAM template deploying `apps/orders.yaml` as a serverless application whose `Location` is a
  local path, reading its output.
- `nested/remote/` — a root template deploying nested stacks linked by a URL of the web, a URL of Amazon S3 and a value
  the template computes, and a serverless application located by its ID, with a resource reading their outputs.
- `nested/outside/` — a root template deploying a nested stack linked by a path leaving the directory.
- `stray/` — a template beside sources the template reader refuses: `docs/notes.yaml`, indented with a tab and
  naming `AWSTemplateFormatVersion` in a comment, and `broken.yaml`, whose YAML breaks off, linked by `other.yaml`
  and by `main.yaml`.
