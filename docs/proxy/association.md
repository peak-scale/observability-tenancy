# Organization Association

The key aspect of these proxy implementations is that they do not just only take a label value and set it as a tenant ID, but they index the namespaces matching the label value and associate them with the tenant ID. This allows for a more flexible and powerful association between namespaces and organizations.

To associate a namespace value with a tenant ID, you can annotate the namespace with a specific annotation. The proxy will then inspect the value of this annotation and use it as the tenant ID for all telemetry data related to that namespace.

* **Annotation**: `observe.addons.projectcapsule.dev/org`

If a dataset contains a [target-label](#target-labels) that delivers a namespace value, the proxy will look for the annotation `observe.addons.projectcapsule.dev/org` in that namespace. If it exists, the value of this annotation will be used as the tenant ID for all telemetry data related to that namespace:

```yaml
apiVersion: v1
kind: Namespace
metadata:
  annotations:
    observe.addons.projectcapsule.dev/org: solar
  name: solar-test
```

When the label value is `solar-test`, the tenant ID will be set to `solar`. This means that all telemetry data related to the namespace `solar-test` will be associated with the organization `solar`.

## Target Labels

In the [configuration](./configuration.md) section, you can define the labels that are inspected for a possible value. Note that only the first label that matches will be used, so you can define multiple labels to inspect in order of priority.

```yaml
tenant:
  labels:
    - namespace
    - target_namespace
```

If no label is found, you can define a default tenant ID that will be used for all namespaces that do not match any of the configured labels:

```yaml
tenant:
  default: "default-tenant-id"
```

You can also define that the namespace name itself is used for each namespace as tenant ID:

```yaml
tenant:
  setNamespaceAsDefault: true
```

## Capsule Integration

This addon was initially designed to exclusively work with [Capsule](https://projectcapsule.dev), a Kubernetes operator that manages multi-tenancy in clusters. However, since the organization association is now bound to namespaces, it's no longer necessary to use Capsule for this functionality. The addon can now operate independently, allowing you to use it without Capsule.

If you are using Capsule, you can use the [AdditionalMetadataList](https://projectcapsule.dev/docs/tenants/enforcement/#additionalmetadatalist) to provide the namespaces of a tenant with the necessary information.

This example, all namespaces of the tenant `solar` will have the annotation `observe.addons.projectcapsule.dev/org: "solar"` added to them. This results in all telemetry relating to those namespaces being associated with the organization `solar`:

```yaml
apiVersion: capsule.clastix.io/v1beta2
kind: Tenant
metadata:
  labels:
    kubernetes.io/metadata.name: solar
  name: solar
spec:
  owners:
  - clusterRoles:
    - admin
    - capsule-namespace-deleter
    kind: User
    name: alice
  namespaceOptions:
    additionalMetadataList:
    # An item without any further selectors is applied to all namspaces
    - annotations:
        observe.addons.projectcapsule.dev/org: "solar"
```

Here is an example of a more complex configuration, where we associate different organizations based on the environment of the namespaces:

```yaml
apiVersion: capsule.clastix.io/v1beta2
kind: Tenant
metadata:
  labels:
    kubernetes.io/metadata.name: solar
  name: solar
spec:
  owners:
  - clusterRoles:
    - admin
    - capsule-namespace-deleter
    kind: User
    name: alice
  namespaceOptions:
    additionalMetadataList:
    # An item without any further selectors is applied to all namspaces
    - annotations:
        observe.addons.projectcapsule.dev/org: "development"
      namespaceSelector:
        matchExpressions:
          - key: corp.example/env
            operator: In
            values: ["dev"]
    - annotations:
        observe.addons.projectcapsule.dev/org: "production"
      namespaceSelector:
        matchExpressions:
          - key: corp.example/env
            operator: In
            values: ["prod"]
    - annotations:
        observe.addons.projectcapsule.dev/org: "infrastructure"
      namespaceSelector:
        matchExpressions:
          - key: corp.example/env
            operator: NotIn
            values: ["dev", "prod"]
