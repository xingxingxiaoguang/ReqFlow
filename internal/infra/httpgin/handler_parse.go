package httpgin

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"reqflow/internal/app"
)

// parseStream POST /api/parse（multipart file）→ SSE。
// 解析完成后以 parsed 事件返回全文，前端确认门展示预览/编辑，
// 用户确认后再调 /api/analyze 进入 LLM 分析。
func (h *handlers) parseStream(c *gin.Context) {
	fileHeader, err := c.FormFile("file")
	if err != nil {
		fail(c, 400, "请上传文件")
		return
	}
	if h.svc.MaxFileMB > 0 && fileHeader.Size > h.svc.MaxFileMB<<20 {
		fail(c, 400, fmt.Sprintf("文件超过大小限制 %dMB", h.svc.MaxFileMB))
		return
	}

	fileName := filepath.Base(fileHeader.Filename)
	if err := os.MkdirAll(h.svc.UploadDir, 0o755); err != nil {
		fail(c, 500, "创建上传目录失败: "+err.Error())
		return
	}
	savedPath := filepath.Join(h.svc.UploadDir, fmt.Sprintf("%d-%s", time.Now().UnixMilli(), fileName))
	if err := c.SaveUploadedFile(fileHeader, savedPath); err != nil {
		fail(c, 500, "保存上传文件失败: "+err.Error())
		return
	}
	defer os.Remove(savedPath) // 解析完成后清理暂存

	startSSE(c)
	pdfMsg := "文件已上传，正在解析文档内容…"
	if strings.EqualFold(filepath.Ext(fileName), ".pdf") {
		pdfMsg = "PDF 已上传，正在提交 MinerU 云端解析（表格/水印处理）…"
	}
	sendEvent(c, "progress", gin.H{"stage": "parsing", "status": "running", "message": pdfMsg})

	text, err := h.svc.Parse.Run(c.Request.Context(), fileName, savedPath, func(p app.ParseProgress) {
		if clientGone(c) {
			return
		}
		sendEvent(c, "progress", gin.H{
			"stage": "parsing", "status": "running",
			"message": p.Message, "elapsedSec": p.ElapsedSec,
		})
	})
	if err != nil {
		sendEvent(c, "error", gin.H{"message": err.Error()})
		return
	}
	sendEvent(c, "parsed", gin.H{"file_name": fileName, "text": text})
}
