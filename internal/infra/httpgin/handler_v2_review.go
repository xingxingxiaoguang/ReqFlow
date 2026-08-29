package httpgin

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

func (h *handlers) v2GetApprovedRecordSet(c *gin.Context) {
	view, err := h.svc.V2Review.ApprovedRecordSetView(c.Request.Context(), c.Param("id"))
	if err != nil {
		fail(c, http.StatusNotFound, err.Error())
		return
	}
	ok(c, gin.H{"approved_record_set": view})
}
