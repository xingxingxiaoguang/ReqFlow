package logic

import (
	"fmt"
	"math/rand"
	"strings"
	"time"
)

// GenerateProjectIdentifier 生成平台内唯一的项目标识（大写字母/数字/下划线/连字符，≤15 字符）。
// 中文或空 → PRJ + 时间与随机后缀；英文基名 → 截断后加随机后缀。
func GenerateProjectIdentifier(base string) string {
	var b strings.Builder
	for _, r := range base {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_':
			b.WriteRune(r)
		}
	}
	clean := strings.ToUpper(strings.Trim(b.String(), "-_"))
	rnd := rand.New(rand.NewSource(time.Now().UnixNano()))
	suffix := fmt.Sprintf("%04d", rnd.Intn(10000))
	if clean == "" {
		// 时间戳 base36 取尾 + 随机，保证低碰撞
		ts := fmt.Sprintf("%x", time.Now().UnixMilli())
		if len(ts) > 4 {
			ts = ts[len(ts)-4:]
		}
		return fmt.Sprintf("PRJ%s%s", strings.ToUpper(ts), suffix)
	}
	if len(clean) > 10 {
		clean = clean[:10]
	}
	return clean + "_" + suffix
}
