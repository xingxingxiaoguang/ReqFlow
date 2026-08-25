// Package parser 实现 port.DocParser：按后缀分发的文档文本提取。
// txt/md 直读；docx 以标准库解析 OOXML（零三方依赖，段落保序）；
// pdf 走 MinerU 云端（表格转 Markdown、水印处理）；xlsx 行级解析能力
// 已就绪（ParseXLSXRows），第二波 bug Excel 导入在同一包内开放。
package parser

import (
	"archive/zip"
	"context"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"reqflow/internal/port"
)

// Options 解析参数（由 cmd 从配置注入）。
type Options struct {
	MaxFileMB int
	MinerU    MinerUOptions
}

// Parser 文档解析器。
type Parser struct {
	opt    Options
	mineru *MinerU
}

// New 构造解析器。
func New(opt Options) *Parser {
	return &Parser{opt: opt, mineru: NewMinerU(opt.MinerU)}
}

// ErrUnsupportedFormat 不支持的文件格式。
var ErrUnsupportedFormat = errors.New("不支持的文件格式（支持 txt/md/docx/pdf）")

// Parse 按文件名后缀分发解析，返回全文文本。
func (p *Parser) Parse(ctx context.Context, filename, filePath string, onProgress func(port.ParseProgress)) (string, error) {
	if p.opt.MaxFileMB > 0 {
		if st, err := os.Stat(filePath); err == nil && st.Size() > int64(p.opt.MaxFileMB)<<20 {
			return "", fmt.Errorf("文件超过大小限制 %dMB", p.opt.MaxFileMB)
		}
	}
	switch strings.ToLower(filepath.Ext(filename)) {
	case ".txt", ".md":
		data, err := os.ReadFile(filePath)
		if err != nil {
			return "", err
		}
		return string(data), nil
	case ".docx":
		return ParseDocx(filePath)
	case ".pdf":
		return p.mineru.ParsePDF(ctx, filename, filePath, onProgress)
	default:
		return "", ErrUnsupportedFormat
	}
}

/* ---- docx：zip 内 word/document.xml 的 <w:t> 文本提取（段落保序） ---- */

// ParseDocx 提取 docx 全文文本：逐 <w:p> 段落拼接 <w:t> 内容，段落以换行分隔。
// 复杂表格 docx 建议转 PDF 走 MinerU（表格结构更完整）。
func ParseDocx(path string) (string, error) {
	r, err := zip.OpenReader(path)
	if err != nil {
		return "", fmt.Errorf("打开 docx 失败: %w", err)
	}
	defer r.Close()

	var docFile *zip.File
	for _, f := range r.File {
		if f.Name == "word/document.xml" {
			docFile = f
			break
		}
	}
	if docFile == nil {
		return "", fmt.Errorf("docx 中未找到 word/document.xml")
	}
	rc, err := docFile.Open()
	if err != nil {
		return "", err
	}
	defer rc.Close()
	return extractDocxText(rc)
}

func extractDocxText(rd io.Reader) (string, error) {
	dec := xml.NewDecoder(rd)
	var lines []string
	var cur strings.Builder
	inParagraph := false

	for {
		tok, err := dec.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return "", fmt.Errorf("解析 docx XML 失败: %w", err)
		}
		switch t := tok.(type) {
		case xml.StartElement:
			switch t.Name.Local {
			case "p": // 段落开始（w:p）
				inParagraph = true
				cur.Reset()
			case "t": // 文本run（w:t）
				if inParagraph {
					var ch string
					if err := dec.DecodeElement(&ch, &t); err == nil {
						cur.WriteString(ch)
					}
				}
			case "br", "cr": // 段内换行
				if inParagraph {
					cur.WriteByte('\n')
				}
			}
		case xml.EndElement:
			if t.Name.Local == "p" {
				if s := strings.TrimSpace(cur.String()); s != "" {
					lines = append(lines, s)
				}
				inParagraph = false
			}
		}
	}
	return strings.Join(lines, "\n"), nil
}
