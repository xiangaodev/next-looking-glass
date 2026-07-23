package main

import (
	"context"
	"embed"
	"errors"
	"flag"
	"html/template"
	"io/fs"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/xiangaodev/next-looking-glass/internal/config"
	"github.com/xiangaodev/next-looking-glass/internal/server"
)

//go:embed web/templates/index.html
var tplFS embed.FS

//go:embed web/static
var staticFS embed.FS

func main() {
	cfgPath := flag.String("config", "config.yaml", "path to config.yaml")
	flag.Parse()

	cfg, err := config.Load(*cfgPath)
	if err != nil {
		log.Fatalf("config: %v", err)
	}

	tpl, err := template.ParseFS(tplFS, "web/templates/index.html")
	if err != nil {
		log.Fatalf("template: %v", err)
	}

	staticSub, err := fs.Sub(staticFS, "web/static")
	if err != nil {
		log.Fatalf("static fs: %v", err)
	}
	srv := server.New(cfg, tpl, staticSub)

	// Graceful shutdown.
	go func() {
		sig := make(chan os.Signal, 1)
		signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
		<-sig
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = srv.Shutdown(ctx)
	}()

	if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Fatalf("server: %v", err)
	}
}
