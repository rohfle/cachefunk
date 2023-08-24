module github.com/rohfle/cachefunk

go 1.20

require gorm.io/gorm v1.24.5

require (
	github.com/jinzhu/inflection v1.0.0 // indirect
	github.com/jinzhu/now v1.1.5 // indirect
	github.com/mattn/go-sqlite3 v1.14.16 // indirect
	gorm.io/driver/sqlite v1.4.4
)

// ignore versions while I was figuring out go.pkg.dev
retract (
	v0.0.1
	v0.1.0
	v0.2.0
	v0.3.0
	v0.3.1
)
