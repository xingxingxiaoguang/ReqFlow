package model

import "testing"

func TestTaskItemValues(t *testing.T) {
	it := TaskItem{Fields: `{"title":"实现登录","priority":"High"}`}
	v := it.Values()
	if v["title"] != "实现登录" || v["priority"] != "High" {
		t.Fatalf("Values = %+v", v)
	}
	// 非法 JSON / 空 Fields → 空 map（读侧不因坏数据 panic）
	if got := (TaskItem{Fields: `{bad`}).Values(); len(got) != 0 {
		t.Fatalf("非法 JSON 应返回空 map: %+v", got)
	}
	if got := (TaskItem{}).Values(); len(got) != 0 {
		t.Fatalf("空 Fields 应返回空 map: %+v", got)
	}
}
