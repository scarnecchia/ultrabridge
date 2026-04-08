package main

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	gocaldav "github.com/emersion/go-webdav/caldav"
	"golang.org/x/crypto/bcrypt"

	"github.com/sysop/ultrabridge/internal/auth"
	"github.com/sysop/ultrabridge/internal/booxpipeline"
	ubcaldav "github.com/sysop/ultrabridge/internal/caldav"
	ubwebdav "github.com/sysop/ultrabridge/internal/webdav"
	"github.com/sysop/ultrabridge/internal/config"
	"github.com/sysop/ultrabridge/internal/db"
	"github.com/sysop/ultrabridge/internal/logging"
	"github.com/sysop/ultrabridge/internal/notedb"
	"github.com/sysop/ultrabridge/internal/notestore"
	"github.com/sysop/ultrabridge/internal/pipeline"
	"github.com/sysop/ultrabridge/internal/processor"
	"github.com/sysop/ultrabridge/internal/rag"
	"github.com/sysop/ultrabridge/internal/search"
	"github.com/sysop/ultrabridge/internal/sync"
	"github.com/sysop/ultrabridge/internal/taskdb"
	"github.com/sysop/ultrabridge/internal/tasksync"
	"github.com/sysop/ultrabridge/internal/tasksync/supernote"
	"github.com/sysop/ultrabridge/internal/web"
)

// syncProviderAdapter wraps tasksync.SyncEngine to satisfy web.SyncStatusProvider.
type syncProviderAdapter struct{ engine *tasksync.SyncEngine }

func (a *syncProviderAdapter) Status() web.SyncStatus {
	s := a.engine.Status()
	return web.SyncStatus{
		LastSyncAt:    s.LastSyncAt,
		NextSyncAt:    s.NextSyncAt,
		InProgress:    s.InProgress,
		LastError:     s.LastError,
		AdapterID:     s.AdapterID,
		AdapterActive: s.AdapterActive,
	}
}

func (a *syncProviderAdapter) TriggerSync() { a.engine.TriggerSync() }

func main() {
	if len(os.Args) >= 3 && os.Args[1] == "hash-password" {
		hash, err := bcrypt.GenerateFromPassword([]byte(os.Args[2]), 10)
		if err != nil {
			fmt.Fprintf(os.Stderr, "ultrabridge: %v\n", err)
			os.Exit(1)
		}
		fmt.Println(string(hash))
		return
	}

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

	// Connect to Supernote MariaDB.
	// Required when SN sync is enabled or notes pipeline uses catalog sync.
	// Non-fatal when sync is disabled — task store is SQLite-only.
	database, err := db.Connect(cfg.DSN())
	if err != nil {
		if cfg.SNSyncEnabled {
			logger.Error("database connection failed (required for sync)", "error", err)
			os.Exit(1)
		}
		logger.Warn("database connection failed, notes catalog sync disabled", "error", err)
		// database is nil — catalog updater won't be set, which is nil-guarded below
	}
	if database != nil {
		defer database.Close()
	}

	var userID int64
	if database != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()

		userID, err = db.ResolveUserID(ctx, database, cfg.UserID)
		if err != nil {
			if cfg.SNSyncEnabled {
				logger.Error("user resolution failed (required for sync)", "error", err)
				os.Exit(1)
			}
			logger.Warn("user resolution failed", "error", err)
		} else if cfg.UserID != 0 {
			logger.Info("using configured user_id", "user_id", userID)
		} else {
			logger.Info("discovered user_id", "user_id", userID)
		}
	}

	// Open the task SQLite DB
	taskDB, err := taskdb.Open(context.Background(), cfg.TaskDBPath)
	if err != nil {
		logger.Error("taskdb open failed", "err", err, "path", cfg.TaskDBPath)
		os.Exit(1)
	}
	defer taskDB.Close()

	store := taskdb.NewStore(taskDB)

	// Run migration if task DB is empty and SPC sync is enabled
	if cfg.SNSyncEnabled {
		isEmpty, err := store.IsEmpty(context.Background())
		if err != nil {
			logger.Error("taskdb empty check failed", "err", err)
			os.Exit(1)
		}
		if isEmpty {
			logger.Info("empty task DB detected, attempting migration from SPC")
			migClient := supernote.NewClient(cfg.SNAPIURL, cfg.SNAccount, cfg.SNPassword, logger)
			if err := migClient.Login(context.Background()); err != nil {
				logger.Warn("SPC login failed for migration, starting with empty store", "error", err)
			} else {
				sm := tasksync.NewSyncMap(taskDB)
				count, err := supernote.MigrateFromSPC(context.Background(), migClient, store, sm, logger)
				if err != nil {
					logger.Warn("migration from SPC failed", "error", err)
				} else {
					logger.Info("migrated tasks from SPC", "count", count)
				}
			}
		} else {
			logger.Info("task DB populated, skipping migration")
		}
	}

	notifier := sync.NewNotifier(cfg.SocketIOURL, logger)
	notifier.Connect(context.Background())
	defer notifier.Close()

	// Open the notes SQLite DB (separate from Supernote's MariaDB)
	noteDB, err := notedb.Open(context.Background(), cfg.DBPath)
	if err != nil {
		logger.Error("notedb open failed", "err", err, "path", cfg.DBPath)
		os.Exit(1)
	}
	defer noteDB.Close()

	// Notes pipeline components
	ns := notestore.New(noteDB, cfg.NotesPath)
	si := search.New(noteDB)

	// Initialize embedding pipeline if enabled
	var embedder rag.Embedder
	var embedStore *rag.Store
	var backfillCancel context.CancelFunc
	if cfg.EmbedEnabled {
		embedder = rag.NewOllamaEmbedder(cfg.OllamaURL, cfg.OllamaEmbedModel, logger)
		embedStore = rag.NewStore(noteDB, logger)

		// Load existing embeddings into memory (AC1.6)
		n, err := embedStore.LoadAll(context.Background())
		if err != nil {
			logger.Warn("failed to load embeddings into cache", "err", err)
		} else {
			logger.Info("loaded embeddings into memory", "count", n)
		}

		// Startup backfill (AC1.4) — runs in background with cancellable context
		var backfillCtx context.Context
		backfillCtx, backfillCancel = context.WithCancel(context.Background())
		go func() {
			n, err := rag.Backfill(backfillCtx, embedStore, embedder, cfg.OllamaEmbedModel, logger)
			if err != nil {
				logger.Warn("startup backfill failed", "err", err)
			} else if n > 0 {
				logger.Info("startup backfill complete", "embedded", n)
			}
		}()
	}

	// Create retriever if embedding is available (also works FTS-only when embedStore is nil)
	var retriever *rag.Retriever
	if embedStore != nil {
		retriever = rag.NewRetriever(noteDB, si, embedStore, embedder, logger)
	} else {
		// FTS-only mode: retriever works without embeddings
		retriever = rag.NewRetriever(noteDB, si, nil, nil, logger)
	}

	workerCfg := processor.WorkerConfig{
		OCREnabled: cfg.OCREnabled,
		BackupPath: cfg.BackupPath,
		MaxFileMB:  cfg.OCRMaxFileMB,
		Indexer:    si,
		Embedder:   embedder,
		EmbedModel: cfg.OllamaEmbedModel,
		EmbedStore: embedStore,
		OCRPrompt: func() string {
			v, _ := notedb.GetSetting(context.Background(), noteDB, "sn_ocr_prompt")
			return v
		},
	}
	if database != nil {
		workerCfg.CatalogUpdater = processor.NewSPCCatalog(database)
	}
	if cfg.OCREnabled && cfg.OCRAPIURL != "" {
		workerCfg.OCRClient = processor.NewOCRClient(cfg.OCRAPIURL, cfg.OCRAPIKey, cfg.OCRModel, cfg.OCRFormat)
	}
	proc := processor.New(noteDB, workerCfg)
	if cfg.OCREnabled {
		if err := proc.Start(context.Background()); err != nil {
			logger.Warn("processor start failed", "err", err)
		}
		defer proc.Stop()
	}

	pl := pipeline.New(pipeline.Config{
		NotesPath: cfg.NotesPath,
		Store:     ns,
		Proc:      proc,
		Events:    notifier.Events(),
		Logger:    logger,
	})
	pl.Start(context.Background())
	defer pl.Close()

	// Wire Boox pipeline if enabled
	var booxProc *booxpipeline.Processor
	if cfg.BooxEnabled && cfg.BooxNotesPath != "" {
		booxCfg := booxpipeline.WorkerConfig{
			Indexer:        si,  // shared search.Store (same as Supernote)
			ContentDeleter: si,  // search.Store also satisfies ContentDeleter
			CachePath:      filepath.Join(cfg.BooxNotesPath, ".cache"),
			Embedder:       embedder,
			EmbedModel:     cfg.OllamaEmbedModel,
			EmbedStore:     embedStore,
			OCRPrompt: func() string {
				v, _ := notedb.GetSetting(context.Background(), noteDB, "boox_ocr_prompt")
				return v
			},
			TodoEnabled: func() bool {
				v, _ := notedb.GetSetting(context.Background(), noteDB, "boox_todo_enabled")
				return v == "true"
			},
			TodoPrompt: func() string {
				v, _ := notedb.GetSetting(context.Background(), noteDB, "boox_todo_prompt")
				return v
			},
			OnTodosFound: func(ctx context.Context, notePath string, todos []booxpipeline.TodoItem) {
				created := booxpipeline.CreateTasksFromTodos(ctx, store, notePath, todos, logger)
				if created > 0 && notifier != nil {
					notifier.Notify(ctx)
				}
			},
		}
		if cfg.OCREnabled && cfg.OCRAPIURL != "" {
			booxCfg.OCR = processor.NewOCRClient(cfg.OCRAPIURL, cfg.OCRAPIKey, cfg.OCRModel, cfg.OCRFormat)
		}
		booxProc = booxpipeline.New(noteDB, cfg.BooxNotesPath, booxCfg, logger)
		if err := booxProc.Start(context.Background()); err != nil {
			logger.Warn("boox processor start failed", "err", err)
		} else {
			defer booxProc.Stop()
		}
		// Sync import path from env var to settings DB so the web handler can read it.
		if cfg.BooxImportPath != "" {
			notedb.SetSetting(context.Background(), noteDB, "boox_import_path", cfg.BooxImportPath)
		}
	}

	// Start sync engine if enabled
	var syncEngine *tasksync.SyncEngine
	if cfg.SNSyncEnabled {
		syncEngine = tasksync.NewSyncEngine(
			store, taskDB, logger,
			time.Duration(cfg.SNSyncInterval)*time.Second,
		)
		snAdapter := supernote.NewAdapter(cfg.SNAPIURL, cfg.SNAccount, cfg.SNPassword, notifier, logger)
		syncEngine.RegisterAdapter(snAdapter)
		if err := syncEngine.Start(context.Background()); err != nil {
			logger.Warn("sync engine start failed", "error", err)
		} else {
			defer syncEngine.Stop()
		}
	}

	backend := ubcaldav.NewBackend(store, "/caldav", cfg.CalDAVCollectionName, cfg.DueTimeMode, notifier)
	caldavHandler := &gocaldav.Handler{
		Backend: backend,
		Prefix:  "/caldav",
	}

	authMW := auth.New(cfg.Username, cfg.PasswordHash)

	// Create log broadcaster for web UI
	broadcaster := logging.NewLogBroadcaster()

	// Wire the broadcasting handler to capture logs
	broadcastHandler := logging.NewBroadcastingHandler(logger.Handler(), broadcaster)
	logger = slog.New(broadcastHandler)

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

	// Wire Boox WebDAV server if enabled
	if cfg.BooxEnabled && cfg.BooxNotesPath != "" {
		davHandler := ubwebdav.NewHandler(cfg.BooxNotesPath, func(absPath string) {
			logger.Info("boox note uploaded", "path", absPath)
			if booxProc != nil {
				if err := booxProc.Enqueue(context.Background(), absPath); err != nil {
					logger.Error("enqueue boox job", "error", err, "path", absPath)
				}
			}
		})
		mux.Handle("/webdav/", authMW.Wrap(davHandler))
		logger.Info("boox webdav enabled", "path", cfg.BooxNotesPath)
	}

	// Wire web UI if enabled
	if cfg.WebEnabled {
		// If sync is enabled, wrap syncEngine for web UI; otherwise nil
		var syncProvider web.SyncStatusProvider
		if cfg.SNSyncEnabled && syncEngine != nil {
			syncProvider = &syncProviderAdapter{engine: syncEngine}
		}
		// If Boox is enabled, pass the store and processor; otherwise nil
		var booxStore web.BooxStore
		var booxImporter web.BooxImporter
		if booxProc != nil {
			booxStore = booxProc.Store()
			booxImporter = booxProc
		}
		webHandler := web.NewHandler(store, notifier, ns, si, proc, pl, syncProvider, booxStore, booxImporter, cfg.BooxNotesPath, cfg.NotesPath, noteDB, logger, broadcaster, embedder, embedStore, cfg.OllamaEmbedModel, retriever)
		mux.Handle("/", authMW.Wrap(webHandler))
	}

	handler := logging.RequestID(logger)(mux)

	// Setup graceful shutdown with signal handling
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	// Start HTTP server in a goroutine so we can wait for signals
	serverErr := make(chan error, 1)
	go func() {
		logger.Info("ultrabridge starting", "addr", cfg.ListenAddr)
		serverErr <- http.ListenAndServe(cfg.ListenAddr, handler)
	}()

	// Wait for shutdown signal or server error
	select {
	case err := <-serverErr:
		if err != nil {
			logger.Error("server error", "error", err)
			os.Exit(1)
		}
	case sig := <-sigChan:
		logger.Info("shutdown signal received", "signal", sig)

		// Cancel the backfill goroutine
		if backfillCancel != nil {
			backfillCancel()
		}
	}
}
