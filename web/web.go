// Package web 内嵌 Web 管理界面资源
package web

import "embed"

//nolint:all // 内嵌静态资源
//
//go:embed index.html favicon-16.png favicon-32.png
var fs embed.FS

// IndexHTML 返回管理界面首页
func IndexHTML() ([]byte, error) {
	return fs.ReadFile("index.html")
}

// Favicon 返回指定尺寸的站点图标（favicon-16.png / favicon-32.png）
func Favicon(name string) ([]byte, bool) {
	if name != "favicon-16.png" && name != "favicon-32.png" {
		return nil, false
	}
	b, err := fs.ReadFile(name)
	if err != nil {
		return nil, false
	}
	return b, true
}