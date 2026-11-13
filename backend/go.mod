module github.com/SmallRob/dbx-sonar-quality

go 1.23.0

require (
	github.com/OneOfOne/xxhash v1.2.8
	github.com/jackc/pgx/v5 v5.7.6
	github.com/pierrec/lz4/v4 v4.1.22
	github.com/t8y2/dbx/plugins/sdk/go/dbx-plugin-sdk v0.0.0-20261113071908-65efe53beb56
)

// SDK 随仓库固定，构建可在 GOPROXY=off 下复现。
replace github.com/t8y2/dbx/plugins/sdk/go/dbx-plugin-sdk => ./dbxpluginsdk

require (
	github.com/jackc/pgpassfile v1.0.0 // indirect
	github.com/jackc/pgservicefile v0.0.0-20240606120523-5a60cdf6a761 // indirect
	github.com/jackc/puddle/v2 v2.2.2 // indirect
	golang.org/x/crypto v0.37.0 // indirect
	golang.org/x/sync v0.13.0 // indirect
	golang.org/x/text v0.24.0 // indirect
)
