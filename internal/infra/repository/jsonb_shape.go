package repository

import (
	"bytes"
	"encoding/json"
	"fmt"
)

// 本文件把 jsonb 列的形状约束从数据库 CHECK(jsonb_typeof(...)) 前移到写入边界：
// 非法形状在应用层报可归因的错误，而不是等 INSERT 撞约束后炸出 SQLSTATE 23514；
// Go 的 nil slice/map 会被 json.Marshal 序列化成 null，而约束下"缺失"的存储语义
// 是空数组/空对象（与各表列的 DEFAULT 一致），由 marshalJSONBArray 统一归一化。

func requireJSONBObject(column string, value json.RawMessage) error {
	trimmed := bytes.TrimSpace(value)
	if len(trimmed) == 0 || trimmed[0] != '{' || !json.Valid(trimmed) {
		return fmt.Errorf("%s 必须是 JSON object", column)
	}
	return nil
}

func marshalJSONBArray[T any](value []T) (json.RawMessage, error) {
	if len(value) == 0 {
		return json.RawMessage(`[]`), nil
	}
	return json.Marshal(value)
}
