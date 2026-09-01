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
	GRPCTLS    bool          `mapstructure:"grpc-tls"`
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
	EnableRemote    bool          `mapstructure:"enable-remote"`
	AdditionalNodes struct {
		RPC  []string `mapstructure:"rpc"`
		REST []string `mapstructure:"rest"`
		GRPC []string `mapstructure:"grpc"`
	} `mapstructure:"additional-nodes"`
}

type HealthConfig struct {
	HealthyThreshold    time.Duration `mapstructure:"healthy-threshold"`
	ProxyRequestTimeout time.Duration `mapstructure:"proxy-request-timeout"`
	// EjectionThreshold is the number of consecutive upstream transport failures that
	// eject a peer from rotation. Zero or negative disables ejection.
	EjectionThreshold int `mapstructure:"ejection-threshold"`
	// EjectionCooldown is how long an ejected peer stays out of rotation before it
	// becomes eligible again, subject to the seed probe still reporting it healthy.
	EjectionCooldown time.Duration `mapstructure:"ejection-cooldown"`
}

type HaltConfig struct {
	Enabled   bool          `mapstructure:"enabled"`
	Threshold time.Duration `mapstructure:"threshold"`
}

type CORSConfig struct {
	AllowOrigin  string `mapstructure:"allow-origin"`
	AllowMethods string `mapstructure:"allow-methods"`
	AllowHeaders string `mapstructure:"allow-headers"`
}

type MetricsConfig struct {
	Enabled     bool   `mapstructure:"enabled"`
	Listen      string `mapstructure:"listen"`
	Path        string `mapstructure:"path"`
	ServiceName string `mapstructure:"service-name"`
}

type OTELConfig struct {
	Enabled      bool    `mapstructure:"enabled"`
	ExporterType string  `mapstructure:"exporter-type"`
	Endpoint     string  `mapstructure:"endpoint"`
	Insecure     bool    `mapstructure:"insecure"`
	ServiceName  string  `mapstructure:"service-name"`
	SampleRate   float64 `mapstructure:"sample-rate"`
}

type Config struct {
	Server  ServerConfig  `mapstructure:"server"`
	TLS     TLSConfig     `mapstructure:"tls"`
	Seed    SeedConfig    `mapstructure:"seed"`
	Health  HealthConfig  `mapstructure:"health"`
	Halt    HaltConfig    `mapstructure:"halt"`
	CORS    CORSConfig    `mapstructure:"cors"`
	Metrics MetricsConfig `mapstructure:"metrics"`
	OTEL    OTELConfig    `mapstructure:"otel"`
}

// Read returns the configuration from viper.Viper. Returns error if unable to unmarshal.
func Read(v *viper.Viper) (Config, error) {
	var cfg Config
	if err := v.Unmarshal(&cfg); err != nil {
		return Config{}, fmt.Errorf("unmarshalling config: %w", err)
	}

	return cfg, nil
}
