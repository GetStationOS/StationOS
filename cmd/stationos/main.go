package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"stationos.local/stationos/internal/broadcast"
	"strings"
	"syscall"
	"time"
	_ "time/tzdata"

	"stationos.local/stationos/internal/catalogue"
	"stationos.local/stationos/internal/persistence"
	"stationos.local/stationos/internal/production"
	ai "stationos.local/stationos/internal/production/openai"
	"stationos.local/stationos/internal/sources/upnp"
	"stationos.local/stationos/internal/web"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stderr, nil))
	if err := run(os.Args[1:], logger); err != nil {
		logger.Error("StationOS stopped", "error", err)
		os.Exit(1)
	}
}
func run(args []string, logger *slog.Logger) error {
	flags := flag.NewFlagSet("stationos", flag.ContinueOnError)
	dbPath := flags.String("db", "data/stationos.db", "SQLite database path")
	listen := flags.String("listen", "127.0.0.1:8080", "HTTP listen address")
	publicURL := flags.String("public-url", "", "public origin for stream URLs and proxied management writes (optional)")
	maxListeners := flags.Int("max-listeners", 1024, "maximum connected listeners per station")
	maxBroadcasts := flags.Int("max-broadcasts", min(8, max(1, runtime.NumCPU())), "maximum simultaneous station encoders")
	task := flags.String("task", "serve", "serve, integrity, sync, or clear-catalogue")
	source := flags.String("source", "", "source ID for sync or optional filter for clear-catalogue")
	if err := flags.Parse(args); err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := os.MkdirAll(filepath.Dir(*dbPath), 0700); err != nil {
		return err
	}
	db, err := persistence.Open(ctx, *dbPath)
	if err != nil {
		return err
	}
	defer db.Close()
	stations := persistence.Stations{DB: db}
	sources := persistence.Sources{DB: db}
	cat := persistence.Catalogue{DB: db}
	syncer := catalogue.Sync{Sources: sources, Catalogue: cat, Factory: upnp.Factory, Logger: logger}
	switch *task {
	case "integrity":
		if err = persistence.Integrity(ctx, db); err != nil {
			return err
		}
		fmt.Println("Database integrity: ok")
		return nil
	case "sync":
		if *source == "" {
			return errors.New("sync requires -source")
		}
		n, err := syncer.Run(ctx, *source)
		if err != nil {
			return err
		}
		fmt.Printf("Imported %d items\n", n)
		return nil
	case "clear-catalogue":
		if err = cat.Clear(ctx, *source); err != nil {
			return err
		}
		fmt.Println("Derived catalogue cleared; station and source configuration retained")
		return nil
	case "serve":
	default:
		return errors.New("unknown task")
	}
	password := os.Getenv("STATIONOS_ADMIN_PASSWORD")
	host, _, err := net.SplitHostPort(*listen)
	if err != nil {
		return err
	}
	ip := net.ParseIP(host)
	if (ip == nil || !ip.IsLoopback()) && password == "" {
		return errors.New("non-loopback listen requires STATIONOS_ADMIN_PASSWORD")
	}
	if *publicURL != "" {
		u, e := url.Parse(*publicURL)
		if e != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Path != "" && u.Path != "/") {
			return errors.New("public-url must be an HTTP or HTTPS origin")
		}
		*publicURL = strings.TrimSuffix(*publicURL, "/")
	}
	if *maxListeners < 1 || *maxListeners > 10000 || *maxBroadcasts < 1 || *maxBroadcasts > 64 {
		return errors.New("invalid broadcast capacity")
	}
	ffmpeg, err := exec.LookPath("ffmpeg")
	if err != nil {
		return errors.New("FFmpeg is required for station broadcasting; install ffmpeg and restart")
	}
	config := persistence.Broadcast{DB: db}
	djsRepo, err := persistence.OpenDJs(ctx, db, *dbPath+".secrets-key")
	if err != nil {
		return err
	}
	shows := persistence.Schedule{DB: db}
	presenter := ai.New(djsRepo, persistence.PresenterAssets{DB: db}, production.FFmpeg{Path: ffmpeg})
	broadcasts := broadcast.New(config, broadcast.FFmpeg{Path: ffmpeg, Sources: sources, Factory: upnp.Factory}, logger, broadcast.Options{MaxStations: *maxBroadcasts, MaxListeners: *maxListeners, Schedule: shows, Producer: presenter, Links: presenter})
	defer broadcasts.Close()
	handler, err := web.New(web.Dependencies{ArtworkFactory: upnp.Factory, Schedule: shows, DJs: djsRepo, Broadcast: broadcasts, BroadcastConfig: config, PublicURL: *publicURL, Stations: stations, Sources: sources, Catalogue: cat, Playlists: persistence.Playlists{DB: db}, Sync: syncer, Ready: db.PingContext, Logger: logger, Password: password})
	if err != nil {
		return err
	}
	server := &http.Server{Addr: *listen, Handler: handler, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 15 * time.Second, WriteTimeout: 150 * time.Second, IdleTimeout: 60 * time.Second, MaxHeaderBytes: 32 << 10}
	listener, err := net.Listen("tcp", *listen)
	if err != nil {
		return err
	}
	done := make(chan error, 1)
	go func() { done <- server.Serve(listener) }()
	logger.Info("StationOS listening", "address", listener.Addr().String())
	select {
	case err = <-done:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	case <-ctx.Done():
		broadcasts.Close()
		shutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err = server.Shutdown(shutdown); err != nil {
			server.Close()
			return err
		}
		return nil
	}
}
