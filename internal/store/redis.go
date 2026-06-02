package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"

	"github.com/siqiliu18/healthwatch/internal/model"
)

const resultTTL = 5 * time.Minute

type Cache interface {
	GetLatestResult(ctx context.Context, checkID uuid.UUID) (*model.CheckResult, error)
	SetLatestResult(ctx context.Context, checkID uuid.UUID, result *model.CheckResult) error
}

type RedisCache struct {
	client *redis.Client
}

func NewRedisCache(addr string) *RedisCache {
	return &RedisCache{client: redis.NewClient(&redis.Options{Addr: addr})}
}

func (c *RedisCache) Close() error {
	return c.client.Close()
}

func (c *RedisCache) GetLatestResult(ctx context.Context, checkID uuid.UUID) (*model.CheckResult, error) {
	key := fmt.Sprintf("check:result:%s", checkID)
	data, err := c.client.Get(ctx, key).Bytes()
	if errors.Is(err, redis.Nil) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("redis get: %w", err)
	}
	var r model.CheckResult
	if err := json.Unmarshal(data, &r); err != nil {
		return nil, fmt.Errorf("unmarshal result: %w", err)
	}
	return &r, nil
}

func (c *RedisCache) SetLatestResult(ctx context.Context, checkID uuid.UUID, result *model.CheckResult) error {
	key := fmt.Sprintf("check:result:%s", checkID)
	data, err := json.Marshal(result)
	if err != nil {
		return fmt.Errorf("marshal result: %w", err)
	}
	if err := c.client.Set(ctx, key, data, resultTTL).Err(); err != nil {
		return fmt.Errorf("redis set: %w", err)
	}
	return nil
}
