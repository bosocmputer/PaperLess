// Package httpx holds the shared HTTP response envelope, mirroring the
// {success, data, meta} / {success, error} convention used by sml-api-bybos so
// that clients (and the web app) see one consistent shape across both services.
package httpx

import "github.com/gin-gonic/gin"

// Meta carries pagination/aggregate info for list responses.
type Meta struct {
	Total int `json:"total"`
	Page  int `json:"page"`
	Size  int `json:"size"`
}

// OK writes a success envelope: {"success": true, "data": <data>}.
func OK(c *gin.Context, status int, data any) {
	c.JSON(status, gin.H{"success": true, "data": data})
}

// List writes a success envelope with pagination meta.
func List(c *gin.Context, status int, data any, meta Meta) {
	c.JSON(status, gin.H{"success": true, "data": data, "meta": meta})
}

// Error writes a failure envelope:
// {"success": false, "error": {"code": <code>, "message": <message>}}.
// code is a stable machine-readable string (e.g. "duplicate_document") that the
// web app maps to a specific UI error state; message is human-readable.
func Error(c *gin.Context, status int, code, message string) {
	c.JSON(status, gin.H{
		"success": false,
		"error":   gin.H{"code": code, "message": message},
	})
}
