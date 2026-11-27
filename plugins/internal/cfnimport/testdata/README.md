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
- `sam/globals/template.yaml` — SAM Globals for functions and simple tables (a primitive, a map of variables, a list of
  layers, the architectures, and a network setting reading a parameter) beside two functions, one overriding each,
  a simple table writing no properties, and a queue no Globals name.
- `sam/capacity/template.yaml` — SAM Globals for capacity providers (instance requirements, and managed-resource tags
  holding a member besides `Tags` so that the members a resource does not write are seen dropped) beside a capacity
  provider overriding both, one writing its managed-resource tags empty, and one writing no properties.
- `sam/events-edges/template.yaml` — a SAM function writing an event of each type SAM reads, beside the queue, stream,
  table, bucket, topic, APIs, event bus, log group and user pool its events name.
- `sam/connectors/template.yaml` — a SAM connector resource with a list of destinations and a function depending on it,
  and a connector a function writes under its `Connectors`.
- `sam/properties/template.yaml` — two SAM functions writing, or taking from the Globals, an alias, a
  `DeploymentPreference`, policies, packaged code and inline code.
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
- `golden/<name>/source/` — the templates each golden import reads, and `golden/<name>/want/` the files, diagnostics
  and decisions the import writes, rewritten with `UPDATE_GOLDEN=1`:
  - `rds-server` — an RDS for PostgreSQL instance taking a parameter group, and an Amazon ECS service behind the
    operator's load balancer whose task definition, holding a sidecar beside its main container, reads the instance's
    address into its environment, with the cluster, log group, load balancer, listener, target group and listener
    rule it runs with;
  - `sam` — an AWS SAM template: a function packaged as a .zip and one running an image, taking Globals, a simple
    table, an HTTP API one function's event routes through, and a REST API;
  - `drafted-from-source` — a resource no catalog Recipe deploys, read by a function's environment;
  - `nested` — a template deploying a local nested stack holding a queue and its queue policy, whose output a
    function reads;
  - `condition` — buckets deployed under a condition on a boolean parameter and under one on another parameter, a
    bucket policy reading its bucket in deploy-time text, and a name chosen by `Fn::If`;
  - `dynamic-reference` — a Systems Manager parameter's dynamic reference, and the secrets Secrets Manager and a
    SecureString parameter hold, read by dynamic reference at credential slots;
  - `import-value` — values another stack exports, read whole and inside other functions.
- `forms/long.yaml`, `forms/template.json` — the template of `golden/rds-server/source/template.yaml` written in the
  YAML long form and in JSON.
