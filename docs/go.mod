module github.com/maypok86/otter/v2/docs

go 1.24.0

replace github.com/maypok86/otter/v2 => ../

require (
	github.com/jmoiron/sqlx v1.4.0
	github.com/maypok86/otter/v2 v2.0.0-00010101000000-000000000000
)

require golang.org/x/sys v0.38.0 // indirect
