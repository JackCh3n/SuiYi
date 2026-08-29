package api

import "strings"

// Lang 语言项
type Lang struct {
	Code string `json:"code"` // 语言代码
	Zh   string `json:"zh"`   // 中文全称
	En   string `json:"en"`   // 英文全称
}

// Languages 支持的语言列表（Hy-MT2 33+ 语言，含全称用于 prompt）
var Languages = []Lang{
	{"zh", "中文", "Chinese"},
	{"zh-Hant", "繁体中文", "Traditional Chinese"},
	{"en", "英语", "English"},
	{"fr", "法语", "French"},
	{"pt", "葡萄牙语", "Portuguese"},
	{"es", "西班牙语", "Spanish"},
	{"ja", "日语", "Japanese"},
	{"tr", "土耳其语", "Turkish"},
	{"ru", "俄语", "Russian"},
	{"ar", "阿拉伯语", "Arabic"},
	{"ko", "韩语", "Korean"},
	{"th", "泰语", "Thai"},
	{"it", "意大利语", "Italian"},
	{"de", "德语", "German"},
	{"vi", "越南语", "Vietnamese"},
	{"ms", "马来语", "Malay"},
	{"id", "印尼语", "Indonesian"},
	{"tl", "菲律宾语", "Filipino"},
	{"hi", "印地语", "Hindi"},
	{"pl", "波兰语", "Polish"},
	{"cs", "捷克语", "Czech"},
	{"nl", "荷兰语", "Dutch"},
	{"km", "高棉语", "Khmer"},
	{"my", "缅甸语", "Burmese"},
	{"fa", "波斯语", "Persian"},
	{"gu", "古吉拉特语", "Gujarati"},
	{"ur", "乌尔都语", "Urdu"},
	{"te", "泰卢固语", "Telugu"},
	{"mr", "马拉地语", "Marathi"},
	{"he", "希伯来语", "Hebrew"},
	{"bn", "孟加拉语", "Bengali"},
	{"ta", "泰米尔语", "Tamil"},
	{"uk", "乌克兰语", "Ukrainian"},
	{"bo", "藏语", "Tibetan"},
	{"kk", "哈萨克语", "Kazakh"},
	{"mn", "蒙古语", "Mongolian"},
	{"ug", "维吾尔语", "Uyghur"},
	{"yue", "粤语", "Cantonese"},
}

// langName 返回语言代码对应的中文/英文全称；未知代码返回原样
func langName(code string, zh bool) string {
	for _, l := range Languages {
		if l.Code == code {
			if zh {
				return l.Zh
			}
			return l.En
		}
	}
	return code
}

// BuildPrompt 依据官方模板构造翻译提示词
// 使用中文模板（全称中文），与 README 中文指令一致
func BuildPrompt(text, source, target, glossary, style string) string {
	targetName := langName(target, true)
	var sb strings.Builder

	if glossary != "" {
		sb.WriteString("*参考下面的翻译：*\n")
		for _, line := range strings.Split(glossary, "\n") {
			line = strings.TrimSpace(line)
			if line == "" {
				continue
			}
			sb.WriteString(line)
			sb.WriteString("\n")
		}
		sb.WriteString("\n")
	}

	if style != "" {
		sb.WriteString("请将以下文本翻译为 " + targetName + "。\n")
		sb.WriteString("注意翻译的风格要严格符合【" + style + "】\n\n")
		sb.WriteString(text)
		return sb.String()
	}

	if sb.Len() > 0 {
		sb.WriteString("将以下文本翻译为 " + targetName + "，注意只需要输出翻译后的结果，不要额外解释：\n\n")
		sb.WriteString(text)
		return sb.String()
	}
	return "将以下文本翻译为 " + targetName + "，注意只需要输出翻译后的结果，不要额外解释：\n\n" + text
}

// buildChatPrompt 官方英文模板（保留备用）
func buildChatPrompt(text string, target string) string {
	return "Translate the following text into " + langName(target, false) + ". Note that you should only output the translated result without any additional explanation:\n\n" + text
}