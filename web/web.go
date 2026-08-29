// Package web 内嵌 Web 管理界面资源
package web

import "embed"

//nolint:all // 内嵌静态资源
//
//go:embed index.html
var fs embed.FS

// IndexHTML 返回管理界面首页
func IndexHTML() ([]byte, error) {
	return fs.ReadFile("index.html")
}