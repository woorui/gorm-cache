package gormcache

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"reflect"
	"time"

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

// Codec defines how to Marshal and Unmarshal cache data.
//
// the default Codec is std json.Marshal and json.Unmarshal.
type Codec interface {
	Marshal(interface{}) ([]byte, error)
	Unmarshal([]byte, interface{}) error
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

// GormCacher is cache plugin for gorm.io/gorm.
type GormCacher struct {
	kv                     CacheKV
	queryAfterRegisterName string
	modelNames             map[string]bool
	exp                    time.Duration
	requestGroup           singleflight.Group
	cacheKeyFunc           func(*gorm.DB) string
	codec                  Codec
}

// GormCache implements `gorm.Plugin` interface, exp is auto expires cache duration,
// Models is gorm model to be cached.
func GormCache(kv CacheKV, exp time.Duration, options ...Option) *GormCacher {
	cacher := &GormCacher{
		kv:                     kv,
		queryAfterRegisterName: Name + ":after_query",
		exp:                    exp,
		cacheKeyFunc: func(db *gorm.DB) string {
			return db.Dialector.Explain(db.Statement.SQL.String(), db.Statement.Vars...)
		},
		codec: &stdJsonCodec{},
	}
	for _, o := range options {
		o(cacher)
	}
	return cacher
}

// Name `gorm.Plugin` implements.
func (g *GormCacher) Name() string { return Name }

// Initialize `gorm.Plugin` implements.
func (g *GormCacher) Initialize(db *gorm.DB) error {
	if len(g.modelNames) == 0 {
		return NewErrGormCache(errors.New("call `Models` to add model that needs to be cached"))
	}
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
func (g *GormCacher) query(db *gorm.DB) {
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
		g.queryFromDB(db, cachekey)
		return
	}
	g.queryFromDB(db, "")
}

// queryFromDB query from database.
func (g *GormCacher) queryFromDB(db *gorm.DB, cachekey string) {
	var (
		rows *sql.Rows
		err  error
	)
	if cachekey == "" {
		rows, err = db.Statement.ConnPool.QueryContext(db.Statement.Context, db.Statement.SQL.String(), db.Statement.Vars...)
	} else {
		var any interface{}
		any, err, _ = g.requestGroup.Do(cachekey, func() (interface{}, error) {
			db.Statement.Context = context.WithValue(db.Statement.Context, nocachectx{}, 1)
			return db.Statement.ConnPool.QueryContext(db.Statement.Context, db.Statement.SQL.String(), db.Statement.Vars...)
		})
		rows = any.(*sql.Rows)
	}
	if err != nil {
		db.AddError(err)
		return
	}
	defer rows.Close()
	gorm.Scan(rows, db, 0)
}

// queryAfter set cache to kv after query.
func (g *GormCacher) queryAfter(db *gorm.DB) {
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
func (g *GormCacher) canCache(db *gorm.DB) bool {
	tname := structType(db.Statement.ReflectValue.Interface()).Name()
	_, ok := g.modelNames[tname]
	return ok
}

// queryResult get sql result as a string. only support struct or struct slice and array here.
// if not support, return false and empty string.
func (g *GormCacher) queryResult(db *gorm.DB) (bool, string, error) {
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
		val, err := json.Marshal(valueData)
		return true, string(val), err
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
		val, err := json.Marshal(valueArrData)
		return true, string(val), err
	default:
		db.Logger.Info(db.Statement.Context, "gorm-cache: %s, type not support", reflectValue.Kind().String())
	}
	return false, "", nil
}

// unmarshalToDB unmarshal cache value to gorm.DB, set data to dest.
func (g *GormCacher) unmarshalToDB(cacheValue string, db *gorm.DB) {
	reflectValue := db.Statement.ReflectValue
	err := json.Unmarshal([]byte(cacheValue), db.Statement.Dest)
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

// structType get struct type name for checking whether the model can be cached.
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

type Option func(*GormCacher)

// CacheKeyFunc allow you diy cache key for CacheKV.
func CacheKeyFunc(f func(*gorm.DB) string) Option {
	return func(gc *GormCacher) {
		gc.cacheKeyFunc = f
	}
}

// Models accept model that you want to be cached.
func Models(models ...interface{}) Option {
	return func(gc *GormCacher) {
		modelNames := make(map[string]bool, len(models))
		for _, item := range models {
			modelNames[structType(item).Name()] = true
		}
		gc.modelNames = modelNames
	}
}

// WithCodec inject your codec.
func WithCodec(codec Codec) Option {
	return func(gc *GormCacher) {
		gc.codec = codec
	}
}

type stdJsonCodec struct{}

func (m *stdJsonCodec) Marshal(v interface{}) ([]byte, error)      { return json.Marshal(v) }
func (m *stdJsonCodec) Unmarshal(data []byte, v interface{}) error { return json.Unmarshal(data, v) }
