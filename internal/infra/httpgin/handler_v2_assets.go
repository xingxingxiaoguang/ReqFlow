package httpgin

import (
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"

	apppipeline "reqflow/internal/app/pipeline"
)

func (h *handlers) v2UploadAsset(c *gin.Context) {
	maxBytes := h.svc.MaxFileMB << 20
	if maxBytes <= 0 {
		maxBytes = 50 << 20
	}
	// multipart 自身有少量边界开销，业务层仍按真实文件字节做精确限制。
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, maxBytes+(1<<20))
	header, err := c.FormFile("file")
	if err != nil {
		fail(c, http.StatusBadRequest, "缺少 multipart file 字段或请求超过大小限制")
		return
	}
	if header.Size > maxBytes {
		fail(c, http.StatusRequestEntityTooLarge, "文件超过大小限制")
		return
	}
	file, err := header.Open()
	if err != nil {
		fail(c, http.StatusBadRequest, "读取上传文件失败")
		return
	}
	defer file.Close()
	view, created, err := h.svc.V2Assets.RegisterAsset(c.Request.Context(), apppipeline.UploadAssetRequest{
		WorkspaceID: c.PostForm("workspace_id"), Filename: header.Filename,
		MIMEType: header.Header.Get("Content-Type"), SizeBytes: header.Size, Content: file,
	})
	if err != nil {
		fail(c, http.StatusBadRequest, err.Error())
		return
	}
	status := http.StatusCreated
	if !created {
		status = http.StatusOK
	}
	c.JSON(status, gin.H{"success": true, "data": gin.H{"asset": view, "created": created}})
}

func (h *handlers) v2CreateAssetSet(c *gin.Context) {
	var request apppipeline.CreateAssetSetRequest
	if err := c.ShouldBindJSON(&request); err != nil {
		fail(c, http.StatusBadRequest, "AssetSet JSON 非法")
		return
	}
	view, err := h.svc.V2Assets.RegisterAssetSet(c.Request.Context(), request)
	if err != nil {
		fail(c, http.StatusBadRequest, err.Error())
		return
	}
	c.JSON(http.StatusCreated, gin.H{"success": true, "data": gin.H{"asset_set": view}})
}

func (h *handlers) v2GetAssetSet(c *gin.Context) {
	view, err := h.svc.V2Assets.ViewAssetSet(c.Request.Context(), c.Param("id"))
	if err != nil {
		fail(c, http.StatusNotFound, err.Error())
		return
	}
	ok(c, gin.H{"asset_set": view})
}

func (h *handlers) v2GetParsedDocumentSet(c *gin.Context) {
	view, err := h.svc.V2Assets.ViewParsedDocumentSet(c.Request.Context(), c.Param("id"))
	if err != nil {
		fail(c, http.StatusNotFound, err.Error())
		return
	}
	ok(c, gin.H{"parsed_document_set": view})
}

func (h *handlers) v2GetDocumentBlocks(c *gin.Context) {
	after, err := strconv.Atoi(c.DefaultQuery("after_ordinal", "-1"))
	if err != nil || after < -1 {
		fail(c, http.StatusBadRequest, "after_ordinal 必须是不小于 -1 的整数")
		return
	}
	limit, err := strconv.Atoi(c.DefaultQuery("limit", "200"))
	if err != nil || limit <= 0 {
		fail(c, http.StatusBadRequest, "limit 必须是正整数")
		return
	}
	view, err := h.svc.V2Assets.ViewDocumentBlocks(c.Request.Context(), c.Param("id"), after, limit)
	if err != nil {
		fail(c, http.StatusNotFound, err.Error())
		return
	}
	ok(c, gin.H{"parsed_document": view, "after_ordinal": after})
}
