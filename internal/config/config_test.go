package config

import (
	"os"
	"strings"
	"testing"
	"time"

	"github.com/spf13/viper"

	"github.com/stretchr/testify/require"
)

var customTestConfig = Config{
	Server: ServerConfig{
		Listen:     ":8080",
		ListenGRPC: ":9091",
		Timeouts: TimeoutConfig{
			Read:  15 * time.Second,
			Write: 20 * time.Second,
			Idle:  25 * time.Second,
		},
	},
	TLS: TLSConfig{
		Autocert: AutocertConfig{
			Email: "test@example.com",
			Hosts: []string{"example.com", "test.com"},
		},
		Cert: "/path/to/cert",
		Key:  "/path/to/key",
	},
	Seed: SeedConfig{
		URL:             "https://custom-seed-url.com",
		RefreshInterval: 10 * time.Minute,
		ChainID:         "custom-chain",
		EnableRemote:    true,
	},
	Health: HealthConfig{
		HealthyThreshold:    20 * time.Second,
		ProxyRequestTimeout: 30 * time.Second,
	},
	CORS: CORSConfig{
		AllowOrigin:  "https://example.com",
		AllowMethods: "GET,POST",
		AllowHeaders: "Content-Type",
	},
	Metrics: MetricsConfig{
		Enabled: true,
		Listen:  ":9090",
		Path:    "/metrics",
	},
}

func TestEnvironmentVariables(t *testing.T) {
	// Set custom environment variables
	os.Setenv("AKASH_PROXY_SERVER_LISTEN", customTestConfig.Server.Listen)
	os.Setenv("AKASH_PROXY_SERVER_LISTEN_GRPC", customTestConfig.Server.ListenGRPC)
	os.Setenv("AKASH_PROXY_SERVER_TIMEOUTS_READ", customTestConfig.Server.Timeouts.Read.String())
	os.Setenv("AKASH_PROXY_SERVER_TIMEOUTS_WRITE", customTestConfig.Server.Timeouts.Write.String())
	os.Setenv("AKASH_PROXY_SERVER_TIMEOUTS_IDLE", customTestConfig.Server.Timeouts.Idle.String())
	os.Setenv("AKASH_PROXY_TLS_AUTOCERT_EMAIL", customTestConfig.TLS.Autocert.Email)
	os.Setenv("AKASH_PROXY_TLS_AUTOCERT_HOSTS", strings.Join(customTestConfig.TLS.Autocert.Hosts, ","))
	os.Setenv("AKASH_PROXY_TLS_CERT", customTestConfig.TLS.Cert)
	os.Setenv("AKASH_PROXY_TLS_KEY", customTestConfig.TLS.Key)
	os.Setenv("AKASH_PROXY_SEED_URL", customTestConfig.Seed.URL)
	os.Setenv("AKASH_PROXY_SEED_REFRESH_INTERVAL", customTestConfig.Seed.RefreshInterval.String())
	os.Setenv("AKASH_PROXY_SEED_CHAIN_ID", customTestConfig.Seed.ChainID)
	os.Setenv("AKASH_PROXY_SEED_ENABLE_REMOTE", "true")

	os.Setenv("AKASH_PROXY_HEALTH_HEALTHY_THRESHOLD", customTestConfig.Health.HealthyThreshold.String())
	os.Setenv("AKASH_PROXY_HEALTH_PROXY_REQUEST_TIMEOUT", customTestConfig.Health.ProxyRequestTimeout.String())
	os.Setenv("AKASH_PROXY_CORS_ALLOW_ORIGIN", customTestConfig.CORS.AllowOrigin)
	os.Setenv("AKASH_PROXY_CORS_ALLOW_METHODS", customTestConfig.CORS.AllowMethods)
	os.Setenv("AKASH_PROXY_CORS_ALLOW_HEADERS", customTestConfig.CORS.AllowHeaders)

	os.Setenv("AKASH_PROXY_METRICS_ENABLED", "true")
	os.Setenv("AKASH_PROXY_METRICS_LISTEN", ":9090")
	os.Setenv("AKASH_PROXY_METRICS_PATH", "/metrics")

	defer clearAllEnvVars()

	v := viper.New()
	v.SetEnvPrefix("AKASH_PROXY")
	v.SetEnvKeyReplacer(Replacer)
	v.AutomaticEnv()

	// Bind environment variables explicitly
	v.BindEnv("server.listen")
	v.BindEnv("server.listen-grpc")
	v.BindEnv("server.timeouts.read")
	v.BindEnv("server.timeouts.write")
	v.BindEnv("server.timeouts.idle")
	v.BindEnv("tls.autocert.email")
	v.BindEnv("tls.autocert.hosts")
	v.BindEnv("tls.cert")
	v.BindEnv("tls.key")
	v.BindEnv("seed.url")
	v.BindEnv("seed.refresh-interval")
	v.BindEnv("seed.chain-id")
	v.BindEnv("health.healthy-threshold")
	v.BindEnv("health.proxy-request-timeout")
	v.BindEnv("cors.allow-origin")
	v.BindEnv("cors.allow-methods")
	v.BindEnv("cors.allow-headers")

	assignedConfig, err := Read(v)
	if err != nil {
		t.Errorf("unexpected error: %s", err)
	}

	// Test all environment variable values
	require.Equal(t, customTestConfig.Server.Listen, assignedConfig.Server.Listen)
	require.Equal(t, customTestConfig.Server.ListenGRPC, assignedConfig.Server.ListenGRPC)
	require.Equal(t, customTestConfig.Server.Timeouts.Read, assignedConfig.Server.Timeouts.Read)
	require.Equal(t, customTestConfig.Server.Timeouts.Write, assignedConfig.Server.Timeouts.Write)
	require.Equal(t, customTestConfig.Server.Timeouts.Idle, assignedConfig.Server.Timeouts.Idle)
	require.Equal(t, customTestConfig.TLS.Autocert.Email, assignedConfig.TLS.Autocert.Email)
	require.Equal(t, customTestConfig.TLS.Autocert.Hosts, assignedConfig.TLS.Autocert.Hosts)
	require.Equal(t, customTestConfig.TLS.Cert, assignedConfig.TLS.Cert)
	require.Equal(t, customTestConfig.TLS.Key, assignedConfig.TLS.Key)
	require.Equal(t, customTestConfig.Seed.URL, assignedConfig.Seed.URL)
	require.Equal(t, customTestConfig.Seed.RefreshInterval, assignedConfig.Seed.RefreshInterval)
	require.Equal(t, customTestConfig.Seed.ChainID, assignedConfig.Seed.ChainID)
	require.Equal(t, customTestConfig.Health.HealthyThreshold, assignedConfig.Health.HealthyThreshold)
	require.Equal(t, customTestConfig.Health.ProxyRequestTimeout, assignedConfig.Health.ProxyRequestTimeout)
	require.Equal(t, customTestConfig.CORS.AllowOrigin, assignedConfig.CORS.AllowOrigin)
	require.Equal(t, customTestConfig.CORS.AllowMethods, assignedConfig.CORS.AllowMethods)
	require.Equal(t, customTestConfig.CORS.AllowHeaders, assignedConfig.CORS.AllowHeaders)
	require.Equal(t, customTestConfig.Metrics.Enabled, assignedConfig.Metrics.Enabled)
	require.Equal(t, customTestConfig.Metrics.Listen, assignedConfig.Metrics.Listen)
	require.Equal(t, customTestConfig.Metrics.Path, assignedConfig.Metrics.Path)
}

func clearAllEnvVars() {
	envVars := []string{
		"AKASH_PROXY_SERVER_LISTEN",
		"AKASH_PROXY_SERVER_LISTEN_GRPC",
		"AKASH_PROXY_SERVER_TIMEOUTS_READ",
		"AKASH_PROXY_SERVER_TIMEOUTS_WRITE",
		"AKASH_PROXY_SERVER_TIMEOUTS_IDLE",
		"AKASH_PROXY_TLS_AUTOCERT_EMAIL",
		"AKASH_PROXY_TLS_AUTOCERT_HOSTS",
		"AKASH_PROXY_TLS_CERT",
		"AKASH_PROXY_TLS_KEY",
		"AKASH_PROXY_SEED_URL",
		"AKASH_PROXY_SEED_REFRESH_INTERVAL",
		"AKASH_PROXY_SEED_CHAIN_ID",
		"AKASH_PROXY_SEED_ENABLE_REMOTE",
		"AKASH_PROXY_HEALTH_HEALTHY_THRESHOLD",
		"AKASH_PROXY_HEALTH_PROXY_REQUEST_TIMEOUT",
		"AKASH_PROXY_CORS_ALLOW_ORIGIN",
		"AKASH_PROXY_CORS_ALLOW_METHODS",
		"AKASH_PROXY_CORS_ALLOW_HEADERS",
		"AKASH_PROXY_METRICS_ENABLED",
		"AKASH_PROXY_METRICS_LISTEN",
		"AKASH_PROXY_METRICS_PATH",
	}

	for _, envVar := range envVars {
		os.Unsetenv(envVar)
	}
}
