package main

import (
	"context"
	_ "embed"
	"errors"
	"fmt"
	"html/template"
	"log"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/spf13/viper"

	"github.com/akash-network/rpc-proxy/internal/proxy/cors"

	"golang.org/x/net/http2"
	"golang.org/x/net/http2/h2c"
	"golang.org/x/sync/errgroup"

	"github.com/akash-network/rpc-proxy/internal/config"
	"github.com/akash-network/rpc-proxy/internal/metrics"
	"github.com/akash-network/rpc-proxy/internal/proxy"
	"github.com/akash-network/rpc-proxy/internal/seed"
	"github.com/spf13/cobra"
	"golang.org/x/crypto/acme/autocert"
)

//go:embed index.html
var index []byte

func NewRootCmd(v *viper.Viper) *cobra.Command {
	rootCmd := &cobra.Command{
		Use:   "akash-proxy",
		Short: "Akash Proxy - A load balancer and proxy for Akash network nodes",
		Long:  "Akash Proxy provides load balancing and automatic failover for Akash network RPC, gRPC and REST nodes.",
		PreRunE: func(cmd *cobra.Command, args []string) error {
			v.SetEnvPrefix("AKASH_PROXY")
			v.SetEnvKeyReplacer(config.Replacer)
			v.AutomaticEnv()

			configPath := cmd.Flags().Lookup("config").Value.String()
			if configPath != "" {
				v.SetConfigFile(configPath)
			} else {
				v.SetConfigName("config")
				v.SetConfigType("yaml")
				v.AddConfigPath(".")
				v.AddConfigPath(filepath.Join("$HOME", ".akash-proxy"))
			}

			// If a config file is found, read it in.
			if err := v.ReadInConfig(); err != nil {
				var configFileNotFoundError viper.ConfigFileNotFoundError
				if !errors.As(err, &configFileNotFoundError) {
					return fmt.Errorf("reading configuration: %w", err)
				}
			}

			if err := v.BindPFlags(cmd.PersistentFlags()); err != nil {
				return fmt.Errorf("binding flags %w", err)
			}

			return nil
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := config.Read(v)
			if err != nil {
				return fmt.Errorf("reading configuration: %w", err)
			}
			runProxy(cfg)
			return nil
		},
	}

	// Server configuration
	rootCmd.PersistentFlags().String("server.listen", ":25567", "Address to listen on for HTTP REST & RPC requests")
	rootCmd.PersistentFlags().String("server.listen-grpc", ":25568", "Address to listen on for gRPC requests")
	rootCmd.PersistentFlags().Bool("server.grpc-tls", true, "Enable TLS for gRPC server")
	rootCmd.PersistentFlags().Duration("server.timeouts.read", 10*time.Second, "Server read timeout")
	rootCmd.PersistentFlags().Duration("server.timeouts.write", 10*time.Second, "Server write timeout")
	rootCmd.PersistentFlags().Duration("server.timeouts.idle", 10*time.Second, "Server idle timeout")

	// TLS configuration
	rootCmd.PersistentFlags().String("tls.autocert.email", "", "Email for Let's Encrypt certificates")
	rootCmd.PersistentFlags().StringSlice("tls.autocert.hosts", []string{}, "Comma-separated list of domains for Let's Encrypt")
	rootCmd.PersistentFlags().String("tls.cert", "", "Path to TLS certificate file")
	rootCmd.PersistentFlags().String("tls.key", "", "Path to TLS private key file")

	// Seed configuration
	rootCmd.PersistentFlags().String("seed.url", "https://raw.githubusercontent.com/cosmos/chain-registry/master/akash/chain.json", "URL to fetch initial node list")
	rootCmd.PersistentFlags().Duration("seed.refresh-interval", 5*time.Minute, "How often to refresh node list")
	rootCmd.PersistentFlags().String("seed.chain-id", "akashnet-2", "Expected chain ID")
	rootCmd.PersistentFlags().Bool("seed.enable-remote", true, "Enable remote seed fetching")
	rootCmd.PersistentFlags().StringSlice("seed.additional-nodes.rpc", []string{}, "Comma-separated list of additional RPC nodes")
	rootCmd.PersistentFlags().StringSlice("seed.additional-nodes.rest", []string{}, "Comma-separated list of additional REST nodes")
	rootCmd.PersistentFlags().StringSlice("seed.additional-nodes.grpc", []string{}, "Comma-separated list of additional gRPC nodes")

	// Health configuration
	rootCmd.PersistentFlags().Duration("health.healthy-threshold", 10*time.Second, "Response time threshold for healthy nodes")
	rootCmd.PersistentFlags().Duration("health.proxy-request-timeout", 15*time.Second, "Timeout for proxied requests")

	// CORS configuration
	rootCmd.PersistentFlags().String("cors.allow-origin", "*", "CORS allowed origin")
	rootCmd.PersistentFlags().String("cors.allow-methods", "GET, POST, PUT, DELETE, OPTIONS", "CORS allowed methods")
	rootCmd.PersistentFlags().String("cors.allow-headers", "Content-Type, Authorization", "CORS allowed headers")

	// Metrics configuration
	rootCmd.PersistentFlags().Bool("metrics.enabled", true, "Enable metrics server")
	rootCmd.PersistentFlags().String("metrics.listen", ":4000", "Address to listen on for metrics")
	rootCmd.PersistentFlags().String("metrics.path", "/metrics", "Path to expose metrics on")

	// Configuration file support
	rootCmd.PersistentFlags().StringP("config", "c", "", "config file (default is $HOME/.akash-proxy/config.yaml)")

	return rootCmd
}

func runProxy(cfg config.Config) {
	log := slog.New(slog.NewJSONHandler(os.Stdout, nil))

	var metricsServer *http.Server
	if cfg.Metrics.Enabled {
		metricsServer = metrics.PrepareMetricsServer(cfg.Metrics.Listen, cfg.Metrics.Path)
		go func() {
			log.Info("metrics server", "addr", metricsServer.Addr, "path", cfg.Metrics.Path)
			if err := metricsServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
				panic(err)
			}
		}()
	}

	rpcListener := make(chan seed.Seed, 1)
	restListener := make(chan seed.Seed, 1)
	grpcListener := make(chan seed.Seed, 1)

	seederCfg := seed.Config{
		SeedURL:             cfg.Seed.URL,
		SeedRefreshInterval: cfg.Seed.RefreshInterval,
		ChainID:             cfg.Seed.ChainID,
		EnableRemote:        cfg.Seed.EnableRemote,
		AdditionalNodes: struct {
			RPC  []string
			REST []string
			GRPC []string
		}{
			RPC:  cfg.Seed.AdditionalNodes.RPC,
			REST: cfg.Seed.AdditionalNodes.REST,
			GRPC: cfg.Seed.AdditionalNodes.GRPC,
		},
	}
	seeder := seed.New(seederCfg, log, rpcListener, restListener, grpcListener)
	rpcProxyHandler := proxy.NewRPCProxy(rpcListener, cfg.Health, log, proxy.NewLatencyBased(log))
	restProxyHandler := proxy.NewRestProxy(restListener, cfg.Health, log, proxy.NewLatencyBased(log))
	grpcProxyHandler := proxy.NewGRPCProxy(grpcListener, log, proxy.NewLatencyBased(log))

	ctx, proxyCtxCancel := context.WithCancel(context.Background())
	defer proxyCtxCancel()
	seeder.Start(ctx)
	rpcProxyHandler.Start(ctx)
	restProxyHandler.Start(ctx)
	grpcProxyHandler.Start(ctx)

	srv := prepareRestAndRPCServer(log, cfg, rpcProxyHandler, restProxyHandler)
	grpcServer := prepareGRPCServer(log, cfg, grpcProxyHandler)

	proxyGroup, ctx := errgroup.WithContext(ctx)

	done := make(chan os.Signal, 1)
	signal.Notify(done, os.Interrupt, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		<-done
		proxyCtxCancel()
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if err := srv.Shutdown(ctx); err != nil {
			log.Error("could not close server", "error", err)
			os.Exit(1)
		}

		if err := grpcServer.Shutdown(ctx); err != nil {
			log.Error("could not close server", "error", err)
			os.Exit(1)
		}

		if cfg.Metrics.Enabled && metricsServer != nil {
			if err := metricsServer.Shutdown(ctx); err != nil {
				log.Error("could not close metrics server", "error", err)
				os.Exit(1)
			}
		}
	}()

	proxyGroup.Go(func() error {
		log.Info("starting server", "addr", srv.Addr)
		var err error

		if cfg.Server.Listen == ":https" {
			if cfg.TLS.Cert == "" || cfg.TLS.Key == "" {
				err = fmt.Errorf("TLS certificate and key must be provided when HTTPS is enabled")
				log.Error("could not start server", "error", err)
				return err
			}
			err = srv.ListenAndServeTLS(cfg.TLS.Cert, cfg.TLS.Key)
		} else {
			err = srv.ListenAndServe()
		}
		if err != nil {
			if errors.Is(err, http.ErrServerClosed) {
				log.Info("server shut down")
				return nil
			}
			log.Error("could not start server", "error", err)
			return err
		}

		return nil
	})

	proxyGroup.Go(func() error {
		log.Info("starting grpc proxy", "addr", grpcServer.Addr)
		var err error
		if cfg.Server.GRPCTLS {
			if cfg.TLS.Cert == "" || cfg.TLS.Key == "" {
				err = fmt.Errorf("TLS certificate and key must be provided when gRPC TLS is enabled")
				log.Error("could not start grpc server", "error", err)
				return err
			}
			err = grpcServer.ListenAndServeTLS(cfg.TLS.Cert, cfg.TLS.Key)
		} else {
			err = grpcServer.ListenAndServe()
		}
		if err != nil {
			if errors.Is(err, http.ErrServerClosed) {
				log.Info("server shut down")
				return nil
			}
			log.Error("could not start grpc server", "error", err)
			return err
		}

		return nil
	})

	if err := proxyGroup.Wait(); err != nil {
		log.Error("there was an error an a proxy", "error", err)
	}
}

func main() {
	var v = viper.New()

	if err := NewRootCmd(v).Execute(); err != nil {
		log.Fatalf("failed to execute command: %v", err)
	}
}

func prepareRestAndRPCServer(log *slog.Logger, cfg config.Config, rpcProxyHandler *proxy.RPCProxy, restProxyHandler *proxy.RestProxy) *http.Server {
	am := autocert.Manager{
		Cache:  autocert.DirCache("."),
		Prompt: autocert.AcceptTOS,
	}
	if addr := cfg.TLS.Autocert.Email; addr != "" {
		am.Email = addr
	}
	if hosts := cfg.TLS.Autocert.Hosts; len(hosts) > 0 {
		am.HostPolicy = autocert.HostWhitelist(hosts...)
	}

	indexTpl := template.Must(template.New("stats").Parse(string(index)))

	m := http.NewServeMux()
	m.Handle("/health/ready", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !rpcProxyHandler.Ready() || !restProxyHandler.Ready() {
			w.WriteHeader(http.StatusServiceUnavailable)
		}
	}))
	m.Handle("/health/live", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !rpcProxyHandler.Live() || !restProxyHandler.Live() {
			w.WriteHeader(http.StatusServiceUnavailable)
		}
	}))
	m.Handle("/rpc/", rpcProxyHandler)
	m.Handle("/rest/", restProxyHandler)
	m.Handle("/rpc", rpcProxyHandler)
	m.Handle("/rest", restProxyHandler)
	m.Handle("/status", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := indexTpl.Execute(w, map[string][]proxy.ServerStat{
			"RPC":  rpcProxyHandler.Stats(),
			"Rest": restProxyHandler.Stats(),
		}); err != nil {
			log.Error("could render stats", "error", err)
		}
	}))

	corsHeaders := map[string]string{
		cors.AccessControlAllowOrigin:  cfg.CORS.AllowOrigin,
		cors.AccessControlAllowMethods: cfg.CORS.AllowMethods,
		cors.AccessControlAllowHeaders: cfg.CORS.AllowHeaders,
	}

	srv := &http.Server{
		Addr:         cfg.Server.Listen,
		Handler:      cors.WithCorsMiddleware(corsHeaders, m),
		TLSConfig:    am.TLSConfig(),
		ReadTimeout:  cfg.Server.Timeouts.Read,
		IdleTimeout:  cfg.Server.Timeouts.Idle,
		WriteTimeout: cfg.Server.Timeouts.Write,
	}

	if cfg.TLS.Cert != "" && cfg.TLS.Key != "" {
		srv.TLSConfig = nil
	}

	return srv
}

func prepareGRPCServer(log *slog.Logger, cfg config.Config, p *proxy.GRPCProxy) *http.Server {
	corsHeaders := map[string]string{
		cors.AccessControlAllowOrigin:  cfg.CORS.AllowOrigin,
		cors.AccessControlAllowMethods: cfg.CORS.AllowMethods,
		cors.AccessControlAllowHeaders: cfg.CORS.AllowHeaders,
	}

	mux := http.NewServeMux()
	mux.Handle("/", cors.WithCorsMiddleware(corsHeaders, p))

	// Start HTTP/2.0 server.
	grpcServer := &http.Server{
		Addr:         cfg.Server.ListenGRPC,
		ReadTimeout:  cfg.Server.Timeouts.Read,
		IdleTimeout:  cfg.Server.Timeouts.Idle,
		WriteTimeout: cfg.Server.Timeouts.Write,
		Handler:      h2c.NewHandler(mux, &http2.Server{}),
	}

	return grpcServer
}
