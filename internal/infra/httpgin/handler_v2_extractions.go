package httpgin

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

func (h *handlers) v2GetRecordDraftSet(c *gin.Context) {
	view, err := h.svc.V2Extractions.ViewRecordDraftSet(c.Request.Context(), c.Param("id"))
	if err != nil {
		fail(c, http.StatusNotFound, err.Error())
		return
	}
	ok(c, gin.H{"record_draft_set": view})
}
