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
	"stationos.local/stationos/internal/changefeed"
	"strings"
	"syscall"
	"time"
	_ "time/tzdata"

	agentai "stationos.local/stationos/internal/ai"
	"stationos.local/stationos/internal/catalogue"
	"stationos.local/stationos/internal/episodes"
	"stationos.local/stationos/internal/inference"
	"stationos.local/stationos/internal/operations"
	"stationos.local/stationos/internal/persistence"
	"stationos.local/stationos/internal/producer"
	producerAI "stationos.local/stationos/internal/producer/openai"
	"stationos.local/stationos/internal/production"
	ai "stationos.local/stationos/internal/production/openai"
	"stationos.local/stationos/internal/schedule"
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
	streamBuffer := flags.Duration("stream-buffer", 5*time.Second, "shared startup/join audio buffer (0s to 5s)")
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
	_, _, err = net.SplitHostPort(*listen)
	if err != nil {
		return err
	}
	if *publicURL != "" {
		u, e := url.Parse(*publicURL)
		if e != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Path != "" && u.Path != "/") {
			return errors.New("public-url must be an HTTP or HTTPS origin")
		}
		*publicURL = strings.TrimSuffix(*publicURL, "/")
	}
	if *streamBuffer < 0 || *streamBuffer > 5*time.Second {
		return errors.New("stream-buffer must be between 0s and 5s")
	}
	if *maxListeners < 1 || *maxListeners > 10000 || *maxBroadcasts < 1 || *maxBroadcasts > 64 {
		return errors.New("invalid broadcast capacity")
	}
	ffmpeg, err := exec.LookPath("ffmpeg")
	if err != nil {
		return errors.New("FFmpeg is required for station broadcasting; install ffmpeg and restart")
	}
	changes := &changefeed.Bus{}
	operationalStore := persistence.Operations{DB: db, Changes: changes}
	recorder, err := operations.New(ctx, operationalStore, logger)
	if err != nil {
		return err
	}
	defer recorder.Close()
	config := persistence.Broadcast{DB: db}
	mixerConfig := persistence.Mixer{DB: db}
	djsRepo, err := persistence.OpenDJs(ctx, db, *dbPath+".secrets-key")
	if err != nil {
		return err
	}
	shows := persistence.Schedule{DB: db, Changes: changes}
	audienceRepo := persistence.Audience{DB: db, Changes: changes}
	sharedAI := persistence.NewProducer(djsRepo)
	sharedAI.Changes = changes
	if err = sharedAI.AdoptLegacyKey(ctx); err != nil {
		return err
	}
	textProviders := inference.New(sharedAI, producerAI.New())
	presenter := ai.New(persistence.ProducerKeys{Producer: sharedAI}, persistence.PresenterAssets{DB: db}, production.FFmpeg{Path: ffmpeg})
	promptStore := persistence.Prompts{DB: db}
	presenter.Prompts = promptStore
	presenter.Models = sharedAI
	presenter.Text = textProviders
	episodeStore := persistence.Episodes{DB: db, Changes: changes}
	episodeService := &episodes.Service{Store: episodeStore, Pool: config, Text: textProviders, Fallback: presenter}
	episodeService.ReportPlanning = func(o schedule.Occurrence, planningErr error) {
		at := time.Now()
		kind, reason := "ai", "DJ selection delayed. "+agentai.FailureMessage(agentai.FailureCode(planningErr))+" Automatic preparation will retry."
		if planningErr == nil {
			kind = "recovery"
			reason = "DJ selection recovered; an accepted track is ready."
		}
		recorder.Record(operations.Event{ID: operations.ID(), StationID: o.StationID, ShowID: o.ShowID, Show: o.Title, DJID: o.DJ.ID, DJ: o.DJ.Name, PlaylistID: o.PlaylistID, Playlist: o.PlaylistName, Started: at, Ended: at, Kind: kind, Outcome: "observed", Phase: "curation", Reason: reason, IncidentCode: "episode-plan:" + o.ShowID + ":" + o.Start.UTC().Format(time.RFC3339), Recover: planningErr == nil, Severity: "warning", Effect: "Local music continues", Action: "Review curation connection settings, episode details and server logs."})
	}
	presenter.Session = episodeService
	broadcasts := broadcast.New(config, broadcast.FFmpeg{Path: ffmpeg, Sources: sources, Factory: upnp.Factory}, logger, broadcast.Options{Operations: recorder, History: config, Mixer: mixerConfig, StreamBuffer: *streamBuffer, MaxStations: *maxBroadcasts, MaxListeners: *maxListeners, Schedule: shows, Producer: presenter, Links: presenter, Selector: episodeService, Session: episodeService, Audience: audienceRepo})
	episodeService.Eligibility = broadcasts
	stopEpisodes := episodeService.Start(ctx, stations, shows, changes, logger)
	defer stopEpisodes()
	defer broadcasts.Close()
	playlistsRepo := persistence.Playlists{DB: db}
	images := producerAI.NewImages(sharedAI)
	images.Prompts = promptStore
	assistant, err := producer.New(ctx, sharedAI, producer.NewRegistry(producer.Dependencies{Episodes: episodeService, Images: images, Stations: stations, Sources: sources, Catalogue: cat, Playlists: playlistsRepo, DJs: djsRepo, Schedule: shows, Broadcast: config, Runtime: broadcasts, Sync: syncer, Mixer: mixerConfig, Audience: audienceRepo}), textProviders)
	if err != nil {
		return err
	}
	defer assistant.Close()
	assistant.Prompts = promptStore
	handler, err := web.New(web.Dependencies{Episodes: episodeService, Changes: changes, Prompts: promptStore, Operations: operationalStore, Recording: recorder, Images: images, SourceDiscovery: &upnp.Discovery{}, Assistant: assistant, Audience: audienceRepo, Mixer: mixerConfig, ArtworkFactory: upnp.Factory, Schedule: shows, DJs: djsRepo, Broadcast: broadcasts, BroadcastConfig: config, PublicURL: *publicURL, Stations: stations, Sources: sources, Catalogue: cat, Playlists: persistence.Playlists{DB: db}, Sync: syncer, Ready: db.PingContext, Logger: logger, Password: password})
	if err != nil {
		return err
	}
	server := &http.Server{BaseContext: func(net.Listener) context.Context { return ctx }, Addr: *listen, Handler: handler, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 15 * time.Second, WriteTimeout: 150 * time.Second, IdleTimeout: 60 * time.Second, MaxHeaderBytes: 32 << 10}
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
