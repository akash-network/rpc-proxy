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

	"github.com/akash-network/proxy/internal/config"
	"github.com/akash-network/proxy/internal/proxy"
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

	proxyHandler := proxy.New(cfg)

	proxyCtx, proxyCtxCancel := context.WithCancel(context.Background())
	defer proxyCtxCancel()
	proxyHandler.Start(proxyCtx)

	indexTpl := template.Must(template.New("stats").Parse(string(index)))

	m := http.NewServeMux()
	m.Handle("/", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := indexTpl.Execute(w, proxyHandler.Stats()); err != nil {
			slog.Error("could render stats", "err", err)
		}
	}))
	m.Handle("/rpc", proxyHandler)

	srv := &http.Server{
		Addr:         cfg.Listen,
		Handler:      m,
		TLSConfig:    am.TLSConfig(),
		ReadTimeout:  time.Second * 10,
		IdleTimeout:  time.Second * 10,
		WriteTimeout: time.Second * 10,
	}

	go func() {
		slog.Info("starting server", "addr", cfg.Listen)
		if err := srv.ListenAndServe(); err != nil {
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

	proxyCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := srv.Shutdown(proxyCtx); err != nil {
		slog.Error("could not close server", "err", err)
		os.Exit(1)
	}
}
