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

func (h *handlers) v2CreateRetrievalProfile(c *gin.Context) {
	var request appretrieval.CreateProfileRequest
	if err := c.ShouldBindJSON(&request); err != nil {
		fail(c, http.StatusBadRequest, "RetrievalProfile JSON 非法")
		return
	}
	profile, err := h.svc.V2Retrieval.RegisterProfile(c.Request.Context(), request)
	if err != nil {
		fail(c, http.StatusBadRequest, err.Error())
		return
	}
	c.JSON(http.StatusCreated, gin.H{"success": true, "data": gin.H{"retrieval_profile": profile}})
}

func (h *handlers) v2GetRetrievalProfile(c *gin.Context) {
	profile, err := h.svc.V2Retrieval.GetProfileView(c.Request.Context(), c.Param("id"))
	if err != nil {
		fail(c, http.StatusNotFound, "RetrievalProfile 不存在")
		return
	}
	ok(c, gin.H{"retrieval_profile": profile})
}

func (h *handlers) v2ListRetrievalProfiles(c *gin.Context) {
	limit, err := retrievalListLimit(c)
	if err != nil {
		fail(c, http.StatusBadRequest, err.Error())
		return
	}
	profiles, err := h.svc.V2Retrieval.ListProfileViews(c.Request.Context(), c.Query("workspace_id"),
		c.Query("dataset_schema_id"), limit)
	if err != nil {
		fail(c, http.StatusBadRequest, err.Error())
		return
	}
	ok(c, gin.H{"retrieval_profiles": profiles})
}

func (h *handlers) v2CloneRetrievalProfile(c *gin.Context) {
	var request appretrieval.CloneProfileRequest
	if err := c.ShouldBindJSON(&request); err != nil {
		fail(c, http.StatusBadRequest, "Clone RetrievalProfile JSON 非法")
		return
	}
	profile, err := h.svc.V2Retrieval.CloneProfileView(c.Request.Context(), c.Param("id"), request)
	if err != nil {
		fail(c, http.StatusBadRequest, err.Error())
		return
	}
	c.JSON(http.StatusCreated, gin.H{"success": true, "data": gin.H{"retrieval_profile": profile}})
}

func (h *handlers) v2DeleteRetrievalSnapshot(c *gin.Context) {
	deleted, err := h.svc.V2Retrieval.DeleteSnapshot(c.Request.Context(), c.Param("id"))
	if err != nil {
		fail(c, http.StatusInternalServerError, err.Error())
		return
	}
	if !deleted {
		fail(c, http.StatusNotFound, "RetrievalSnapshot 不存在")
		return
	}
	ok(c, gin.H{"deleted": true})
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
		c.Query("retrieval_profile_id"), c.Query("status"), limit)
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
