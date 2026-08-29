// Package parser 实现与存储位置无关的结构化文档解析。
package parser

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"reqflow/internal/domain/model"
	"reqflow/internal/port"
)

const (
	Name    = "reqflow.structured"
	Version = "1"
)

type Options struct {
	MaxFileMB int
	MinerU    MinerUOptions
}

type Parser struct {
	opt    Options
	mineru *MinerU
}

func New(opt Options) *Parser {
	return &Parser{opt: opt, mineru: NewMinerU(opt.MinerU)}
}

func (*Parser) ParserName() string    { return Name }
func (*Parser) ParserVersion() string { return Version }

var ErrUnsupportedFormat = errors.New("不支持的文件格式（支持 txt/md/docx/pdf）")

// Parse 接受 Reader 而不是本地路径。BlobStore 因而可以独立替换，Parser 也不能
// 绕过 Asset 边界读取任意主机文件。
func (p *Parser) Parse(ctx context.Context, source port.ParseSource, onProgress func(port.ParseProgress)) ([]model.DocumentBlock, error) {
	if source.Content == nil {
		return nil, fmt.Errorf("解析内容不能为空")
	}
	limit := int64(p.opt.MaxFileMB) << 20
	if limit <= 0 {
		limit = 50 << 20
	}
	if source.SizeBytes > limit {
		return nil, fmt.Errorf("文件超过大小限制 %dMB", limit>>20)
	}
	data, err := io.ReadAll(io.LimitReader(source.Content, limit+1))
	if err != nil {
		return nil, fmt.Errorf("读取解析内容: %w", err)
	}
	if int64(len(data)) > limit {
		return nil, fmt.Errorf("文件超过大小限制 %dMB", limit>>20)
	}

	var blocks []model.DocumentBlock
	switch strings.ToLower(filepath.Ext(source.Filename)) {
	case ".txt":
		blocks = parsePlainText(data)
	case ".md", ".markdown":
		blocks = parseMarkdown(data)
	case ".docx":
		blocks, err = parseDocx(data)
	case ".pdf":
		var markdown string
		markdown, err = p.mineru.ParsePDF(ctx, source.Filename, data, onProgress)
		if err == nil {
			blocks = parseMarkdown([]byte(markdown))
		}
	default:
		return nil, ErrUnsupportedFormat
	}
	if err != nil {
		return nil, err
	}
	if len(blocks) == 0 {
		return nil, fmt.Errorf("解析结果不包含可用文本区块")
	}
	for i := range blocks {
		blocks[i].Ordinal = i
		if strings.TrimSpace(blocks[i].Metadata) == "" {
			blocks[i].Metadata = "{}"
		}
	}
	return blocks, nil
}

func parsePlainText(data []byte) []model.DocumentBlock {
	paragraphs := regexp.MustCompile(`\r?\n\s*\r?\n`).Split(string(data), -1)
	blocks := make([]model.DocumentBlock, 0, len(paragraphs))
	for _, paragraph := range paragraphs {
		if text := strings.TrimSpace(strings.ReplaceAll(paragraph, "\r\n", "\n")); text != "" {
			blocks = append(blocks, model.DocumentBlock{BlockType: model.BlockParagraph, Text: text})
		}
	}
	return blocks
}

var (
	headingPattern = regexp.MustCompile(`^(#{1,6})\s+(.+?)\s*#*\s*$`)
	listPattern    = regexp.MustCompile(`^\s*(?:[-+*]|\d+[.)])\s+`)
	pagePattern    = regexp.MustCompile(`(?i)^\s*(?:<!--\s*page(?:number)?\s*[:=]\s*"?(\d+)"?.*-->|[-=]{2,}\s*page\s+(\d+)\s*[-=]{2,})\s*$`)
)

func parseMarkdown(data []byte) []model.DocumentBlock {
	lines := strings.Split(strings.ReplaceAll(string(data), "\r\n", "\n"), "\n")
	blocks := make([]model.DocumentBlock, 0, len(lines)/2+1)
	section := []string{}
	pageNo := 0
	for i := 0; i < len(lines); {
		line := strings.TrimSpace(lines[i])
		if line == "" {
			i++
			continue
		}
		if match := pagePattern.FindStringSubmatch(line); match != nil {
			value := match[1]
			if value == "" {
				value = match[2]
			}
			pageNo, _ = strconv.Atoi(value)
			i++
			continue
		}
		if match := headingPattern.FindStringSubmatch(line); match != nil {
			level, title := len(match[1]), strings.TrimSpace(match[2])
			if len(section) >= level {
				section = section[:level-1]
			}
			for len(section) < level-1 {
				section = append(section, "")
			}
			section = append(section, title)
			blocks = append(blocks, block(model.BlockHeading, pageNo, section, title,
				map[string]any{"level": level}))
			i++
			continue
		}
		if listPattern.MatchString(lines[i]) {
			start := i
			for i < len(lines) && listPattern.MatchString(lines[i]) {
				i++
			}
			blocks = append(blocks, block(model.BlockList, pageNo, section,
				strings.TrimSpace(strings.Join(lines[start:i], "\n")), nil))
			continue
		}
		if looksLikeTableRow(line) {
			start := i
			for i < len(lines) && looksLikeTableRow(strings.TrimSpace(lines[i])) {
				i++
			}
			blocks = append(blocks, block(model.BlockTable, pageNo, section,
				strings.TrimSpace(strings.Join(lines[start:i], "\n")), map[string]any{"format": "markdown"}))
			continue
		}
		start := i
		for i < len(lines) {
			candidate := strings.TrimSpace(lines[i])
			if candidate == "" || headingPattern.MatchString(candidate) || listPattern.MatchString(lines[i]) ||
				looksLikeTableRow(candidate) || pagePattern.MatchString(candidate) {
				break
			}
			i++
		}
		if i == start {
			i++
		}
		text := strings.TrimSpace(strings.Join(lines[start:i], "\n"))
		if text != "" {
			blocks = append(blocks, block(model.BlockParagraph, pageNo, section, text, nil))
		}
	}
	return blocks
}

func looksLikeTableRow(line string) bool {
	return strings.Count(line, "|") >= 2
}

func parseDocx(data []byte) ([]model.DocumentBlock, error) {
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return nil, fmt.Errorf("打开 docx 失败: %w", err)
	}
	var document *zip.File
	for _, file := range zr.File {
		if file.Name == "word/document.xml" {
			document = file
			break
		}
	}
	if document == nil {
		return nil, fmt.Errorf("docx 中未找到 word/document.xml")
	}
	reader, err := document.Open()
	if err != nil {
		return nil, err
	}
	defer reader.Close()
	return decodeDocxBlocks(reader)
}

func decodeDocxBlocks(reader io.Reader) ([]model.DocumentBlock, error) {
	decoder := xml.NewDecoder(reader)
	var blocks []model.DocumentBlock
	var current strings.Builder
	var style string
	var section []string
	inParagraph := false
	tableDepth := 0
	tableIndex := -1
	rowIndex := -1
	cellIndex := -1
	pageNo := 1
	paragraphPage := pageNo
	for {
		token, err := decoder.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("解析 docx XML 失败: %w", err)
		}
		switch value := token.(type) {
		case xml.StartElement:
			switch value.Name.Local {
			case "tbl":
				tableDepth++
				if tableDepth == 1 {
					tableIndex++
					rowIndex, cellIndex = -1, -1
				}
			case "tr":
				if tableDepth > 0 {
					rowIndex++
					cellIndex = -1
				}
			case "tc":
				if tableDepth > 0 {
					cellIndex++
				}
			case "p":
				inParagraph, style, paragraphPage = true, "", pageNo
				current.Reset()
			case "pStyle":
				if inParagraph {
					style = xmlAttr(value, "val")
				}
			case "t":
				if inParagraph {
					var text string
					if err := decoder.DecodeElement(&text, &value); err != nil {
						return nil, err
					}
					current.WriteString(text)
				}
			case "tab":
				if inParagraph {
					current.WriteByte('\t')
				}
			case "br", "lastRenderedPageBreak":
				if value.Name.Local == "lastRenderedPageBreak" || xmlAttr(value, "type") == "page" {
					pageNo++
				} else if inParagraph {
					current.WriteByte('\n')
				}
			}
		case xml.EndElement:
			switch value.Name.Local {
			case "tbl":
				if tableDepth > 0 {
					tableDepth--
				}
			case "p":
				text := strings.TrimSpace(current.String())
				if text != "" {
					kind := model.BlockParagraph
					metadata := map[string]any{}
					if tableDepth > 0 {
						kind = model.BlockTable
						metadata["format"] = "docx_cell"
						metadata["table_index"] = tableIndex
						metadata["row"] = rowIndex
						metadata["column"] = cellIndex
					} else if level := headingLevel(style); level > 0 {
						kind = model.BlockHeading
						metadata["level"] = level
						if len(section) >= level {
							section = section[:level-1]
						}
						for len(section) < level-1 {
							section = append(section, "")
						}
						section = append(section, text)
					} else if strings.Contains(strings.ToLower(style), "list") {
						kind = model.BlockList
					}
					if style != "" {
						metadata["style"] = style
					}
					if pageNo > paragraphPage {
						metadata["page_end"] = pageNo
					}
					blocks = append(blocks, block(kind, paragraphPage, section, text, metadata))
				}
				inParagraph = false
			}
		}
	}
	return blocks, nil
}

func xmlAttr(element xml.StartElement, local string) string {
	for _, attr := range element.Attr {
		if attr.Name.Local == local {
			return attr.Value
		}
	}
	return ""
}

func headingLevel(style string) int {
	lower := strings.ToLower(style)
	for _, prefix := range []string{"heading", "标题"} {
		if strings.HasPrefix(lower, prefix) {
			level, _ := strconv.Atoi(strings.TrimSpace(strings.TrimPrefix(lower, prefix)))
			if level >= 1 && level <= 6 {
				return level
			}
		}
	}
	return 0
}

func block(kind string, pageNo int, section []string, text string, metadata map[string]any) model.DocumentBlock {
	cleanSection := make([]string, 0, len(section))
	for _, part := range section {
		if part = strings.TrimSpace(part); part != "" {
			cleanSection = append(cleanSection, part)
		}
	}
	if metadata == nil {
		metadata = map[string]any{}
	}
	raw, _ := json.Marshal(metadata)
	return model.DocumentBlock{BlockType: kind, PageNo: pageNo,
		SectionPath: strings.Join(cleanSection, " / "), Text: strings.TrimSpace(text), Metadata: string(raw)}
}
