package config

import (
	"os"
	"time"

	"github.com/caarlos0/env/v8"
	"github.com/pkg/errors"
	fhu "github.com/valyala/fasthttp/fasthttputil"
	"gopkg.in/yaml.v2"
)

type Config struct {
	Bind            string        `env:"BIND"                   yaml:"bind"`
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
	SetNamespaceAsDefault bool     `yaml:"setNamespaceAsDefault"`
	Prefix                string   `yaml:"prefix"`
	PrefixPreferSource    bool     `yaml:"prefixPreferSource"`
	LabelRemove           bool     `yaml:"labelRemove"`
	Header                string   `yaml:"header"`
	Default               string   `yaml:"default"`
	AcceptAll             bool     `yaml:"acceptAll"`
}

func Load(file string) (*Config, error) {
	cfg := &Config{}

	if file != "" {
		y, err := os.ReadFile(file)
		if err != nil {
			return nil, errors.Wrap(err, "Unable to read config")
		}

		if err := yaml.UnmarshalStrict(y, cfg); err != nil {
			return nil, errors.Wrap(err, "Unable to parse config")
		}
	}

	if err := env.Parse(cfg); err != nil {
		return nil, errors.Wrap(err, "Unable to parse env vars")
	}

	if cfg.Bind == "" {
		cfg.Bind = "0.0.0.0:8080"
	}

	if cfg.Backend == nil {
		return nil, errors.New("backend configuration is required")
	}

	if cfg.Backend.URL == "" {
		return nil, errors.New("backend URL is required")
	}

	if cfg.Concurrency == 0 {
		cfg.Concurrency = 512
	}

	if cfg.Tenant.Header == "" {
		cfg.Tenant.Header = "X-Scope-OrgID"
	}

	// Default to the Label if list is empty
	if len(cfg.Tenant.Labels) == 0 {
		cfg.Tenant.Labels = append(cfg.Tenant.Labels, "__tenant__")
	}

	if cfg.MaxConnsPerHost == 0 {
		cfg.MaxConnsPerHost = 64
	}

	// With this Error Code, Alloy attempts to resend the request
	if cfg.HTTPErrorCode == 0 {
		cfg.HTTPErrorCode = 429
	}

	return cfg, nil
}
