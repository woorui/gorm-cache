package cachekv

import (
	"context"
	"time"

	"github.com/go-redis/redis/v8"
	gormcache "github.com/woorui/gorm-cache"
)

type Rdb struct {
	client *redis.Client
}

func RedisKV(client *redis.Client) gormcache.CacheKV { return &Rdb{client: client} }

func (r *Rdb) Get(ctx context.Context, key string) (bool, string, error) {
	val, err := r.client.Get(ctx, key).Result()
	if err != nil {
		if err == redis.Nil {
			return false, "", nil
		}
		return false, "", err
	}
	return true, val, nil
}

func (r *Rdb) Set(ctx context.Context, key, value string, exp time.Duration) error {
	return r.client.SetEX(ctx, key, value, exp).Err()
}
