// Package repository 以 GORM + 原生 SQL 实现 port 仓储契约。
// 本包只依赖 port/domain 与注入的 *gorm.DB，不感知业务用例与 HTTP。
package repository

import (
	"time"
)

/* ---- 公共构造 ---- */

// 全部依赖经构造函数注入（*gorm.DB），无全局状态。

func strPtr(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

func strVal(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

func timePtr(t time.Time) *time.Time {
	if t.IsZero() {
		return nil
	}
	return &t
}

func timeVal(p *time.Time) time.Time {
	if p == nil {
		return time.Time{}
	}
	return *p
}

func jsonOrEmpty(raw []byte) []byte {
	if len(raw) == 0 {
		return []byte(`{}`)
	}
	return raw
}
