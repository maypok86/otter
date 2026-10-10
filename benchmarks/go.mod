module github.com/maypok86/otter/v2/benchmarks

go 1.26.0

replace github.com/maypok86/otter/v2 => ../

require (
	github.com/BurntSushi/toml v1.5.0
	github.com/Yiling-J/theine-go v0.6.2
	github.com/bluele/gcache v0.0.2
	github.com/dgraph-io/ristretto/v2 v2.4.2
	github.com/dgryski/go-clockpro v0.0.0-20140817124034-edc6d3eeb96e
	github.com/go-analyze/charts v0.6.1
	github.com/hashicorp/golang-lru/arc/v2 v2.0.7
	github.com/hashicorp/golang-lru/v2 v2.0.7
	github.com/jellydator/ttlcache/v3 v3.4.1
	github.com/karlseguin/ccache/v3 v3.0.8
	github.com/klauspost/compress v1.18.0
	github.com/maypok86/otter/v2 v2.0.0-00010101000000-000000000000
	github.com/olekukonko/tablewriter v0.0.5
	github.com/pingcap/go-ycsb v1.0.1
	github.com/scalalang2/golang-fifo/v2 v2.0.0-20231212012136-274aca942e14
	github.com/ulikunitz/xz v0.5.12
	github.com/viccon/sturdyc v1.1.6
	golang.org/x/perf v0.0.0-20261009192801-be2c69fb417e
	golang.org/x/sync v0.16.0
)

require (
	github.com/aclements/go-moremath v0.0.0-20210112150236-f10218a38794 // indirect
	github.com/cespare/xxhash/v2 v2.3.0 // indirect
	github.com/dustin/go-humanize v1.0.1 // indirect
	github.com/go-analyze/bulk v0.1.5 // indirect
	github.com/golang/freetype v0.0.0-20170609003504-e2365dfdc4a0 // indirect
	github.com/klauspost/cpuid/v2 v2.0.9 // indirect
	github.com/magiconair/properties v1.8.0 // indirect
	github.com/mattn/go-runewidth v0.0.16 // indirect
	github.com/pingcap/errors v0.11.5-0.20211224045212-9687c2b0f87c // indirect
	github.com/rivo/uniseg v0.2.0 // indirect
	github.com/stretchr/testify v1.12.1 // indirect
	github.com/zeebo/xxh3 v1.0.2 // indirect
	go.uber.org/atomic v1.9.0 // indirect
	go.yaml.in/yaml/v3 v3.0.5 // indirect
	golang.org/x/image v0.47.0 // indirect
	golang.org/x/sys v0.49.0 // indirect
)

tool golang.org/x/perf/cmd/benchstat
