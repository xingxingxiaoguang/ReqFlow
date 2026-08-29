package parser

import (
	"archive/zip"
	"bytes"
	"context"
	"strings"
	"testing"

	"reqflow/internal/domain/model"
	"reqflow/internal/port"
)

func TestParseMarkdownProducesStructuredBlocks(t *testing.T) {
	content := "<!-- page=2 -->\n# 规格\n\n说明文字\n\n- 一\n- 二\n\n| 参数 | 值 |\n|---|---|\n| 电压 | 220V |"
	blocks, err := New(Options{MaxFileMB: 1}).Parse(context.Background(), port.ParseSource{
		Filename: "spec.md", SizeBytes: int64(len(content)), Content: strings.NewReader(content),
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	wantKinds := []string{model.BlockHeading, model.BlockParagraph, model.BlockList, model.BlockTable}
	if len(blocks) != len(wantKinds) {
		t.Fatalf("blocks=%+v", blocks)
	}
	for i, want := range wantKinds {
		if blocks[i].Ordinal != i || blocks[i].BlockType != want || blocks[i].PageNo != 2 {
			t.Fatalf("block[%d]=%+v want kind=%s page=2", i, blocks[i], want)
		}
	}
	if blocks[1].SectionPath != "规格" {
		t.Fatalf("section_path=%q", blocks[1].SectionPath)
	}
}

func TestParseDocxKeepsHeadingAndPageBreak(t *testing.T) {
	var document bytes.Buffer
	archive := zip.NewWriter(&document)
	entry, err := archive.Create("word/document.xml")
	if err != nil {
		t.Fatal(err)
	}
	xml := `<w:document xmlns:w="urn:test"><w:body>
<w:p><w:pPr><w:pStyle w:val="Heading1"/></w:pPr><w:r><w:t>第一章</w:t></w:r></w:p>
<w:p><w:r><w:t>第一页</w:t><w:lastRenderedPageBreak/><w:t>第二页</w:t></w:r></w:p>
</w:body></w:document>`
	if _, err := entry.Write([]byte(xml)); err != nil {
		t.Fatal(err)
	}
	if err := archive.Close(); err != nil {
		t.Fatal(err)
	}
	blocks, err := New(Options{MaxFileMB: 1}).Parse(context.Background(), port.ParseSource{
		Filename: "spec.docx", SizeBytes: int64(document.Len()), Content: bytes.NewReader(document.Bytes()),
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(blocks) != 2 || blocks[0].BlockType != model.BlockHeading || blocks[1].SectionPath != "第一章" {
		t.Fatalf("docx blocks=%+v", blocks)
	}
}

func TestParseDocxPreservesTableCellCoordinates(t *testing.T) {
	var document bytes.Buffer
	archive := zip.NewWriter(&document)
	entry, err := archive.Create("word/document.xml")
	if err != nil {
		t.Fatal(err)
	}
	xml := `<w:document xmlns:w="urn:test"><w:body><w:tbl><w:tr>
<w:tc><w:p><w:r><w:t>参数</w:t></w:r></w:p></w:tc>
<w:tc><w:p><w:r><w:t>值</w:t></w:r></w:p></w:tc>
</w:tr></w:tbl></w:body></w:document>`
	if _, err := entry.Write([]byte(xml)); err != nil {
		t.Fatal(err)
	}
	if err := archive.Close(); err != nil {
		t.Fatal(err)
	}
	blocks, err := New(Options{MaxFileMB: 1}).Parse(context.Background(), port.ParseSource{
		Filename: "table.docx", SizeBytes: int64(document.Len()), Content: bytes.NewReader(document.Bytes()),
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(blocks) != 2 || blocks[0].BlockType != model.BlockTable ||
		!strings.Contains(blocks[0].Metadata, `"column":0`) || !strings.Contains(blocks[1].Metadata, `"column":1`) {
		t.Fatalf("table blocks=%+v", blocks)
	}
}

func TestParseRejectsOversizedReaderWithoutTrustingDeclaredSize(t *testing.T) {
	content := bytes.Repeat([]byte("x"), (1<<20)+1)
	_, err := New(Options{MaxFileMB: 1}).Parse(context.Background(), port.ParseSource{
		Filename: "large.txt", SizeBytes: 0, Content: bytes.NewReader(content),
	}, nil)
	if err == nil || !strings.Contains(err.Error(), "超过大小限制") {
		t.Fatalf("oversized parse err=%v", err)
	}
}
