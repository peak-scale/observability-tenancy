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
#
# Defaults to false
ipv6: false

# This parameter sets the limit for the count of outgoing concurrent connections to Cortex / Mimir.
# By default it's 64 and if all of these connections are busy you will get errors when pushing from Prometheus.
# If your `target` is a DNS name that resolves to several IPs then this will be a per-IP limit.
#
# Defaults to 64
maxConnectionsPerHost: 0

# HTTP error code to return when the request is rejected (backend unavailable, no tenant found, etc.)
# 429 indicates for most clients that the request was rejected and that it should be retried later.
#
# Defaults to 429
httpErrorCode: 429

# HTTP request timeout
timeout: 10s

# Timeout to wait on shutdown to allow load balancers detect that we're going away.
# During this period after the shutdown command the /alive endpoint will reply with HTTP 503.
# Set to 0s to disable.
timeoutShutdown: 10s

# Max number of parallel incoming HTTP requests to handle
#
# Defaults to 512
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

  # Whether to remove the tenant label from the request
  #
  # Defaults to false
  labelRemove: true

  # To which header to add the tenant ID
  # This value is also considered as source for the prefixPreferSource option
  #
  # Defaults to X-Scope-OrgID
  header: X-Scope-OrgID

  # Whether to set the HTTP header (Header above) with the tenant ID
  # If false, no header will be set. Useful when you just want to aggregate the tenant as label to the data set.
  #
  # Defaults to true
  setHeader: true

  # Which tenant ID to use if the label is missing in any of the timeseries
  # If this is not set or empty then the write request with missing tenant label
  # will be rejected with HTTP code 400
  # Namespaces which can not be assigned to any tenant will get the
  # default value
  default: foobar

  # Enable if you want all metrics from Prometheus to be accepted with a 204 HTTP code
  # regardless of the response from upstream. This can lose metrics if Cortex/Mimir is
  # throwing rejections.
  #
  # Defaults to true
  acceptAll: false

  # Optional prefix to be added to a tenant header before sending it to Cortex/Mimir.
  # Make sure to use only allowed characters:
  # https://grafana.com/docs/mimir/latest/configure/about-tenant-ids/
  prefix: foobar-

  # When no tenant ID is defined via namespace annotations, use the namespace name as tenant ID.
  # This has priority over the `default` tenant ID.
  #
  # Defaults to false
  setNamespaceAsDefault: false

  # Set this value to add the evaluated tenant as label on the data set.
  # If empty, no label is added
  tenantLabel: ""

  # If true will use the tenant ID of the inbound request as the prefix of the new tenant id.
  # Will be automatically suffixed with a `-` character.
  # Example:
  #   Prometheus forwards metrics with `X-Scope-OrgID: Prom-A` set in the inbound request.
  #   This would result in the tenant prefix being set to `Prom-A-`.
  # https://grafana.com/docs/mimir/latest/configure/about-tenant-ids/
  #
  # Defaults to false
  prefixPreferSource: false
```




type Config struct {
	Bind            string        `default:"0.0.0.0:8080" env:"BIND" yaml:"bind"`
	Backend         *Backend      `yaml:"backend"`
	EnableIPv6      bool          `yaml:"ipv6"`
	HTTPErrorCode   int           `yaml:"httpErrorCode"`
	Selector        LabelSelector `yaml:"selector,omitempty"`
	Timeout         time.Duration `yaml:"timeout"`
	TimeoutShutdown time.Duration `yaml:"timeoutShutdown"`
	Concurrency     int           `yaml:"concurrency"`
	Metadata        bool          `yaml:"metadata"`
	MaxConnDuration time.Duration `yaml:"maxConnectionDuration"`
	MaxConnsPerHost int           `yaml:"maxConnectionsPerHost"`
	Tenant          *TenantConfig `yaml:"tenant"`
	PipeIn          *fhu.InmemoryListener
	PipeOut         *fhu.InmemoryListener
}

type Backend struct {
	URL  string `yaml:"url"`
	Auth struct {
		Username string `yaml:"username"`
		Password string `yaml:"password"`
	} `yaml:"auth"`
}

type TenantConfig struct {
	Labels                []string `yaml:"labels"`
	SetNamespaceAsDefault bool     `default:"false" yaml:"setNamespaceAsDefault"`
	Prefix                string   `yaml:"prefix"`
	PrefixPreferSource    bool     `default:"false" yaml:"prefixPreferSource"`
	LabelRemove           bool     `default:"false" yaml:"labelRemove"`
	Header                string   `default:"X-Scope-OrgID" yaml:"header"`
	SetHeader             bool     `default:"true" yaml:"setHeader"`
	TenantLabel           string   `yaml:"tenantLabel"`

	Default   string `yaml:"default"`
	AcceptAll bool   `default:"true" yaml:"acceptAll"`
}

