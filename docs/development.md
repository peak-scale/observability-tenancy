# Development

Use Go 1.27.1 or newer within the Go 1.27 series. The module declares Go 1.27
and selects the Go 1.27.1 toolchain automatically when needed.
Makefile tools are pinned and installed under `bin/`; changing a version pin
replaces an existing binary on the next invocation of its target.

Getting started locally is pretty easy. You can execute:

```shell
make e2e-build
```

This installs all required operators an installs the operator within a [KinD Cluster](https://kind.sigs.k8s.io/). The required binaries are also downloaded.

If you wish to test against a specific Kubernetes version, you can pass that via variable:

```shell
KIND_K8S_VERSION="v1.31.0" make e2e-build
```

When you want to quickly develop, you can scale down the operator within the cluster:

```shell
kubectl scale deploy observability-addon --replicas=0 -n observability-addon
```

And then execute the binary:

```shell
go run cmd/main.go -zap-log-level=10
```

You might need to first export the Kubeconfig for the cluster (If you are using multiple clusters at the same time):

```shell
bin/kind get kubeconfig --name observability-addon  > /tmp/observability-addon
export KUBECONFIG="/tmp/observability-addon"
```

## Testing

When you are done with the development run the following commands.

For linting:

```shell
make golint
```

To apply formatting and available linter fixes:

```shell
make golint-fix
```

The dependency updates keep `k8s.io/kube-openapi` at the revision used by
Kubernetes v0.37.1. Newer revisions use `structured-merge-diff/v7`, which is
incompatible with that Kubernetes release. The transitive `github.com/google/cel-go`
dependency remains on v0.31.0 because v0.32.0 moved to `cel.dev/cel-go`, while
Capsule still imports the former module path.

For Unit-Testing

```shell
make test
```

For Unit-Testing (When running Unit-Tests there should not be any `argotranslators`, `tenants` and `appprojects` present):

```shell
make e2e-exec
```

## Helm Chart

When making changes to the Helm-Chart, Update the documentation by running:

```shell
make helm-docs
```

Linting and Testing the chart:

```shell
make helm-lint
make helm-test
```

## Performance

Use [PProf](https://book.kubebuilder.io/reference/pprof-tutorial) for profiling:

```shell
curl -s "http://127.0.0.1:8082/debug/pprof/profile" > ./cpu-profile.out

go tool pprof -http=:8080 ./cpu-profile.out
```
