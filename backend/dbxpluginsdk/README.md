# DBX Plugin SDK (Go)

本目录是 [t8y2/dbx](https://github.com/t8y2/dbx) 的 Go 插件 SDK，源代码取自
`plugins/sdk/go/dbx-plugin-sdk`，Apache-2.0 许可。

固定来源版本：`v0.0.0-20261113071908-65efe53beb56`。

主模块通过 `replace` 指向本目录，SDK 不依赖外部 module；插件自身的第三方依赖
另由 `backend/vendor` 固定，因此 `GOPROXY=off` 下仍可复现构建。

同步上游 SDK 后必须重新运行 `go test ./...` 和 `cmd/dbxpoke` 协议握手。
