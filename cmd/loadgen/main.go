package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net/http"
	"sync"
	"sync/atomic"
	"time"
)

func main() {
	api := flag.String("api", "http://localhost:8080", "API base URL")
	n := flag.Int("n", 200, "number of URLs to register")
	urlPattern := flag.String("url", "https://example.com/?id=%d", "URL pattern for generated checks (%%d replaced with index)")
	flag.Parse()

	log.Printf("registering %d URLs against %s", *n, *api)

	var registered atomic.Int64
	sem := make(chan struct{}, 20)
	var wg sync.WaitGroup

	for i := 0; i < *n; i++ {
		wg.Add(1)
		sem <- struct{}{}
		go func(i int) {
			defer wg.Done()
			defer func() { <-sem }()

			endpoint := fmt.Sprintf(*urlPattern, i)
			if err := register(*api, endpoint); err != nil {
				log.Printf("register %d: %v", i, err)
				return
			}
			if v := registered.Add(1); v%50 == 0 || int(v) == *n {
				log.Printf("registered %d/%d", v, *n)
			}
		}(i)
	}
	wg.Wait()
	log.Printf("all %d URLs registered — waiting for scheduler tick to enqueue jobs...", *n)

	// Poll until queue fills (first non-zero reading) then until it drains.
	seenWork := false
	for {
		depth, err := queueDepth(*api)
		if err != nil {
			log.Printf("queue depth: %v — retrying", err)
			time.Sleep(2 * time.Second)
			continue
		}
		log.Printf("queue depth: %d", depth)
		if depth > 0 {
			seenWork = true
		}
		if seenWork && depth == 0 {
			log.Println("queue drained — done")
			return
		}
		time.Sleep(2 * time.Second)
	}
}

func register(api, endpoint string) error {
	body, _ := json.Marshal(map[string]string{"endpoint": endpoint})
	resp, err := http.Post(api+"/checks", "application/json", bytes.NewReader(body))
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		return fmt.Errorf("unexpected status %d", resp.StatusCode)
	}
	return nil
}

func queueDepth(api string) (int, error) {
	resp, err := http.Get(api + "/metrics/queue-depth")
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	var result struct {
		Pending int `json:"pending"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return 0, err
	}
	return result.Pending, nil
}
