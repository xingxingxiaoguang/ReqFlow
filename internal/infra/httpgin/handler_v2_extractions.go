package httpgin

import (
	"net/http"

	"github.com/gin-gonic/gin"

	apppipeline "reqflow/internal/app/pipeline"
)

func (h *handlers) v2CreateExtractionProfile(c *gin.Context) {
	var request apppipeline.CreateExtractionProfileRequest
	if err := c.ShouldBindJSON(&request); err != nil {
		fail(c, http.StatusBadRequest, "ExtractionProfile JSON 非法")
		return
	}
	view, err := h.svc.V2Extractions.RegisterProfile(c.Request.Context(), request)
	if err != nil {
		fail(c, http.StatusBadRequest, err.Error())
		return
	}
	c.JSON(http.StatusCreated, gin.H{"success": true, "data": gin.H{"extraction_profile": view}})
}

func (h *handlers) v2GetExtractionProfile(c *gin.Context) {
	view, err := h.svc.V2Extractions.ViewProfile(c.Request.Context(), c.Param("id"))
	if err != nil {
		fail(c, http.StatusNotFound, err.Error())
		return
	}
	ok(c, gin.H{"extraction_profile": view})
}

func (h *handlers) v2GetRecordDraftSet(c *gin.Context) {
	view, err := h.svc.V2Extractions.ViewRecordDraftSet(c.Request.Context(), c.Param("id"))
	if err != nil {
		fail(c, http.StatusNotFound, err.Error())
		return
	}
	ok(c, gin.H{"record_draft_set": view})
}
