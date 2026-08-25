package httpgin

import (
	"strconv"

	"github.com/gin-gonic/gin"

	"reqflow/internal/app"
)

// browseFilterFromQuery 从 query 参数构造浏览查询。
func browseFilterFromQuery(c *gin.Context) app.BrowseFilter {
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "50"))
	offset, _ := strconv.Atoi(c.DefaultQuery("offset", "0"))
	return app.BrowseFilter{
		ProjectID: c.Query("project_id"),
		Search:    c.Query("search"),
		Limit:     limit,
		Offset:    offset,
	}
}
