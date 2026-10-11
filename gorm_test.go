package cachefunk

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"testing"
	"time"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

type HelloWorldParams struct {
	Name string
	Age  int64
}

// sqlRecorder is a gorm logger that collects the SQL of every statement it is
// asked to trace
type sqlRecorder struct {
	statements []string
}

func (r *sqlRecorder) LogMode(logger.LogLevel) logger.Interface { return r }
func (r *sqlRecorder) Info(context.Context, string, ...any)     {}
func (r *sqlRecorder) Warn(context.Context, string, ...any)     {}
func (r *sqlRecorder) Error(context.Context, string, ...any)    {}

func (r *sqlRecorder) Trace(ctx context.Context, begin time.Time, fc func() (string, int64), err error) {
	statement, _ := fc()
	r.statements = append(r.statements, statement)
}

// TestGORMStorageIndexes checks the indexes the storage depends on: (key, params)
// for lookup and upsert, (key, timestamp) for cleanup and expired counting. The
// second must also appear when an older table is migrated.
func TestGORMStorageIndexes(t *testing.T) {
	db, err := gorm.Open(sqlite.Open("file:indexes_test?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatal("failed to connect database")
	}
	if _, err := NewGORMStorage(db); err != nil {
		t.Fatalf("error while setting up gorm cache storage: %s", err)
	}

	for _, name := range []string{"idx_key_params", "idx_key_timestamp"} {
		if !db.Migrator().HasIndex(&CacheEntry{}, name) {
			t.Errorf("expected index %q to exist after migration", name)
		}
	}

	// a table written before the index existed gains it on the next migration
	if err := db.Migrator().DropIndex(&CacheEntry{}, "idx_key_timestamp"); err != nil {
		t.Fatalf("error while dropping idx_key_timestamp: %s", err)
	}
	if _, err := NewGORMStorage(db); err != nil {
		t.Fatalf("error while migrating again: %s", err)
	}
	if !db.Migrator().HasIndex(&CacheEntry{}, "idx_key_timestamp") {
		t.Error("expected the migration to recreate idx_key_timestamp on an existing table")
	}
}

// TestGORMStorageQuotesReservedColumns checks that column names reach the
// database quoted. gorm passes raw fragments such as "key = ?" through
// unquoted, and key is a reserved word in MySQL, which made every get, cleanup
// and expired count a syntax error there. sqlite does not care, so the generated
// SQL is what this test holds to.
func TestGORMStorageQuotesReservedColumns(t *testing.T) {
	db, err := gorm.Open(sqlite.Open("file::memory:?cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatal("failed to connect database")
	}

	if err := db.AutoMigrate(&CacheEntry{}); err != nil {
		t.Fatalf("error while migrating: %s", err)
	}

	recorder := &sqlRecorder{}
	storage := &GORMStorage{DB: db.Session(&gorm.Session{Logger: recorder})}
	expireBeforeTime := time.Now().UTC()

	storage.Get("key", DefaultKeyConfig, "params", expireBeforeTime)
	storage.Cleanup("key", DefaultKeyConfig, expireBeforeTime)
	storage.ExpiredEntryCount("key", DefaultKeyConfig, expireBeforeTime)

	if len(recorder.statements) != 3 {
		t.Fatalf("expected the 3 storage calls to reach the database but traced %d statements: %v", len(recorder.statements), recorder.statements)
	}

	// a column is only safe from reserved words when it is quoted: `key` = ?
	// rather than key = ?
	unquotedColumn := regexp.MustCompile("(^|[^`\\w])(key|timestamp|params)\\s*[=<>]")
	for _, statement := range recorder.statements {
		if !strings.Contains(statement, "`key`") {
			t.Errorf("expected the key column to be quoted in %q", statement)
		}
		if unquotedColumn.MatchString(statement) {
			t.Errorf("expected every column to be quoted in %q", statement)
		}
	}
}

func TestGORMStorage(t *testing.T) {
	db, err := gorm.Open(sqlite.Open("file::memory:?cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatal("failed to connect database")
	}

	config := Config{}
	storage, err := NewGORMStorage(db)
	if err != nil {
		t.Fatal("failed to setup gorm cache storage")
	}
	cache := &CacheFunk{
		Config:  config,
		Storage: storage,
	}

	runTestWrapWithStringResult(t, cache)
	cache.Clear()
	runTestWrapWithContextAndStringResult(t, cache)
	cache.Clear()
	runTestWrapWithIgnoreAndObjectResult(t, cache)
	cache.Clear()
	runTestWrapWithContextAndObjectResult(t, cache)
	cache.Clear()
	runTestWrapAndObjectResult(t, cache)
	cache.Clear()
	runTestCacheFuncErrorsReturned(t, cache)
	cache.Clear()
	runTestCacheFuncWithContextErrorsReturned(t, cache)
	cache.Clear()
	expireAllEntries := func() {
		db.Model(CacheEntry{}).Where("1=1").Update("timestamp", time.Now().UTC().Add(-3600*time.Second))
	}
	runTestCacheFuncTTL(t, cache, expireAllEntries)
	cache.Clear()
	runTestCachePoisoning(t, cache)
	cache.Clear()
	runTestCacheFallBackToExpired(t, cache, expireAllEntries)
	cache.Clear()
	runTestCacheFallBackToExpiredWithContext(t, cache, expireAllEntries)
	cache.Clear()
	runTestCacheMismatchCompressionType(t, cache, expireAllEntries)
	cache.Clear()
	runTestCleanupUnconfiguredKey(t, cache, expireAllEntries)
	cache.Clear()
	runTestCacheCompressedPayload(t, cache)
	cache.Clear()
	runTestSingleflightCoalesces(t, cache)
	cache.Clear()
	runTestSingleflightPanicIsReturnedAsError(t, cache)
	cache.Clear()
	runTestSingleflightDoneContext(t, cache)
	cache.Clear()
	runTestSingleflightCallerGivesUp(t, cache)
	cache.Clear()
	runTestSingleflightReentrantCall(t, cache)
	cache.Clear()
	runTestSingleflightNestedCalls(t, cache)
	cache.Clear()
	runTestCacheErrDoNotCache(t, cache)
	cache.Clear()
}

func ExampleGORMStorage() {
	type HelloWorldParams struct {
		Name string
	}

	helloWorld := func(ignoreCache bool, params *HelloWorldParams) (string, error) {
		return "Hello " + params.Name, nil
	}

	db, err := gorm.Open(sqlite.Open("file::memory:?cache=shared"), &gorm.Config{})
	if err != nil {
		panic("failed to connect database")
	}

	config := Config{}
	storage, err := NewGORMStorage(db)
	if err != nil {
		panic("failed to setup gorm cache storage")
	}
	cache := &CacheFunk{
		Config:  config,
		Storage: storage,
	}

	HelloWorld := WrapWithIgnore(cache, "hello", helloWorld)
	params := &HelloWorldParams{
		Name: "bob",
	}

	// First call will get value from wrapped function
	value, err := HelloWorld(false, params)
	fmt.Println("First call:", value, err)
	// Second call will get value from cache
	value, err = HelloWorld(false, params)
	fmt.Println("Second call:", value, err)
}
