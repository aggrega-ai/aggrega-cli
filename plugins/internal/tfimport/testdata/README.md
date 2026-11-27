# Terraform reader fixtures

Every file here is synthetic, written for the reader's tests; none is copied from a provider's documentation or a
published module.

- `same/native/main.tf`, `same/json/main.tf.json` — one configuration written in the native syntax and in JSON: a
  `terraform` block with its required providers, a provider, a variable, locals, a security group writing two
  `ingress` blocks, one `egress` block, a map of tags (one key quoted) and lists of addresses, a data source, a module
  call and an output, each in the same order in both files.
- `order/a.tf.json`, `order/b.tf` — an output and locals in JSON, and a queue and a variable in the native syntax, so
  that files read in name order and blocks in the order each file writes them.
- `notread/native/main.tf` — a queue beside the blocks the reader does not read: an S3 `backend` inside the
  `terraform` block, `moved`, `import`, `removed`, a `check`, and an `ephemeral` resource, a kind the reader does not
  know.
- `notread/json/main.tf.json` — a queue beside a `cloud` block inside the `terraform` block, a list of `moved` blocks
  and an `ephemeral` resource, in JSON.
- `broken/main.tf` — a queue whose `visibility_timeout_seconds` has no value, so the file does not parse at line 3,
  column 31.
- `layout/` — `envs/prod/` and `envs/dev/`, each calling `../../modules/db`, which declares a variable and a
  database instance.
- `single/` — a root module calling `./modules/queue`, beside a `terraform.tfvars`.
- `varsonly/` — a `terraform.tfvars` and a `prod.tfvars`, and no configuration file.
- `initialised/` — a root module calling a registry module and declaring a queue, as `terraform init` leaves it: a
  `.terraform.lock.hcl`, the installed modules' record `.terraform/modules/modules.json`, the installed module's
  files under `.terraform/modules/db/`, declaring a database instance and a parameter group, and a text file standing
  for a provider binary under `.terraform/providers/`.
- `cdktf/main.tf.json` — a configuration as CDK for Terraform writes it, with `"//"` comment members where Terraform
  reads them as comments: at the top level, in the body of each block, in a nested block and in each block of a list
  of nested blocks.
