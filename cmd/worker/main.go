package main

import (
	"context"
	"log"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/siqiliu18/healthwatch/internal/store"
	"github.com/siqiliu18/healthwatch/internal/worker"
)

func main() {
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		log.Fatal("DATABASE_URL is required")
	}

	pingTimeout := 5 * time.Second
	if v := os.Getenv("PING_TIMEOUT"); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			pingTimeout = d
		}
	}

	concurrency := 4
	if v := os.Getenv("WORKER_CONCURRENCY"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			concurrency = n
		}
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, os.Interrupt)
	defer stop()

	pool, err := store.Connect(ctx, dsn)
	if err != nil {
		log.Fatalf("connect to database: %v", err)
	}
	defer pool.Close()

	ps := store.NewPostgresStore(pool)

	var cache store.Cache
	if addr := os.Getenv("REDIS_ADDR"); addr != "" {
		rc := store.NewRedisCache(addr)
		defer rc.Close()
		cache = rc
	} else {
		log.Println("REDIS_ADDR not set, running without cache")
	}

	w := worker.NewWorker(ps, cache, pingTimeout, concurrency)

	log.Printf("worker starting (concurrency=%d, pingTimeout=%s)", concurrency, pingTimeout)
	w.Run(ctx)
	log.Println("worker stopped")
}
