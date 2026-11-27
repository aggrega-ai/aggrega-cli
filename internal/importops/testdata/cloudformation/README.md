# Test data for the cloudformation importer's end-to-end cases

Every template here is written for these tests.

| Path | What it holds |
|---|---|
| `build-back/` | A PostgreSQL instance named `OrdersDb` reading two `Fn::ImportValue` exports, a log group the operator owns, and an MSK cluster no row maps. The cases import it, build the drafts and read the stack template the build writes. |
| `roots/two/` | Two templates, `a.yaml` and `b.yaml`, neither linking the other. |
| `roots/shared/` | Two templates, `a/main.yaml` and `b/main.yaml`, each linking `shared/net.yaml` by the relative path `../shared/net.yaml`. |
| `remote/` | A template linking a second by an `https` address, and a queue. |
| `forms/` | One template written three ways, in `short/` with the short intrinsic forms, in `long/` with the long forms, and in `json/` as JSON. |
