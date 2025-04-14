package main

import (
	"context"
	_ "embed"
	"errors"
	"github.com/akash-network/rpc-proxy/internal/proxy/cors"
	"html/template"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"golang.org/x/net/http2"
	"golang.org/x/net/http2/h2c"
	"golang.org/x/sync/errgroup"

	"github.com/akash-network/rpc-proxy/internal/config"
	"github.com/akash-network/rpc-proxy/internal/proxy"
	"github.com/akash-network/rpc-proxy/internal/seed"
	"golang.org/x/crypto/acme/autocert"
)

//go:embed index.html
var index []byte

func main() {
	cfg := config.Must()
	// TODO: Logging configurations through context propagation??
	log := slog.New(slog.NewJSONHandler(os.Stdout, nil))

	rpcListener := make(chan seed.Seed, 1)
	restListener := make(chan seed.Seed, 1)
	grpcListener := make(chan seed.Seed, 1)

	seederCfg := seed.Config{
		SeedURL:             cfg.SeedURL,
		SeedRefreshInterval: cfg.SeedRefreshInterval,
		ChainID:             cfg.ChainID,
	}
	seeder := seed.New(seederCfg, log, rpcListener, restListener, grpcListener)
	rpcProxyHandler := proxy.NewRPCProxy(rpcListener, cfg, log, proxy.NewLatencyBased(log))
	restProxyHandler := proxy.NewRestProxy(restListener, cfg, log, proxy.NewLatencyBased(log))
	grpcProxyHandler := proxy.NewGRPCProxy(grpcListener, cfg, log, proxy.NewLatencyBased(log))

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
			log.Error("could not close server", "err", err)
			os.Exit(1)
		}

		if err := grpcServer.Shutdown(ctx); err != nil {
			log.Error("could not close server", "err", err)
			os.Exit(1)
		}
	}()

	proxyGroup.Go(func() error {
		log.Info("starting server", "addr", srv.Addr)
		var err error
		if cfg.Listen == ":https" {
			err = srv.ListenAndServeTLS(cfg.TLSCert, cfg.TLSKey)
		} else {
			err = srv.ListenAndServe()
		}
		if err != nil {
			if errors.Is(err, http.ErrServerClosed) {
				log.Info("server shut down")
				return nil
			}
			log.Error("could not start server", "err", err)
			return err
		}

		return nil
	})

	proxyGroup.Go(func() error {
		log.Info("starting grpc proxy", "addr", grpcServer.Addr)
		err := grpcServer.ListenAndServeTLS(cfg.TLSCert, cfg.TLSKey)
		if err != nil {
			if errors.Is(err, http.ErrServerClosed) {
				log.Info("server shut down")
				return nil
			}
			log.Error("could not start grpc server", "err", err)
			return err
		}

		return nil
	})

	if err := proxyGroup.Wait(); err != nil {
		log.Error("there was an error an a proxy", "error", err)
	}
}

func prepareRestAndRPCServer(log *slog.Logger, cfg config.Config, rpcProxyHandler *proxy.RPCProxy, restProxyHandler *proxy.RestProxy) *http.Server {
	am := autocert.Manager{
		Cache:  autocert.DirCache("."),
		Prompt: autocert.AcceptTOS,
	}
	if addr := cfg.AutocertEmail; addr != "" {
		am.Email = addr
	}
	if hosts := cfg.AutocertHosts; len(hosts) > 0 {
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
			log.Error("could render stats", "err", err)
		}
	}))

	// TODO: make this part of the configuration in configuration PR.
	corsHeaders := map[string]string{
		cors.AccessControlAllowOrigin:  "*",
		cors.AccessControlAllowMethods: "GET, POST, PUT, DELETE, OPTIONS",
		cors.AccessControlAllowHeaders: "Content-Type, Authorization",
	}

	srv := &http.Server{
		Addr:         cfg.Listen,
		Handler:      cors.WithCorsMiddleware(corsHeaders, m),
		TLSConfig:    am.TLSConfig(),
		ReadTimeout:  time.Second * 10,
		IdleTimeout:  time.Second * 10,
		WriteTimeout: time.Second * 10,
	}

	if cfg.TLSCert != "" && cfg.TLSKey != "" {
		srv.TLSConfig = nil
	}

	return srv
}

func prepareGRPCServer(log *slog.Logger, cfg config.Config, p *proxy.GRPCProxy) *http.Server {
	// TODO: make this part of the configuration in configuration PR.
	corsHeaders := map[string]string{
		cors.AccessControlAllowOrigin:  "*",
		cors.AccessControlAllowMethods: "GET, POST, PUT, DELETE, OPTIONS",
		cors.AccessControlAllowHeaders: "Content-Type, Authorization",
	}

	mux := http.NewServeMux()
	mux.Handle("/", cors.WithCorsMiddleware(corsHeaders, p))

	// Start HTTP/2.0 server.
	grpcServer := &http.Server{
		Addr:         cfg.ListenGRPC,
		ReadTimeout:  time.Second * 10,
		IdleTimeout:  time.Second * 10,
		WriteTimeout: time.Second * 10,
		Handler:      h2c.NewHandler(mux, &http2.Server{}),
	}

	return grpcServer
}
