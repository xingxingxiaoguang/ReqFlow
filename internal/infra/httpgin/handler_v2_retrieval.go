package httpgin

import (
	"fmt"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"

	appretrieval "reqflow/internal/app/retrieval"
)

func retrievalListLimit(c *gin.Context) (int, error) {
	limit, err := strconv.Atoi(c.DefaultQuery("limit", "100"))
	if err != nil {
		return 0, fmt.Errorf("limit 必须是整数")
	}
	if limit < 1 || limit > 200 {
		return 0, fmt.Errorf("limit 必须在 1..200 之间")
	}
	return limit, nil
}

func (h *handlers) v2GetRetrievalSnapshot(c *gin.Context) {
	snapshot, err := h.svc.V2Retrieval.GetSnapshotView(c.Request.Context(), c.Param("id"))
	if err != nil {
		fail(c, http.StatusNotFound, "RetrievalSnapshot 不存在")
		return
	}
	ok(c, gin.H{"retrieval_snapshot": snapshot})
}

func (h *handlers) v2ListRetrievalSnapshots(c *gin.Context) {
	limit, err := retrievalListLimit(c)
	if err != nil {
		fail(c, http.StatusBadRequest, err.Error())
		return
	}
	snapshots, err := h.svc.V2Retrieval.ListSnapshotViews(c.Request.Context(), c.Query("dataset_id"),
		c.Query("search_spec_hash"), c.Query("status"), limit)
	if err != nil {
		fail(c, http.StatusBadRequest, err.Error())
		return
	}
	ok(c, gin.H{"retrieval_snapshots": snapshots})
}

func (h *handlers) v2SearchRetrieval(c *gin.Context) {
	var request appretrieval.SearchAPIRequest
	if err := c.ShouldBindJSON(&request); err != nil {
		fail(c, http.StatusBadRequest, "检索请求 JSON 非法")
		return
	}
	response, err := h.svc.V2Retrieval.SearchAPI(c.Request.Context(), request)
	if err != nil {
		fail(c, http.StatusBadRequest, err.Error())
		return
	}
	ok(c, gin.H{"search": response})
}
