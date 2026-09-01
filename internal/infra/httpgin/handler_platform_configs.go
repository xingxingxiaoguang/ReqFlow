package httpgin

import (
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"

	appplatformconfig "reqflow/internal/app/platformconfig"
)

func (h *handlers) listPlatformConfigs(c *gin.Context) {
	view, err := h.svc.PlatformConfigs.Catalog(c.Request.Context(), c.Query("workspace_id"))
	if err != nil {
		fail(c, http.StatusInternalServerError, err.Error())
		return
	}
	ok(c, view)
}

func (h *handlers) createPlatformConfig(c *gin.Context) {
	var input appplatformconfig.UpsertInput
	if err := c.ShouldBindJSON(&input); err != nil {
		fail(c, http.StatusBadRequest, "平台配置参数非法")
		return
	}
	view, err := h.svc.PlatformConfigs.Create(c.Request.Context(), c.Query("workspace_id"), c.Param("kind"), input)
	if err != nil {
		fail(c, platformConfigErrorStatus(err), err.Error())
		return
	}
	c.JSON(http.StatusCreated, gin.H{"success": true, "data": view})
}

func (h *handlers) updatePlatformConfig(c *gin.Context) {
	var input appplatformconfig.UpsertInput
	if err := c.ShouldBindJSON(&input); err != nil {
		fail(c, http.StatusBadRequest, "平台配置参数非法")
		return
	}
	view, err := h.svc.PlatformConfigs.Update(c.Request.Context(), c.Query("workspace_id"),
		c.Param("kind"), c.Param("id"), input)
	if err != nil {
		fail(c, platformConfigErrorStatus(err), err.Error())
		return
	}
	ok(c, view)
}

func (h *handlers) deletePlatformConfig(c *gin.Context) {
	err := h.svc.PlatformConfigs.Delete(c.Request.Context(), c.Query("workspace_id"), c.Param("kind"), c.Param("id"))
	if err != nil {
		fail(c, platformConfigErrorStatus(err), err.Error())
		return
	}
	ok(c, gin.H{"deleted": true})
}

func (h *handlers) activatePlatformConfig(c *gin.Context) {
	err := h.svc.PlatformConfigs.Activate(c.Request.Context(), c.Query("workspace_id"), c.Param("kind"), c.Param("id"))
	if err != nil {
		fail(c, platformConfigErrorStatus(err), err.Error())
		return
	}
	ok(c, gin.H{"active": true})
}

func platformConfigErrorStatus(err error) int {
	if errors.Is(err, appplatformconfig.ErrNotFound) {
		return http.StatusNotFound
	}
	return http.StatusBadRequest
}
