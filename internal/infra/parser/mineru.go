package parser

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"reqflow/internal/port"
)

// MinerUOptions MinerU 云端解析参数（精准解析 API v4；单文件 ≤200MB、≤200 页）。
type MinerUOptions struct {
	Enabled      bool
	APIURL       string
	APIToken     string
	ModelVersion string // vlm（MinerU 2.5）| pipeline
	Timeout      time.Duration
	PollInterval time.Duration
}

// MinerU 云端 PDF 解析客户端。
type MinerU struct {
	opt  MinerUOptions
	http *http.Client
}

// NewMinerU 构造客户端。
func NewMinerU(opt MinerUOptions) *MinerU {
	if opt.Timeout <= 0 {
		opt.Timeout = 10 * time.Minute
	}
	if opt.PollInterval <= 0 {
		opt.PollInterval = 5 * time.Second
	}
	return &MinerU{opt: opt, http: &http.Client{Timeout: 120 * time.Second}}
}

// ParsePDF 解析本地 PDF，返回 Markdown 文本（表格已转 Markdown、水印已处理）。
// 流程：申请预签名上传链接 → PUT 上传 → 轮询批次结果 → 下载 zip 取 full.md。
func (m *MinerU) ParsePDF(ctx context.Context, filename string, content []byte, onProgress func(port.ParseProgress)) (string, error) {
	if !m.opt.Enabled || m.opt.APIToken == "" {
		return "", fmt.Errorf("PDF 解析未配置：请在 config.yaml 填写 parser.mineru.api_token 后重启（或使用 docx/md/txt 格式）")
	}
	ctx, cancel := context.WithTimeout(ctx, m.opt.Timeout)
	defer cancel()

	batchID, uploadURL, err := m.createUploadURL(ctx, filename)
	if err != nil {
		return "", err
	}
	if err := m.uploadFile(ctx, uploadURL, content); err != nil {
		return "", err
	}
	zipURL, err := m.waitBatchResult(ctx, batchID, onProgress)
	if err != nil {
		return "", err
	}
	return m.downloadMarkdown(ctx, zipURL)
}

func (m *MinerU) createUploadURL(ctx context.Context, fileName string) (batchID, uploadURL string, err error) {
	body, err := json.Marshal(map[string]any{
		"model_version": m.opt.ModelVersion,
		"files":         []map[string]string{{"name": fileName, "data_id": fileName}},
	})
	if err != nil {
		return "", "", err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		strings.TrimSuffix(m.opt.APIURL, "/")+"/api/v4/file-urls/batch", bytes.NewReader(body))
	if err != nil {
		return "", "", err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+m.opt.APIToken)

	resp, err := m.http.Do(req)
	if err != nil {
		return "", "", fmt.Errorf("申请 MinerU 上传链接失败: %w", err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	var out struct {
		Code int    `json:"code"`
		Msg  string `json:"msg"`
		Data struct {
			BatchID  string   `json:"batch_id"`
			FileURLs []string `json:"file_urls"`
		} `json:"data"`
	}
	if err := json.Unmarshal(raw, &out); err != nil || out.Code != 0 || out.Data.BatchID == "" || len(out.Data.FileURLs) == 0 {
		return "", "", fmt.Errorf("申请 MinerU 上传链接失败: %s", trunc(string(raw), 200))
	}
	return out.Data.BatchID, out.Data.FileURLs[0], nil
}

// uploadFile 上传到 OSS 预签名 URL。
// 必须显式避免 Content-Type：预签名未包含该头，多带会导致 SignatureDoesNotMatch。
func (m *MinerU) uploadFile(ctx context.Context, uploadURL string, content []byte) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, uploadURL, bytes.NewReader(content))
	if err != nil {
		return err
	}
	req.ContentLength = int64(len(content))
	resp, err := m.http.Do(req)
	if err != nil {
		return fmt.Errorf("上传文件到 MinerU 失败: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return fmt.Errorf("上传文件到 MinerU 失败: HTTP %d %s", resp.StatusCode, trunc(string(body), 150))
	}
	return nil
}

func (m *MinerU) waitBatchResult(ctx context.Context, batchID string, onProgress func(port.ParseProgress)) (string, error) {
	start := time.Now()
	for {
		select {
		case <-ctx.Done():
			return "", fmt.Errorf("MinerU 解析超时或被取消")
		case <-time.After(m.opt.PollInterval):
		}
		if onProgress != nil {
			onProgress(port.ParseProgress{
				Message:    fmt.Sprintf("MinerU 云端解析中… 已等待 %d 秒", int(time.Since(start).Seconds())),
				ElapsedSec: int(time.Since(start).Seconds()),
			})
		}
		rawURL := strings.TrimSuffix(m.opt.APIURL, "/") + "/api/v4/extract-results/batch/" + url.PathEscape(batchID)
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
		if err != nil {
			return "", err
		}
		req.Header.Set("Authorization", "Bearer "+m.opt.APIToken)
		resp, err := m.http.Do(req)
		if err != nil {
			continue // 偶发网络抖动不直接失败，继续轮询
		}
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
		resp.Body.Close()

		var out struct {
			Code int    `json:"code"`
			Msg  string `json:"msg"`
			Data struct {
				ExtractResult []struct {
					State      string `json:"state"`
					FullZipURL string `json:"full_zip_url"`
					ErrMsg     string `json:"err_msg"`
				} `json:"extract_result"`
			} `json:"data"`
		}
		if err := json.Unmarshal(raw, &out); err != nil || out.Code != 0 {
			continue // 查询异常同样继续轮询
		}
		if len(out.Data.ExtractResult) == 0 {
			continue
		}
		item := out.Data.ExtractResult[0]
		switch item.State {
		case "done":
			if item.FullZipURL != "" {
				return item.FullZipURL, nil
			}
		case "failed":
			msg := item.ErrMsg
			if msg == "" {
				msg = "未提供错误信息"
			}
			return "", fmt.Errorf("MinerU 解析失败: %s", msg)
		}
		// waiting-file / pending / running → 继续等
	}
}

func (m *MinerU) downloadMarkdown(ctx context.Context, zipURL string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, zipURL, nil)
	if err != nil {
		return "", err
	}
	resp, err := m.http.Do(req)
	if err != nil {
		return "", fmt.Errorf("下载 MinerU 结果失败: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return "", fmt.Errorf("下载 MinerU 结果失败: HTTP %d", resp.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, 512<<20))
	if err != nil {
		return "", err
	}
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return "", fmt.Errorf("MinerU 结果包解析失败: %w", err)
	}
	var entry *zip.File
	for i, f := range zr.File {
		if f.Name == "full.md" || (!f.FileInfo().IsDir() && strings.HasSuffix(f.Name, ".md")) {
			entry = zr.File[i]
			break
		}
	}
	if entry == nil {
		return "", fmt.Errorf("MinerU 结果包中未找到 Markdown 文件")
	}
	rc, err := entry.Open()
	if err != nil {
		return "", err
	}
	defer rc.Close()
	md, err := io.ReadAll(io.LimitReader(rc, 64<<20))
	if err != nil {
		return "", err
	}
	if len(bytes.TrimSpace(md)) == 0 {
		return "", fmt.Errorf("MinerU 解析结果为空（PDF 可能无文本层或全部为图片）")
	}
	return string(md), nil
}

func trunc(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
