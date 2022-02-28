# gorm-cache

[![Go](https://github.com/woorui/gorm-cache/actions/workflows/go.yml/badge.svg)](https://github.com/woorui/gorm-cache/actions/workflows/go.yml)

## Overview

gormcache is cache plugin for gorm.io/gorm.

gormcache uses sql statement as key, sql result as value.

It sets cache and auto expires cache after the specified duration.

It can't delete or update the cache when sql do deleting or updating.

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

	db.Use(gormcache.GormCache(RedisKV(rdb), 5*time.Second, gormcache.Models()))

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
