package httpgin

import (
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"

	apppipeline "reqflow/internal/app/pipeline"
)

func (h *handlers) v2CreateSchema(c *gin.Context) {
	var request apppipeline.CreateSchemaRequest
	if err := c.ShouldBindJSON(&request); err != nil {
		fail(c, http.StatusBadRequest, "Schema JSON 非法")
		return
	}
	schema, err := h.svc.V2Datasets.RegisterSchema(c.Request.Context(), request)
	if err != nil {
		fail(c, http.StatusBadRequest, err.Error())
		return
	}
	c.JSON(http.StatusCreated, gin.H{"success": true, "data": gin.H{"schema": schema}})
}

func (h *handlers) v2CreateDataset(c *gin.Context) {
	var request apppipeline.CreateDatasetRequest
	if err := c.ShouldBindJSON(&request); err != nil {
		fail(c, http.StatusBadRequest, "Dataset JSON 非法")
		return
	}
	dataset, err := h.svc.V2Datasets.RegisterDataset(c.Request.Context(), request)
	if err != nil {
		fail(c, http.StatusBadRequest, err.Error())
		return
	}
	c.JSON(http.StatusCreated, gin.H{"success": true, "data": gin.H{"dataset": dataset}})
}

func (h *handlers) v2CreateBatch(c *gin.Context) {
	var request apppipeline.CreateBatchRequest
	if err := c.ShouldBindJSON(&request); err != nil {
		fail(c, http.StatusBadRequest, "Batch JSON 非法")
		return
	}
	batch, err := h.svc.V2Datasets.OpenBatch(c.Request.Context(), c.Param("id"), request)
	if err != nil {
		fail(c, http.StatusBadRequest, err.Error())
		return
	}
	c.JSON(http.StatusCreated, gin.H{"success": true, "data": gin.H{"batch": batch}})
}

func (h *handlers) v2CommitBatch(c *gin.Context) {
	var request apppipeline.CommitBatchRequest
	if err := c.ShouldBindJSON(&request); err != nil {
		fail(c, http.StatusBadRequest, "Batch items JSON 非法")
		return
	}
	batch, err := h.svc.V2Datasets.PublishBatch(c.Request.Context(), c.Param("id"), request)
	if err != nil {
		fail(c, http.StatusConflict, err.Error())
		return
	}
	ok(c, gin.H{"batch": batch})
}

func (h *handlers) v2ListDatasetItems(c *gin.Context) {
	afterSeq, err := strconv.ParseInt(c.DefaultQuery("after_seq", "0"), 10, 64)
	if err != nil || afterSeq < 0 {
		fail(c, http.StatusBadRequest, "after_seq 必须是非负整数")
		return
	}
	throughSeq, err := strconv.ParseInt(c.DefaultQuery("through_seq", "0"), 10, 64)
	if err != nil || throughSeq < 0 {
		fail(c, http.StatusBadRequest, "through_seq 必须是非负整数")
		return
	}
	limit, err := strconv.Atoi(c.DefaultQuery("limit", "500"))
	if err != nil {
		fail(c, http.StatusBadRequest, "limit 必须是整数")
		return
	}
	items, err := h.svc.V2Datasets.ListItems(c.Request.Context(), c.Param("id"), afterSeq, throughSeq, limit)
	if err != nil {
		fail(c, http.StatusBadRequest, err.Error())
		return
	}
	ok(c, gin.H{"items": items, "after_seq": afterSeq, "through_seq": throughSeq})
}
