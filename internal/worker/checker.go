package worker

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/siqiliu18/healthwatch/internal/store"
)

func ping(ctx context.Context, endpoint string, timeout time.Duration) store.CheckResultInput {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		msg := err.Error()
		return store.CheckResultInput{Error: &msg}
	}

	start := time.Now()
	resp, err := http.DefaultClient.Do(req)
	ms := int(time.Since(start).Milliseconds())
	if err != nil {
		msg := err.Error()
		return store.CheckResultInput{DurationMs: &ms, Error: &msg}
	}
	defer resp.Body.Close()

	code := resp.StatusCode
	text := fmt.Sprintf("%d %s", code, http.StatusText(code))
	return store.CheckResultInput{
		StatusCode: &code,
		StatusText: &text,
		DurationMs: &ms,
	}
}
