package httpgin

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

func (h *handlers) v2GetTransformedRecordSet(c *gin.Context) {
	view, err := h.svc.V2Cleaning.TransformedRecordSetView(c.Request.Context(), c.Param("id"))
	if err != nil {
		fail(c, http.StatusNotFound, err.Error())
		return
	}
	ok(c, gin.H{"transformed_record_set": view})
}

func (h *handlers) v2GetValidationResultSet(c *gin.Context) {
	view, err := h.svc.V2Cleaning.ValidationResultSetView(c.Request.Context(), c.Param("id"))
	if err != nil {
		fail(c, http.StatusNotFound, err.Error())
		return
	}
	ok(c, gin.H{"validation_result_set": view})
}
