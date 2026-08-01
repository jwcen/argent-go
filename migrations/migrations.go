// Package migrations 把 SQL 迁移脚本用 go:embed 打包进二进制。
//
// 为什么要 embed：部署时只需要拷贝一个可执行文件，不必再同步一个 SQL 目录，
// 也就不存在「二进制和迁移脚本版本对不上」的经典事故。这是 Go 相比
// Python 原版（运行时 executescript 一段硬编码字符串）的一个实质改进。
//
// 目录约定：
//
//	migrations/global/NNNN_name.sql  -> 全局库（users / auth_sessions / ...）
//	migrations/user/NNNN_name.sql    -> 每个用户库
//
// 文件名前 4 位数字即版本号，必须递增且唯一。
package migrations

import "embed"

// Global 是全局库的迁移集合。
//
//go:embed global/*.sql
var Global embed.FS

// User 是用户库的迁移集合。
//
//go:embed user/*.sql
var User embed.FS

// 子目录名，供迁移执行器读取时拼路径。
const (
	GlobalDir = "global"
	UserDir   = "user"
)
