package cachefunk

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
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

// TestGORMStorageIndexes checks the indexes the storage depends on: the unique
// (key, params_hash) for lookup and upsert, and (key, timestamp) for cleanup and
// expired counting. Both must appear when an older table is migrated.
func TestGORMStorageIndexes(t *testing.T) {
	db, err := gorm.Open(sqlite.Open("file:indexes_test?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatal("failed to connect database")
	}
	if _, err := NewGORMStorage(db); err != nil {
		t.Fatalf("error while setting up gorm cache storage: %s", err)
	}

	indexes := []string{"idx_key_params_hash", "idx_key_timestamp"}
	for _, name := range indexes {
		if !db.Migrator().HasIndex(&CacheEntry{}, name) {
			t.Errorf("expected index %q to exist after migration", name)
		}
	}

	// the unique index has to lead with key, or a lookup that constrains key
	// cannot use it
	var columns []string
	rows, err := db.Raw("PRAGMA index_info(idx_key_params_hash)").Rows()
	if err != nil {
		t.Fatalf("error while reading the index columns: %s", err)
	}
	defer rows.Close()
	for rows.Next() {
		var seqno, cid int
		var name string
		if err := rows.Scan(&seqno, &cid, &name); err != nil {
			t.Fatal(err)
		}
		columns = append(columns, name)
	}
	if joined := strings.Join(columns, ","); joined != "key,params_hash" {
		t.Errorf("expected the unique index over (key, params_hash) but it is over (%s)", joined)
	}

	// a table written before an index existed gains it on the next migration
	for _, name := range indexes {
		if err := db.Migrator().DropIndex(&CacheEntry{}, name); err != nil {
			t.Fatalf("error while dropping %s: %s", name, err)
		}
	}
	if _, err := NewGORMStorage(db); err != nil {
		t.Fatalf("error while migrating again: %s", err)
	}
	for _, name := range indexes {
		if !db.Migrator().HasIndex(&CacheEntry{}, name) {
			t.Errorf("expected the migration to recreate %s on an existing table", name)
		}
	}
}

// TestGORMStorageParamsHash checks that params longer than a btree index entry
// can hold are cached, and that the indexed column holds a sha256 of the params
// the codec serialised.
func TestGORMStorageParamsHash(t *testing.T) {
	db, err := gorm.Open(sqlite.Open("file:paramshash_test?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatal("failed to connect database")
	}
	storage, err := NewGORMStorage(db)
	if err != nil {
		t.Fatalf("error while setting up gorm cache storage: %s", err)
	}
	cache := &CacheFunk{Config: Config{}, Storage: storage}

	buf := make([]byte, 4096)
	if _, err := rand.Read(buf); err != nil {
		t.Fatal(err)
	}
	long := hex.EncodeToString(buf) // 8 KB of incompressible params

	calls := 0
	Fetch := Wrap(cache, "longParams", func(params string) (string, error) {
		calls++
		return "value for long params", nil
	})

	if value, err := Fetch(long); err != nil || value != "value for long params" {
		t.Fatalf("expected long params to be cached but got %q, %v", value, err)
	}
	if value, err := Fetch(long); err != nil || value != "value for long params" || calls != 1 {
		t.Errorf("expected a cache hit for long params but got %q after %d resolver calls (%v)", value, calls, err)
	}

	var stored []byte
	if err := db.Raw("SELECT params_hash FROM cache_entries WHERE key = ?", "longParams").Row().Scan(&stored); err != nil {
		t.Fatalf("error while reading params_hash: %s", err)
	}
	want := sha256.Sum256([]byte(`"` + long + `"`)) // params as the codec serialised them
	if len(stored) != sha256.Size || !bytes.Equal(stored, want[:]) {
		t.Errorf("expected params_hash to be %d bytes of sha256(params) but got %d bytes", sha256.Size, len(stored))
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

	storage.Get(context.Background(), "key", DefaultKeyConfig, "params", expireBeforeTime)
	storage.Cleanup(context.Background(), "key", DefaultKeyConfig, expireBeforeTime)
	storage.ExpiredEntryCount(context.Background(), "key", DefaultKeyConfig, expireBeforeTime)

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
	cache.Clear(context.Background())
	runTestWrapWithContextAndStringResult(t, cache)
	cache.Clear(context.Background())
	runTestWrapWithIgnoreAndObjectResult(t, cache)
	cache.Clear(context.Background())
	runTestWrapWithContextAndObjectResult(t, cache)
	cache.Clear(context.Background())
	runTestWrapAndObjectResult(t, cache)
	cache.Clear(context.Background())
	runTestCacheFuncErrorsReturned(t, cache)
	cache.Clear(context.Background())
	runTestCacheFuncWithContextErrorsReturned(t, cache)
	cache.Clear(context.Background())
	expireAllEntries := func() {
		db.Model(CacheEntry{}).Where("1=1").Update("timestamp", time.Now().UTC().Add(-3600*time.Second))
	}
	runTestCacheFuncTTL(t, cache, expireAllEntries)
	cache.Clear(context.Background())
	runTestCachePoisoning(t, cache)
	cache.Clear(context.Background())
	runTestCacheFallBackToExpired(t, cache, expireAllEntries)
	cache.Clear(context.Background())
	runTestCacheFallBackToExpiredWithContext(t, cache, expireAllEntries)
	cache.Clear(context.Background())
	runTestCacheMismatchCompressionType(t, cache, expireAllEntries)
	cache.Clear(context.Background())
	runTestCleanupUnconfiguredKey(t, cache, expireAllEntries)
	cache.Clear(context.Background())
	runTestCacheCompressedPayload(t, cache)
	cache.Clear(context.Background())
	runTestSingleflightCoalesces(t, cache)
	cache.Clear(context.Background())
	runTestSingleflightPanicIsReturnedAsError(t, cache)
	cache.Clear(context.Background())
	runTestSingleflightDoneContext(t, cache)
	cache.Clear(context.Background())
	runTestSingleflightCallerGivesUp(t, cache)
	cache.Clear(context.Background())
	runTestSingleflightReentrantCall(t, cache)
	cache.Clear(context.Background())
	runTestSingleflightNestedCalls(t, cache)
	cache.Clear(context.Background())
	runTestCacheErrDoNotCache(t, cache)
	cache.Clear(context.Background())
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
