package config

import (
	"time"

	"github.com/caarlos0/env/v11"
)

//go:generate go run github.com/g4s8/envdoc@latest -output ../../config.md -type Config
type Config struct {
	// Address to listen to.
	Listen string `env:"LISTEN" envDefault:":https"`

	// Autocert account email.
	AutocertEmail string `env:"AUTOCERT_EMAIL"`

	// Autocert domains.
	AutocertHosts []string `env:"AUTOCERT_HOSTS"`

	// Proxy seed URL to fetch for server updates.
	SeedURL string `env:"SEED_URL" envDefault:"https://raw.githubusercontent.com/cosmos/chain-registry/master/akash/chain.json"`

	// How frequently fetch SEED_URL for updates.
	SeedRefreshInterval time.Duration `env:"SEED_REFRESH_INTERVAL" envDefault:"5m"`

	// Expected chain ID.
	ChainID string `env:"CHAIN_ID" envDefault:"akashnet-2"`

	// How slow on average a node needs to be to be marked as unhealthy.
	HealthyThreshold time.Duration `env:"HEALTHY_THRESHOLD" envDefault:"1s"`

	// Request timeout for a proxied request.
	ProxyRequestTimeout time.Duration `env:"PROXY_REQUEST_TIMEOUT" envDefault:"5s"`

	// How much chance (in %, 0-100), a node marked as unhealthy have to get a
	// request again and recover.
	UnhealthyServerRecoverChancePct int `env:"UNHEALTHY_SERVER_RECOVERY_CHANCE_PERCENT" envDefault:"1"`
}

func Must() Config {
	cfg, err := env.ParseAsWithOptions[Config](env.Options{
		Prefix: "AKASH_PROXY_",
	})
	if err != nil {
		panic("could not get config: " + err.Error())
	}
	return cfg
}
