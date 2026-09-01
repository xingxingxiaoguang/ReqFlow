package httpgin

import (
	"fmt"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"

	"reqflow/internal/domain/model"
)

func v2CatalogLimit(c *gin.Context) (int, error) {
	limit, err := strconv.Atoi(c.DefaultQuery("limit", "100"))
	if err != nil || limit < 1 || limit > 200 {
		return 0, fmt.Errorf("limit 必须在 1..200 之间")
	}
	return limit, nil
}

func (h *handlers) v2GetAnalysisResult(c *gin.Context) {
	result, err := h.svc.V2Analysis.GetResultView(c.Request.Context(), c.Param("id"))
	if err != nil {
		fail(c, http.StatusNotFound, "AnalysisResult 不存在")
		return
	}
	ok(c, gin.H{"analysis_result": result})
}

func (h *handlers) v2ListArtifacts(c *gin.Context) {
	limit, err := v2CatalogLimit(c)
	if err != nil {
		fail(c, http.StatusBadRequest, err.Error())
		return
	}
	artifacts, err := h.svc.V2Artifacts.ListViews(c.Request.Context(), c.Query("workspace_id"), c.Query("kind"), limit)
	if err != nil {
		fail(c, http.StatusBadRequest, err.Error())
		return
	}
	ok(c, gin.H{"artifacts": artifacts})
}

func (h *handlers) v2GetArtifact(c *gin.Context) {
	artifact, err := h.svc.V2Artifacts.GetView(c.Request.Context(), c.Param("id"))
	if err != nil {
		fail(c, http.StatusNotFound, "Artifact 不存在")
		return
	}
	ok(c, gin.H{"artifact": artifact})
}

func (h *handlers) v2DownloadArtifact(c *gin.Context) {
	artifact, reader, err := h.svc.V2Artifacts.Open(c.Request.Context(), c.Param("id"))
	if err != nil {
		fail(c, http.StatusNotFound, "Artifact 内容不存在")
		return
	}
	defer reader.Close()
	contentType := "application/json; charset=utf-8"
	extension := "json"
	if artifact.Kind == model.ArtifactMarkdown {
		contentType, extension = "text/markdown; charset=utf-8", "md"
	}
	c.Header("Content-Disposition", fmt.Sprintf("attachment; filename=%q", artifact.Name+"."+extension))
	c.DataFromReader(http.StatusOK, -1, contentType, reader, nil)
}
