package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"
	"time"

	"github.com/sysop/ultrabridge/internal/config"
	"github.com/sysop/ultrabridge/internal/db"
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintf(os.Stderr, "ultrabridge: %v\n", err)
		os.Exit(1)
	}

	database, err := db.Connect(cfg.DSN())
	if err != nil {
		fmt.Fprintf(os.Stderr, "ultrabridge: database connection failed: %v\n", err)
		os.Exit(1)
	}
	defer database.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	userID, err := db.DiscoverUserID(ctx, database)
	if err != nil {
		fmt.Fprintf(os.Stderr, "ultrabridge: user discovery failed: %v\n", err)
		os.Exit(1)
	}
	log.Printf("discovered user_id: %d", userID)

	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"status":"ok"}`))
	})

	log.Printf("ultrabridge starting on %s", cfg.ListenAddr)
	if err := http.ListenAndServe(cfg.ListenAddr, mux); err != nil {
		fmt.Fprintf(os.Stderr, "ultrabridge: %v\n", err)
		os.Exit(1)
	}
}
