package main

import (
	"time"

	"github.com/caarlos0/env/v11"
)

type Config struct {
	Listen              string        `env:"LISTEN" envDefault:":https"`
	AutocertEmail       string        `env:"AUTOCERT_EMAIL"`
	AutocertHosts       []string      `env:"AUTOCERT_HOSTS"`
	SeedURL             string        `env:"SEED_URL" envDefault:"https://raw.githubusercontent.com/cosmos/chain-registry/master/akash/chain.json"`
	SeedRefreshInterval time.Duration `env:"SEED_REFRESH_INTERVAL" envDefault:"5m"`
	ChainID             string        `env:"CHAIN_ID" envDefault:"akash-2"`
}

func mustGetConfig() Config {
	cfg, err := env.ParseAsWithOptions[Config](env.Options{
		Prefix: "AKASH_PROXY_",
	})
	if err != nil {
		panic("could not get config: " + err.Error())
	}
	return cfg
}
