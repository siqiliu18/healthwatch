package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/siqiliu18/healthwatch/internal/api"
	"github.com/siqiliu18/healthwatch/internal/store"
	"github.com/siqiliu18/healthwatch/internal/worker"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, os.Interrupt)
	defer stop()

	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		log.Fatal("DATABASE_URL is required")
	}

	pool, err := store.Connect(ctx, dsn)
	if err != nil {
		log.Fatalf("connect to database: %v", err)
	}
	defer pool.Close()

	checkFreq := 30 * time.Second
	if v := os.Getenv("CHECK_FREQUENCY"); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			checkFreq = d
		}
	}

	ps := store.NewPostgresStore(pool)

	sched := worker.NewScheduler(ps, checkFreq)
	go sched.Run(ctx)

	srv := api.NewServer(ps)

	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}

	server := &http.Server{
		Addr:         fmt.Sprintf(":%s", port),
		Handler:      srv,
		ReadTimeout:  10 * time.Second,
		WriteTimeout: 10 * time.Second,
	}

	go func() {
		<-ctx.Done()
		shutCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = server.Shutdown(shutCtx)
	}()

	log.Printf("API server listening on :%s", port)
	if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Fatalf("server: %v", err)
	}
}
