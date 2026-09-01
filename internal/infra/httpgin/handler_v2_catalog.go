package httpgin

import (
	"net/http"

	"github.com/gin-gonic/gin"

	appcatalog "reqflow/internal/app/catalog"
)

func catalogQuery(c *gin.Context) (appcatalog.Query, error) {
	limit, err := v2CatalogLimit(c)
	return appcatalog.Query{WorkspaceID: c.Query("workspace_id"), Status: c.Query("status"),
		Purpose: c.Query("purpose"), TargetSchemaID: c.Query("target_schema_id"), Limit: limit}, err
}

func (h *handlers) v2ListTaskDefinitions(c *gin.Context) {
	query, err := catalogQuery(c)
	if err != nil {
		fail(c, http.StatusBadRequest, err.Error())
		return
	}
	definitions, err := h.svc.V2Catalog.ListDefinitions(c.Request.Context(), query)
	if err != nil {
		fail(c, http.StatusBadRequest, err.Error())
		return
	}
	ok(c, gin.H{"task_definitions": definitions})
}

func (h *handlers) v2ListSchemas(c *gin.Context) {
	query, err := catalogQuery(c)
	if err != nil {
		fail(c, http.StatusBadRequest, err.Error())
		return
	}
	schemas, err := h.svc.V2Catalog.ListSchemas(c.Request.Context(), query)
	if err != nil {
		fail(c, http.StatusBadRequest, err.Error())
		return
	}
	ok(c, gin.H{"schemas": schemas})
}

func (h *handlers) v2ListDatasets(c *gin.Context) {
	query, err := catalogQuery(c)
	if err != nil {
		fail(c, http.StatusBadRequest, err.Error())
		return
	}
	datasets, err := h.svc.V2Catalog.ListDatasets(c.Request.Context(), query)
	if err != nil {
		fail(c, http.StatusBadRequest, err.Error())
		return
	}
	ok(c, gin.H{"datasets": datasets})
}

func (h *handlers) v2ListDatasetBatches(c *gin.Context) {
	limit, err := v2CatalogLimit(c)
	if err != nil {
		fail(c, http.StatusBadRequest, err.Error())
		return
	}
	batches, err := h.svc.V2Catalog.ListBatches(c.Request.Context(), c.Param("id"), limit)
	if err != nil {
		fail(c, http.StatusBadRequest, err.Error())
		return
	}
	ok(c, gin.H{"batches": batches})
}

func (h *handlers) v2ArchiveDataset(c *gin.Context) {
	if err := h.svc.V2Catalog.ArchiveDataset(c.Request.Context(), c.Param("id")); err != nil {
		fail(c, http.StatusConflict, err.Error())
		return
	}
	ok(c, gin.H{"archived": true})
}

func (h *handlers) v2RestoreDataset(c *gin.Context) {
	if err := h.svc.V2Catalog.RestoreDataset(c.Request.Context(), c.Param("id")); err != nil {
		fail(c, http.StatusConflict, err.Error())
		return
	}
	ok(c, gin.H{"restored": true})
}

func (h *handlers) v2ListAssetSets(c *gin.Context) {
	query, err := catalogQuery(c)
	if err != nil {
		fail(c, http.StatusBadRequest, err.Error())
		return
	}
	sets, err := h.svc.V2Catalog.ListAssetSets(c.Request.Context(), query)
	if err != nil {
		fail(c, http.StatusBadRequest, err.Error())
		return
	}
	ok(c, gin.H{"asset_sets": sets})
}

func (h *handlers) v2ListExtractionProfiles(c *gin.Context) {
	query, err := catalogQuery(c)
	if err != nil {
		fail(c, http.StatusBadRequest, err.Error())
		return
	}
	profiles, err := h.svc.V2Catalog.ListExtractionProfiles(c.Request.Context(), query)
	if err != nil {
		fail(c, http.StatusBadRequest, err.Error())
		return
	}
	ok(c, gin.H{"extraction_profiles": profiles})
}

func (h *handlers) v2ArchiveTask(c *gin.Context) {
	if err := h.svc.V2Catalog.ArchiveTask(c.Request.Context(), c.Param("id")); err != nil {
		fail(c, http.StatusConflict, err.Error())
		return
	}
	ok(c, gin.H{"archived": true})
}

func (h *handlers) v2RestoreTask(c *gin.Context) {
	if err := h.svc.V2Catalog.RestoreTask(c.Request.Context(), c.Param("id")); err != nil {
		fail(c, http.StatusConflict, err.Error())
		return
	}
	ok(c, gin.H{"restored": true})
}

func (h *handlers) v2ListArchives(c *gin.Context) {
	query, err := catalogQuery(c)
	if err != nil {
		fail(c, http.StatusBadRequest, err.Error())
		return
	}
	tasks, err := h.svc.V2Catalog.ListArchivedTasks(c.Request.Context(), query)
	if err != nil {
		fail(c, http.StatusBadRequest, err.Error())
		return
	}
	query.Status = "archived"
	datasets, err := h.svc.V2Catalog.ListDatasets(c.Request.Context(), query)
	if err != nil {
		fail(c, http.StatusBadRequest, err.Error())
		return
	}
	ok(c, gin.H{"tasks": tasks, "datasets": datasets})
}
