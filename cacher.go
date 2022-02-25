package gormcache

import (
	"context"
	"reflect"
	"time"

	json "github.com/json-iterator/go"
	"golang.org/x/sync/singleflight"
	"gorm.io/gorm"
	"gorm.io/gorm/callbacks"
)

var Name = "gorm-cache"

type nocachectx struct{}

// CacheKV cache the sql result.
type CacheKV interface {
	Get(ctx context.Context, key string) (bool, string, error)
	Set(ctx context.Context, key, value string, exp time.Duration) error
}

// ErrGormCache error for gorm-cache.
type ErrGormCache struct {
	err error
}

func (e *ErrGormCache) Error() string { return Name + ":" + e.err.Error() }

// NewErrGormCache return ErrGormCache.
func NewErrGormCache(err error) error {
	if err != nil {
		return &ErrGormCache{err: err}
	}
	return nil
}

// GormCache is cache plugin for gorm.io/gorm.
type GormCache struct {
	kv                     CacheKV
	queryAfterRegisterName string
	schemaNames            map[string]bool
	exp                    time.Duration
	requestGroup           singleflight.Group
	cacheKeyFunc           func(*gorm.DB) string
}

// RegisterGormCache implements `gorm.Plugin` interface, exp is auto expires cache duration,
// Schemas is gorm schema to be cached.
func RegisterGormCache(kv CacheKV, exp time.Duration, options ...Option) *GormCache {
	cacher := &GormCache{
		kv:                     kv,
		queryAfterRegisterName: Name + ":after_query",
		exp:                    exp,
		cacheKeyFunc: func(db *gorm.DB) string {
			return db.Dialector.Explain(db.Statement.SQL.String(), db.Statement.Vars...)
		},
	}
	for _, o := range options {
		o(cacher)
	}
	return cacher
}

// Name `gorm.Plugin` implements.
func (g *GormCache) Name() string { return Name }

// Initialize `gorm.Plugin` implements.
func (g *GormCache) Initialize(db *gorm.DB) error {
	if err := db.Callback().Query().Replace("gorm:query", g.query); err != nil {
		return NewErrGormCache(err)
	}
	if err := db.Callback().Query().After("gorm:query").Register(
		g.queryAfterRegisterName,
		g.queryAfter,
	); err != nil {
		return NewErrGormCache(err)
	}
	return nil
}

// query replace gorm:query
func (g *GormCache) query(db *gorm.DB) {
	if db.DryRun || db.Error != nil {
		return
	}
	callbacks.BuildQuerySQL(db)

	if g.canCache(db) {
		cachekey := g.cacheKeyFunc(db)

		ok, value, err := g.kv.Get(db.Statement.Context, cachekey)
		if err != nil {
			db.AddError(NewErrGormCache(err))
			return
		}
		if ok {
			g.unmarshalToDB(value, db)
			return
		}
		g.requestGroup.Do(cachekey, func() (interface{}, error) {
			queryFromDB(db)
			return nil, db.Error
		})
	}
	queryFromDB(db)
}

// queryFromDB query from database.
func queryFromDB(db *gorm.DB) {
	db.Statement.Context = context.WithValue(db.Statement.Context, nocachectx{}, 1)
	rows, err := db.Statement.ConnPool.QueryContext(db.Statement.Context, db.Statement.SQL.String(), db.Statement.Vars...)
	if err != nil {
		db.AddError(err)
		return
	}
	defer rows.Close()
	gorm.Scan(rows, db, 0)
}

// queryAfter set cache to kv after query.
func (g *GormCache) queryAfter(db *gorm.DB) {
	if v := db.Statement.Context.Value(nocachectx{}); v == nil {
		return
	}
	if db.Error != nil || db.Statement.Schema == nil || !g.canCache(db) {
		return
	}
	ok, value, err := g.queryResult(db)
	if !ok {
		return
	}
	key := g.cacheKeyFunc(db)
	if err != nil {
		db.AddError(NewErrGormCache(err))
		return
	}
	if err := g.kv.Set(db.Statement.Context, key, value, g.exp); err != nil {
		db.AddError(NewErrGormCache(err))
		return
	}
}

// canCache checks whether the current sql is cacheable
func (g *GormCache) canCache(db *gorm.DB) bool {
	tname := structType(db.Statement.ReflectValue.Interface()).Name()
	_, ok := g.schemaNames[tname]
	return ok
}

// queryResult get sql result as a string. only support struct or struct slice and array here.
// if not support, return false and empty string.
func (g *GormCache) queryResult(db *gorm.DB) (bool, string, error) {
	var (
		fields       = db.Statement.Schema.Fields
		reflectValue = db.Statement.ReflectValue
	)
	switch reflectValue.Kind() {
	case reflect.Struct:
		valueData := make(map[string]interface{})
		for _, field := range fields {
			if fieldValue, isZero := field.ValueOf(db.Statement.Context, reflectValue); !isZero {
				valueData[field.Name] = fieldValue
			}
		}
		val, err := json.MarshalToString(valueData)
		return true, val, err
	case reflect.Slice, reflect.Array:
		lens := reflectValue.Len()
		valueArrData := make([]map[string]interface{}, lens)
		for _, field := range fields {
			for i := 0; i < lens; i++ {
				if fieldValue, isZero := field.ValueOf(db.Statement.Context, reflectValue.Index(i)); !isZero {
					if valueArrData[i] == nil {
						valueArrData[i] = make(map[string]interface{})
					}
					valueArrData[i][field.Name] = fieldValue
				}
			}
		}
		val, err := json.MarshalToString(valueArrData)
		return true, val, err
	default:
		db.Logger.Info(db.Statement.Context, "gorm-cache: %s, type not support", reflectValue.Kind().String())
	}
	return false, "", nil
}

// unmarshalToDB unmarshal cache value to gorm.DB, set data to dest.
func (g *GormCache) unmarshalToDB(cacheValue string, db *gorm.DB) {
	reflectValue := db.Statement.ReflectValue
	err := json.UnmarshalFromString(cacheValue, db.Statement.Dest)
	if err != nil {
		db.AddError(err)
		return
	}
	elem := reflect.ValueOf(db.Statement.Dest)
	if !elem.CanSet() {
		elem = elem.Elem()
	}
	switch reflectValue.Kind() {
	case reflect.Struct:
		db.RowsAffected = 1
	case reflect.Slice, reflect.Array:
		db.RowsAffected = int64(elem.Len())
	default:
		db.Logger.Info(db.Statement.Context, "gorm-cache: %s, type not support", reflectValue.Kind().String())
	}
	reflectValue.Set(elem)
}

// structType get struct type name for checking whether the schema can be cached.
func structType(val interface{}) reflect.Type {
	typ := reflect.TypeOf(val)
	for typ.Kind() == reflect.Ptr {
		typ = typ.Elem()
	}
	var t reflect.Type
	switch typ.Kind() {
	case reflect.Array, reflect.Slice:
		for typ.Elem().Kind() == reflect.Ptr {
			typ = typ.Elem()
		}
		t = typ.Elem()
	default:
		t = typ
	}
	return t
}

type Option func(*GormCache)

// CacheKeyFunc allow you diy cache key for CacheKV.
func CacheKeyFunc(f func(*gorm.DB) string) Option {
	return func(gc *GormCache) {
		gc.cacheKeyFunc = f
	}
}

// Schemas accept schema that you want to be cached.
func Schemas(schemas ...interface{}) Option {
	return func(gc *GormCache) {
		schemaNames := make(map[string]bool, len(schemas))
		for _, item := range schemas {
			schemaNames[structType(item).Name()] = true
		}
		gc.schemaNames = schemaNames
	}
}
