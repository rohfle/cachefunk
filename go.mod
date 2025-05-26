module github.com/rohfle/cachefunk

go 1.22

toolchain go1.23.5

require gorm.io/gorm v1.24.5

require github.com/vmihailenco/tagparser/v2 v2.0.0 // indirect

require (
	github.com/andybalholm/brotli v1.1.1
	github.com/jinzhu/inflection v1.0.0 // indirect
	github.com/jinzhu/now v1.1.5 // indirect
	github.com/klauspost/compress v1.18.0
	github.com/mattn/go-sqlite3 v1.14.16 // indirect
	github.com/vmihailenco/msgpack/v5 v5.4.1
	gorm.io/driver/sqlite v1.4.4
)

// ignore versions while I was figuring out go.pkg.dev
retract (
	v0.3.1
	v0.3.0
	v0.2.0
	v0.1.0
	v0.0.1
)
