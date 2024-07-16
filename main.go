package main

import (
	"context"
	"errors"
	"io"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/tendermint/tendermint/libs/log"
	"golang.org/x/crypto/acme/autocert"
)

func main() {
	cfg := mustGetConfig()

	logger, err := log.NewDefaultLogger(log.LogFormatText, "info", true)
	if err != nil {
		panic("could not setup logger: " + err.Error())
	}

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

	m := http.NewServeMux()
	m.Handle("/", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, "Hello, world!")
	}))

	srv := &http.Server{
		Addr:      cfg.Listen,
		Handler:   m,
		TLSConfig: am.TLSConfig(),
	}

	go func() {
		logger.Info("starting server", "addr", cfg.Listen)
		if err := srv.ListenAndServe(); err != nil {
			if errors.Is(err, http.ErrServerClosed) {
				logger.Info("server shut down")
				return
			}
			logger.Error("could not start server", "err", err)
			os.Exit(1)
		}
	}()

	done := make(chan os.Signal, 1)
	signal.Notify(done, os.Interrupt, syscall.SIGINT, syscall.SIGTERM)
	<-done

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := srv.Shutdown(ctx); err != nil {
		logger.Error("could not close server", "err", err)
		os.Exit(1)
	}
}
