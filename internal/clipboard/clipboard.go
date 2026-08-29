// Package clipboard 剪贴板监听：轮询 + 内容哈希对比 + 去抖 + 过滤
// 仅读取剪贴板、不写回，天然防循环。
package clipboard

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"time"

	"github.com/atotto/clipboard"
)

// Watch 轮询剪贴板；内容（过滤后）变化时回调 onChange。
// ctx 取消后退出；剪贴板被占用时跳过该次轮询。
func Watch(ctx context.Context, onChange func(text string)) {
	interval := 500 * time.Millisecond
	tick := time.NewTicker(interval)
	defer tick.Stop()

	lastHash := ""
	var lastChange time.Time
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
			text, err := clipboard.ReadAll()
			if err != nil {
				continue // 剪贴板被其他程序占用，跳过
			}
			text = strings.TrimSpace(text)
			if !acceptable(text) {
				continue
			}
			h := hashOf(text)
			if h == lastHash {
				continue
			}
			lastHash = h
			// 去抖：500ms 内只处理最后一次变化
			if time.Since(lastChange) < 500*time.Millisecond {
				continue
			}
			lastChange = time.Now()
			onChange(text)
		}
	}
}

// acceptable 过滤：跳过纯数字、URL、过短(<2)与过长(>2000)文本
func acceptable(s string) bool {
	if len([]rune(s)) < 2 || len([]rune(s)) > 2000 {
		return false
	}
	if isAllDigits(s) || isURL(s) {
		return false
	}
	return true
}

func hashOf(s string) string {
	h := sha256.Sum256([]byte(s))
	return hex.EncodeToString(h[:])
}

func isAllDigits(s string) bool {
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

func isURL(s string) bool {
	lower := strings.ToLower(s)
	return strings.HasPrefix(lower, "http://") || strings.HasPrefix(lower, "https://") || strings.HasPrefix(lower, "www.")
}
