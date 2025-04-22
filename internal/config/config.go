package config

import (
	"fmt"
	"strings"
	"time"

	"github.com/spf13/viper"
)

var Replacer = strings.NewReplacer(".", "_", "-", "_")

type ServerConfig struct {
	Listen     string        `mapstructure:"listen"`
	ListenGRPC string        `mapstructure:"listen-grpc"`
	Timeouts   TimeoutConfig `mapstructure:"timeouts"`
}

type TimeoutConfig struct {
	Read  time.Duration `mapstructure:"read"`
	Write time.Duration `mapstructure:"write"`
	Idle  time.Duration `mapstructure:"idle"`
}

type TLSConfig struct {
	Autocert AutocertConfig `mapstructure:"autocert"`
	Cert     string         `mapstructure:"cert"`
	Key      string         `mapstructure:"key"`
}

type AutocertConfig struct {
	Email string   `mapstructure:"email"`
	Hosts []string `mapstructure:"hosts"`
}

type SeedConfig struct {
	URL             string        `mapstructure:"url"`
	RefreshInterval time.Duration `mapstructure:"refresh-interval"`
	ChainID         string        `mapstructure:"chain-id"`
	AdditionalNodes struct {
		RPC  []string `mapstructure:"rpc"`
		REST []string `mapstructure:"rest"`
		GRPC []string `mapstructure:"grpc"`
	} `mapstructure:"additional-nodes"`
}

type HealthConfig struct {
	HealthyThreshold    time.Duration `mapstructure:"healthy-threshold"`
	ProxyRequestTimeout time.Duration `mapstructure:"proxy-request-timeout"`
}

type CORSConfig struct {
	AllowOrigin  string `mapstructure:"allow-origin"`
	AllowMethods string `mapstructure:"allow-methods"`
	AllowHeaders string `mapstructure:"allow-headers"`
}

type Config struct {
	Server ServerConfig `mapstructure:"server"`
	TLS    TLSConfig    `mapstructure:"tls"`
	Seed   SeedConfig   `mapstructure:"seed"`
	Health HealthConfig `mapstructure:"health"`
	CORS   CORSConfig   `mapstructure:"cors"`
}

// Read returns the configuration from viper.Viper. Returns error if unable to unmarshal.
func Read(v *viper.Viper) (Config, error) {
	var cfg Config
	if err := v.Unmarshal(&cfg); err != nil {
		return Config{}, fmt.Errorf("unmarshalling config: %w", err)
	}

	return cfg, nil
}
