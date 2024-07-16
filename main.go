package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/caarlos0/env/v11"
	"github.com/tendermint/tendermint/libs/log"
)

func main() {
	logger, err := log.NewDefaultLogger(log.LogFormatText, "info", true)
	if err != nil {
		fmt.Println(err)
		os.Exit(1)
	}
	cfg, err := env.ParseAs[Config]()
	if err != nil {
		logger.Error("could not get config", "err", err)
		os.Exit(1)
	}

	m := http.NewServeMux()
	m.Handle("/", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, "Hello, world!")
	}))

	srv := &http.Server{
		Addr:    cfg.Listen,
		Handler: m,
		// TLSConfig:                    &tls.Config{}, TODO
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

type Config struct {
	Listen string `env:"LISTEN" envDefault:":9090"`
}
