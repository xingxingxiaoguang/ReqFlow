package httpgin

import (
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"

	appextraction "reqflow/internal/app/extraction"
)

func (h *handlers) v2DeleteExtractionProfile(c *gin.Context) {
	deleted, err := h.svc.V2Extractions.DeleteProfile(c.Request.Context(), c.Param("id"))
	if err != nil {
		if errors.Is(err, appextraction.ErrProfileInUse) {
			fail(c, http.StatusConflict, err.Error())
			return
		}
		fail(c, http.StatusNotFound, "ExtractionProfile 不存在")
		return
	}
	if !deleted {
		fail(c, http.StatusNotFound, "ExtractionProfile 不存在")
		return
	}
	ok(c, gin.H{"deleted": true})
}

func (h *handlers) v2CreateExtractionProfile(c *gin.Context) {
	var request appextraction.CreateExtractionProfileRequest
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
