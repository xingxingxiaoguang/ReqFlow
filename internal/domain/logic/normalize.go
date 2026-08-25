package logic

import (
	"regexp"
	"strings"
)

var whitespaceRe = regexp.MustCompile(`\s+`)

// NormalizeForExactMatch 精确匹配用文本归一化：全角→半角、连续空白压缩、转小写。
// 项目名/标题是「准标识符」（含版本号、工单号等稀有 token），这类匹配不能交给
// 语义向量——向量对稀有 token 不敏感，会吞掉 V2/V3 这类关键区分信号。
func NormalizeForExactMatch(text string) string {
	if text == "" {
		return ""
	}
	var b strings.Builder
	b.Grow(len(text))
	for _, r := range text {
		// 全角 ASCII（！-～，U+FF01–U+FF5E）转半角：码点减 0xFEE0
		if r >= 0xFF01 && r <= 0xFF5E {
			r -= 0xFEE0
		}
		// 全角空格转普通空格（Go 的 \s 不含 U+3000，需显式处理）
		if r == 0x3000 {
			r = ' '
		}
		b.WriteRune(r)
	}
	return strings.ToLower(strings.TrimSpace(whitespaceRe.ReplaceAllString(b.String(), " ")))
}
