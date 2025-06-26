# Proxy Configuration

The service can be configured by a config file and/or environment variables. Config file may be specified by passing `-config` CLI argument.

If both are used then the env vars have precedence (i.e. they override values from config).
See below for config file format and corresponding env vars.

```yaml
# Where to send the modified requests (Cortex/Mimir)
backend:
  url: http://127.0.0.1:9091/receive
  # Authentication (optional)
  auth:
    username: foo
    password: bar

# Whether to enable querying for IPv6 records
ipv6: false

# This parameter sets the limit for the count of outgoing concurrent connections to Cortex / Mimir.
# By default it's 64 and if all of these connections are busy you will get errors when pushing from Prometheus.
# If your `target` is a DNS name that resolves to several IPs then this will be a per-IP limit.
maxConnectionsPerHost: 0

# HTTP request timeout
timeout: 10s

# Timeout to wait on shutdown to allow load balancers detect that we're going away.
# During this period after the shutdown command the /alive endpoint will reply with HTTP 503.
# Set to 0s to disable.
timeoutShutdown: 10s

# Max number of parallel incoming HTTP requests to handle
concurrency: 10

# Whether to forward metrics metadata from Prometheus to Cortex/Mimir
# Since metadata requests have no timeseries in them - we cannot divide them into tenants
# So the metadata requests will be sent to the default tenant only, if one is not defined - they will be dropped
metadata: false

# Maximum duration to keep outgoing connections alive (to Cortex/Mimir)
# Useful for resetting L4 load-balancer state
# Use 0 to keep them indefinitely
maxConnectionDuration: 0s

# Select only a subset of tenant to consider for collection
# namespaces which can not be assigned to any tenant will get the
# default value
selector:
  matchLabels:
    env: "prod"

tenant:
  # List of labels examined for tenant information.
  labels:
    - namespace
    - target_namespace

  # Whether to remove the tenant label from the request (Cortex Only currently)
  labelRemove: true

  # To which header to add the tenant ID
  header: X-Scope-OrgID

  # Which tenant ID to use if the label is missing in any of the timeseries
  # If this is not set or empty then the write request with missing tenant label
  # will be rejected with HTTP code 400
  # Namespaces which can not be assigned to any tenant will get the
  # default value
  default: foobar

  # Enable if you want all metrics from Prometheus to be accepted with a 204 HTTP code
  # regardless of the response from upstream. This can lose metrics if Cortex/Mimir is
  # throwing rejections.
  acceptAll: false

  # Optional prefix to be added to a tenant header before sending it to Cortex/Mimir.
  # Make sure to use only allowed characters:
  # https://grafana.com/docs/mimir/latest/configure/about-tenant-ids/
  prefix: foobar-

  # When no tenant ID is defined via namespace annotations, use the namespace name as tenant ID.
  # This has priority over the `default` tenant ID.
  setNamespaceAsDefault: false

  # If true will use the tenant ID of the inbound request as the prefix of the new tenant id.
  # Will be automatically suffixed with a `-` character.
  # Example:
  #   Prometheus forwards metrics with `X-Scope-OrgID: Prom-A` set in the inbound request.
  #   This would result in the tenant prefix being set to `Prom-A-`.
  # https://grafana.com/docs/mimir/latest/configure/about-tenant-ids/
  prefixPreferSource: false
```
