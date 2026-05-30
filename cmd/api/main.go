package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"
	"time"

	"github.com/siqiliu18/healthwatch/internal/api"
	"github.com/siqiliu18/healthwatch/internal/store"
)

func main() {
	ctx := context.Background()

	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		log.Fatal("DATABASE_URL is required")
	}

	pool, err := store.Connect(ctx, dsn)
	if err != nil {
		log.Fatalf("connect to database: %v", err)
	}
	defer pool.Close()

	s := store.NewPostgresStore(pool)
	srv := api.NewServer(s)

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

	log.Printf("API server listening on :%s", port)
	if err := server.ListenAndServe(); err != nil {
		log.Fatalf("server: %v", err)
	}
}
