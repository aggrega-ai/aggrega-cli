# Test data for the cloudformation importer's end-to-end cases

Every template here is written for these tests.

| Path | What it holds |
|---|---|
| `build-back/` | A PostgreSQL instance named `OrdersDb` reading two `Fn::ImportValue` exports, a log group the operator owns, and an MSK cluster no row maps. The cases import it, build the drafts and read the stack template the build writes. |
| `roots/two/` | Two templates, `a.template.yaml` and `b.template.yaml`, neither linking the other. |
| `roots/shared/` | Two templates, `a/template.yaml` and `b/template.yaml`, each linking `shared/net.template.yaml` by the relative path `../shared/net.template.yaml`. |
| `remote/` | A template linking a second by an `https` address, and a queue. |
| `forms/` | One template written three ways, in `short/` with the short intrinsic forms, in `long/` with the long forms, and in `json/` as JSON. |
| `owed/function-zip/` | A SAM function packaged as a `.zip`, stating its runtime and its role, whose container image the import leaves for the answers file. |
| `owed/function-image/` | A Lambda function run from a container image, stating its role and no runtime, whose runtime the import leaves for the answers file. |
| `owed/bucket/` | An S3 bucket, whose Region the import leaves for the answers file. |
