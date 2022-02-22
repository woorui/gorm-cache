# gorm-cache

gormcache is cache plugin for gorm.io/gorm.
It sets cache and auto expires cache after the specified duration.
It can't delete or update the cache when sql do deleting or updating.

How to use:

```go
db.Use(RegisterGormCache(CacheKV, roundExp, Schema(&User{}, &Pet{})))
```
