package main

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"time"

	gocaldav "github.com/emersion/go-webdav/caldav"
	"github.com/sysop/ultrabridge/internal/auth"
	ubcaldav "github.com/sysop/ultrabridge/internal/caldav"
	"github.com/sysop/ultrabridge/internal/config"
	"github.com/sysop/ultrabridge/internal/db"
	"github.com/sysop/ultrabridge/internal/logging"
	"github.com/sysop/ultrabridge/internal/sync"
	"github.com/sysop/ultrabridge/internal/taskstore"
	"github.com/sysop/ultrabridge/internal/web"
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintf(os.Stderr, "ultrabridge: %v\n", err)
		os.Exit(1)
	}

	logger := logging.Setup(logging.Config{
		Level:         cfg.LogLevel,
		Format:        cfg.LogFormat,
		File:          cfg.LogFile,
		FileMaxMB:     cfg.LogFileMaxMB,
		FileMaxAge:    cfg.LogFileMaxAge,
		FileMaxBackup: cfg.LogFileMaxBackup,
		SyslogAddr:    cfg.LogSyslogAddr,
	})

	database, err := db.Connect(cfg.DSN())
	if err != nil {
		logger.Error("database connection failed", "error", err)
		os.Exit(1)
	}
	defer database.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	userID, err := db.DiscoverUserID(ctx, database)
	if err != nil {
		logger.Error("user discovery failed", "error", err)
		os.Exit(1)
	}
	logger.Info("discovered user_id", "user_id", userID)

	store := taskstore.New(database, userID)

	notifier := sync.NewNotifier(cfg.SocketIOURL, logger)
	notifier.Connect(context.Background())
	defer notifier.Close()

	backend := ubcaldav.NewBackend(store, "/caldav", cfg.CalDAVCollectionName, cfg.DueTimeMode, notifier)
	caldavHandler := &gocaldav.Handler{
		Backend: backend,
		Prefix:  "/caldav",
	}

	authMW := auth.New(cfg.Username, cfg.PasswordHash)

	// Create log broadcaster for web UI
	broadcaster := logging.NewLogBroadcaster()

	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"status":"ok"}`))
	})
	mux.Handle("/caldav/", authMW.Wrap(caldavHandler))
	mux.HandleFunc("/.well-known/caldav", func(w http.ResponseWriter, r *http.Request) {
		authMW.Wrap(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			http.Redirect(w, r, "/caldav/", http.StatusMovedPermanently)
		})).ServeHTTP(w, r)
	})

	// Wire web UI if enabled
	if cfg.WebEnabled {
		webHandler := web.NewHandler(store, notifier, logger, broadcaster)
		mux.Handle("/", authMW.Wrap(webHandler))
	}

	handler := logging.RequestID(logger)(mux)
	logger.Info("ultrabridge starting", "addr", cfg.ListenAddr)
	if err := http.ListenAndServe(cfg.ListenAddr, handler); err != nil {
		logger.Error("server error", "error", err)
		os.Exit(1)
	}
}
