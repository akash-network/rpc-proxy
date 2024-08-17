package main

import (
	"context"
	_ "embed"
	"errors"
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

	updater := seed.New(cfg, rpcListener, restListener)
	rpcProxyHandler := proxy.New(proxy.RPC, rpcListener, cfg)
	restProxyHandler := proxy.New(proxy.Rest, restListener, cfg)

	ctx, proxyCtxCancel := context.WithCancel(context.Background())
	defer proxyCtxCancel()
	updater.Start(ctx)
	rpcProxyHandler.Start(ctx)
	restProxyHandler.Start(ctx)

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
	m.Handle("/rpc", rpcProxyHandler)
	m.Handle("/rest", restProxyHandler)
	m.Handle("/", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := indexTpl.Execute(w, map[string][]proxy.ServerStat{
			"RPC":  rpcProxyHandler.Stats(),
			"Rest": restProxyHandler.Stats(),
		}); err != nil {
			slog.Error("could render stats", "err", err)
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
		slog.Info("starting server", "addr", cfg.Listen)
		var err error
		// TODO: find a better way to set this.
		if cfg.Listen == ":https" {
			err = srv.ListenAndServeTLS(cfg.TLSCert, cfg.TLSKey)
		} else {
			err = srv.ListenAndServe()
		}
		if err != nil {
			if errors.Is(err, http.ErrServerClosed) {
				slog.Info("server shut down")
				return
			}
			slog.Error("could not start server", "err", err)
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
		slog.Error("could not close server", "err", err)
		os.Exit(1)
	}
}
