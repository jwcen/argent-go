# argent-go

个人投资组合管理系统的 Go 实现。

## 运行

```bash
go build -o bin/argent ./cmd/argent
./bin/argent
```

默认监听 `:8889`，浏览器打开 <http://127.0.0.1:8889> 即可 —— 前端已通过 `go:embed`
打进二进制，无需额外部署静态文件。

## 环境变量

| 变量 | 默认值 | 说明 |
|---|---|---|
| `ARGENT_PORT` | `8889` | 监听端口（原 Python 版用 8888，可并行对拍） |
| `ARGENT_ENV` | `dev` | `production` 时切 JSON 日志 + gin release 模式 |
| `ARGENT_DATA_DIR` | `./data` | 数据根目录。全局库 `portfolio.db`，用户库 `users/u{id}.db` |
| `ARGENT_LEGACY_DB` | — | 首个用户（id=1）继承的旧单用户库路径 |
| `ARGENT_STATIC_DIR` | — | 设置后前端改读磁盘目录（开发用，改文件即时生效）；不设则用内嵌资源 |
| `ARGENT_LOG_LEVEL` | `debug` | `debug` / `info` / `warn` / `error` |
| `ARGENT_LOG_FORMAT` | `text` | `text` / `json` |
| `ARGENT_LOG_OUTPUT` | stdout | 日志文件路径 |

从原版迁移：把原 `portfolio.db` 复制为 `<ARGENT_DATA_DIR>/portfolio.db` 即可，
迁移脚本全部使用 `IF NOT EXISTS`，可以直接跑在旧库上，原账号密码与会话 cookie 均兼容。

## 前端

`web/static/` 存放构建产物（已入库，保证 clone 下来即可运行）。
前端源码目前仍在原仓库，需要改前端时：

1. 在原仓库把 vite 的 proxy target 从 `8888` 改为 `8889`
2. `npm run dev`，或构建后把产物拷回 `web/static/`

也可以 `ARGENT_STATIC_DIR=/path/to/static` 直接指向别处的产物目录调试。

## 开发

```bash
go test ./...
go vet ./...
gofmt -l .
```

Go 1.24 环境下需带 `GOTOOLCHAIN=local`，避免依赖触发工具链自动升级。
