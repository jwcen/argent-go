// Package web 持有前端构建产物，并把它以 fs.FS 的形式暴露给 transport 层。
//
// 为什么单独开一个包？因为 go:embed 只能嵌入「指令所在包的目录及其子目录」，
// 不能用 ../ 向上跳。所以静态资源必须和一个 .go 文件同处一个目录树，
// 这个包的存在就是为了给 web/static/ 提供落脚点。
package web

import (
	"embed"
	"io/fs"
	"os"
)

// embedded 把整个 web/static/ 目录编进二进制。
//
// 三个容易踩的点：
//  1. //go:embed 是编译器指令注释，必须紧贴 var 声明，中间不能有空行，
//     否则它退化成一条普通注释，编译器不报错但 FS 是空的。
//  2. 必须 import "embed"，即使代码里没直接用到 embed 包的标识符
//     （这里用了 embed.FS 类型，所以是显式引用；若只嵌到 string/[]byte
//     则需要写成 _ "embed" 的空导入）。
//  3. all: 前缀不能省。默认模式会跳过 . 和 _ 开头的文件，
//     加上 all: 才是「目录下所有文件」的语义。
//
//go:embed all:static
var embedded embed.FS

// EmbeddedFS 返回已经剥掉 "static/" 前缀的文件系统。
//
// embed 会保留被嵌入目录本身这一层，即路径长成 "static/index.html"。
// 但 HTTP 层期望 "/" 对应 index.html、"/assets/x.js" 对应 assets/x.js，
// 所以要用 fs.Sub 把根下移一层，否则所有请求都会 404。
func EmbeddedFS() (fs.FS, error) {
	return fs.Sub(embedded, "static")
}

// DirFS 返回一个直接读磁盘的文件系统，供开发模式使用。
//
// 返回值同样是 fs.FS —— 这正是这套设计的关键：调用方拿到的是接口，
// 完全不知道背后是二进制里的字节还是磁盘上的文件。
// 换来源不需要改任何一行使用方代码。
func DirFS(dir string) fs.FS {
	return os.DirFS(dir)
}
