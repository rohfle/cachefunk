module github.com/rohfle/cachefunk

go 1.26.0

require (
	golang.org/x/sync v0.24.0
	gorm.io/gorm v1.30.0
)

require golang.org/x/text v0.25.0 // indirect

require (
	github.com/jinzhu/inflection v1.0.0 // indirect
	github.com/jinzhu/now v1.1.5 // indirect
	github.com/klauspost/compress v1.18.0
	github.com/mattn/go-sqlite3 v1.14.16 // indirect
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
