package api

// SplitSegments 将长文本按自然句界切分为若干片段，单段不超过 maxRunes 字符。
// 句界优先于硬切：从段尾往前找最近的句号/换行等边界，避免切断语义；
// 找不到边界时才按长度硬切。用于超长文本分段翻译（每段独立入上下文，滚动前文保持连续）。
func SplitSegments(text string, maxRunes int) []string {
	if maxRunes <= 0 {
		maxRunes = 3000
	}
	runes := []rune(text)
	if len(runes) <= maxRunes {
		return []string{text}
	}
	var segs []string
	start := 0
	for start < len(runes) {
		end := start + maxRunes
		if end >= len(runes) {
			segs = append(segs, string(runes[start:]))
			break
		}
		cut := end
		for i := end; i > start; i-- {
			if isSegmentBoundary(runes[i-1]) {
				cut = i
				break
			}
		}
		if cut <= start {
			cut = end // 该段无句界，硬切
		}
		segs = append(segs, string(runes[start:cut]))
		start = cut
	}
	return segs
}

// isSegmentBoundary 是否作为片段边界（中文/英文句末与换行）
func isSegmentBoundary(r rune) bool {
	switch r {
	case '。', '！', '？', '；', '\n', '.', '!', '?', ';':
		return true
	}
	return false
}
