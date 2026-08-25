//go:build !embed

// 开发构建：前端由 Vite dev server 提供（:5173 代理 /api → :8080），二进制不内嵌静态资源。
package main

import "github.com/gin-gonic/gin"

func mountStatic(r *gin.Engine) {}
