# gorm-cache

[![Go](https://github.com/woorui/gorm-cache/actions/workflows/go.yml/badge.svg)](https://github.com/woorui/gorm-cache/actions/workflows/go.yml)

## Overview

gormcache is cache plugin for https://gorm.io (v2 version), It sets cache and auto expires cache after a specified duration.

It don't delete or update the cache when sql do deleting or updating.

I use gormcache to cache tables with high select frequency but low update frequency. like come config or common resource.

#### Some option api

1. It Supports a variety of cache driver by implementing `CacheKV` interface.

2. It uses sql statement as key, sql result as value; you can use `CacheKeyFunc` to change cache key, just like use `pk` as the cache key.

3. Implement `Codec` interface to use your favorite codec, like `protobuf`, `yaml`, `json-iterator`, `fast-json` and more.

4. Use `Models` to make the model cachable, There is an error if model emptys.

5. Use `errors.Is(err, new(ErrGormCache))` to determine if it's gormcache error.

## Quick Start

```go
package main

import (
	"context"
	"database/sql"
	"time"

	"github.com/go-redis/redis/v8"
	gormcache "github.com/woorui/gorm-cache"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

type User struct {
	gorm.Model
	Name   string `gorm:"type:text;not null;"`
	Gender uint   `gorm:"type:int8;not null;"`
}

// Rdb implements the CacheKV interface
type Rdb struct {
	client *redis.Client
}

func RedisKV(client *redis.Client) gormcache.CacheKV { return &Rdb{client: client} }

func (r *Rdb) Get(ctx context.Context, key string) (bool, string, error) {
	val, err := r.client.Get(ctx, key).Result()
	if err != nil && err != redis.Nil {
		return false, "", err
	}
	return err == redis.Nil, val, nil
}

func (r *Rdb) Set(ctx context.Context, key, value string, exp time.Duration) error {
	return r.client.SetEX(ctx, key, value, exp).Err()
}

func main() {
	sqlDB, err := sql.Open("postgres", "postgres://admin:123456@127.0.0.1:5432/user")
	if err != nil {
		panic(err)
	}
	db, err := gorm.Open(postgres.New(postgres.Config{Conn: sqlDB}))
	if err != nil {
		panic(err)
	}

	rdb := redis.NewClient(&redis.Options{Addr: "127.0.0.1:6379"})

	db.Use(gormcache.GormCache(RedisKV(rdb), 5*time.Second, gormcache.Models(new(User))))

	if err := db.Model(&User{}).Create(&User{Name: "Mike", Gender: 1}).Error; err != nil {
		panic(err)
	}

	// cached.
	user := &User{}
	if err := db.Model(&User{}).First(user).Where("name=?", "Mike"); err != nil {
		panic(err)
	}

	// cached.
	users := []User{}
	if err := db.Model(&User{}).Find(&users).Where("gender=?", 1); err != nil {
		panic(err)
	}
}
```
