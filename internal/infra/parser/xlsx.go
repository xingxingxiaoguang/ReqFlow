package parser

import (
	"fmt"
	"strings"

	"github.com/xuri/excelize/v2"
)

// ParseXLSX 提取 Excel 文本表示（首个工作表，行以 " | " 连接）。
// 第一波未对前端开放；第二波 bug 批量导入的结构化入口见 ParseXLSXRows。
func ParseXLSX(path string) (string, error) {
	rows, headers, err := ParseXLSXRows(path)
	if err != nil {
		return "", err
	}
	var b strings.Builder
	b.WriteString(strings.Join(headers, " | "))
	b.WriteByte('\n')
	for _, row := range rows {
		vals := make([]string, 0, len(headers))
		for _, h := range headers {
			vals = append(vals, row[h])
		}
		b.WriteString(strings.Join(vals, " | "))
		b.WriteByte('\n')
	}
	return b.String(), nil
}

// ParseXLSXRows 解析首个工作表为「表头 → 值」的行映射列表。
// 首行为表头（去空白；空表头列以 列N 命名），数据行全空则跳过。
func ParseXLSXRows(path string) (rows []map[string]string, headers []string, err error) {
	f, err := excelize.OpenFile(path)
	if err != nil {
		return nil, nil, fmt.Errorf("打开 Excel 失败: %w", err)
	}
	defer f.Close()

	sheets := f.GetSheetList()
	if len(sheets) == 0 {
		return nil, nil, fmt.Errorf("Excel 中没有工作表")
	}
	grid, err := f.GetRows(sheets[0])
	if err != nil {
		return nil, nil, fmt.Errorf("读取 Excel 失败: %w", err)
	}
	if len(grid) == 0 {
		return nil, nil, fmt.Errorf("Excel 首个工作表为空")
	}

	headers = make([]string, 0, len(grid[0]))
	seen := map[string]int{}
	for i, h := range grid[0] {
		name := strings.TrimSpace(h)
		if name == "" {
			name = fmt.Sprintf("列%d", i+1)
		}
		if n := seen[name]; n > 0 { // 重名表头去重
			seen[name] = n + 1
			name = fmt.Sprintf("%s_%d", name, n+1)
		} else {
			seen[name] = 1
		}
		headers = append(headers, name)
	}

	for _, raw := range grid[1:] {
		row := map[string]string{}
		nonEmpty := false
		for i, h := range headers {
			v := ""
			if i < len(raw) {
				v = strings.TrimSpace(raw[i])
			}
			if v != "" {
				nonEmpty = true
			}
			row[h] = v
		}
		if nonEmpty {
			rows = append(rows, row)
		}
	}
	return rows, headers, nil
}
