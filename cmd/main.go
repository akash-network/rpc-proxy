package main

import (
	"context"
	_ "embed"
	"errors"
	"fmt"
	"golang.org/x/net/http2"
	"golang.org/x/net/http2/h2c"
	"html/template"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

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

	rpcListener := make(chan seed.Seed, 1)
	restListener := make(chan seed.Seed, 1)
	grpcListener := make(chan seed.Seed, 1)

	updater := seed.New(cfg, log, rpcListener, restListener, grpcListener)
	rpcProxyHandler := proxy.New(proxy.RPC, rpcListener, cfg, log)
	restProxyHandler := proxy.New(proxy.Rest, restListener, cfg, log)
	grpcProxyHandler := proxy.New(proxy.GRPC, grpcListener, cfg, log)

	ctx, proxyCtxCancel := context.WithCancel(context.Background())
	defer proxyCtxCancel()
	updater.Start(ctx)
	rpcProxyHandler.Start(ctx)
	restProxyHandler.Start(ctx)
	grpcProxyHandler.Start(ctx)

	indexTpl := template.Must(template.New("stats").Parse(string(index)))

	m := http.NewServeMux()
	m.Handle("/health/ready", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !rpcProxyHandler.Ready() || !restProxyHandler.Ready() || !grpcProxyHandler.Ready() {
			w.WriteHeader(http.StatusServiceUnavailable)
		}
	}))
	m.Handle("/health/live", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !rpcProxyHandler.Live() || !restProxyHandler.Live() {
			w.WriteHeader(http.StatusServiceUnavailable)
		}
	}))
	m.Handle("/rpc", rpcProxyHandler)
	m.Handle("/rest", restProxyHandler)
	m.Handle("/", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := indexTpl.Execute(w, map[string][]proxy.ServerStat{
			"RPC":  rpcProxyHandler.Stats(),
			"Rest": restProxyHandler.Stats(),
			"GRPC": grpcProxyHandler.Stats(),
		}); err != nil {
			log.Error("could render stats", "err", err)
		}
	}))

	srv := &http.Server{
		Addr:         cfg.Listen,
		Handler:      m,
		TLSConfig:    am.TLSConfig(),
		ReadTimeout:  time.Second * 10,
		IdleTimeout:  time.Second * 10,
		WriteTimeout: time.Second * 10,
	}

	if cfg.TLSCert != "" && cfg.TLSKey != "" {
		srv.TLSConfig = nil
	}

	go func() {
		log.Info("starting server", "addr", cfg.Listen)

		var err error
		if cfg.Listen == ":https" {
			err = srv.ListenAndServeTLS(cfg.TLSCert, cfg.TLSKey)
		} else {
			err = srv.ListenAndServe()
		}
		if err != nil {
			if errors.Is(err, http.ErrServerClosed) {
				log.Info("server shut down")
				return
			}
			log.Error("could not start server", "err", err)
			os.Exit(1)
		}
	}()

	go func() {
		err := startGRPCServer(log, cfg, grpcProxyHandler, &am)
		if err != nil {
			if errors.Is(err, http.ErrServerClosed) {
				log.Info("server shut down")
				return
			}
			log.Error("could not start grpc server", "err", err)
			os.Exit(1)
		}
	}()

	done := make(chan os.Signal, 1)
	signal.Notify(done, os.Interrupt, syscall.SIGINT, syscall.SIGTERM)
	<-done

	proxyCtxCancel()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := srv.Shutdown(ctx); err != nil {
		log.Error("could not close server", "err", err)
		os.Exit(1)
	}
}

func startGRPCServer(log *slog.Logger, cfg config.Config, p *proxy.Proxy, am *autocert.Manager) error {
	mux := http.NewServeMux()

	// Handle all requests with the proxy
	mux.Handle("/", p)

	// Start the HTTP/2 server with TLS
	server := &http.Server{
		Addr:         cfg.ListenGRPC,
		Handler:      h2c.NewHandler(mux, &http2.Server{}),
		ReadTimeout:  time.Second * 10,
		IdleTimeout:  time.Second * 10,
		WriteTimeout: time.Second * 10,
	}

	log.Info(fmt.Sprintf("starting grpc proxy on %s", cfg.ListenGRPC))
	return server.ListenAndServeTLS(cfg.TLSCert, cfg.TLSKey)
}
