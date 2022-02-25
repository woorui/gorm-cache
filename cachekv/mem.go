package cachekv

import (
	"context"
	"sync"
	"time"

	gormcache "github.com/woorui/gorm-cache"
)

type Mdb struct {
	data    *sync.Map
	expires *sync.Map
}

func MemKV() gormcache.CacheKV {
	result := &Mdb{
		data:    &sync.Map{},
		expires: &sync.Map{},
	}
	return result
}

func (kv *Mdb) Get(ctx context.Context, key string) (bool, string, error) {
	ex, ok := kv.expires.Load(key)
	if ok {
		ext := ex.(time.Time)
		if ext.Before(time.Now()) {
			kv.expires.Delete(key)
			kv.data.Delete(key)
			return false, "", nil
		}
		val, ok := kv.data.Load(key)
		if ok {
			return true, val.(string), nil
		}
	}
	return false, "", nil
}

func (kv *Mdb) Set(ctx context.Context, key string, value string, exp time.Duration) error {
	kv.data.Store(key, value)
	kv.expires.Store(key, time.Now().Add(exp))
	return nil
}
