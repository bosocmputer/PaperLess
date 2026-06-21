package handlers

import (
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5/pgxpool"
	"go.uber.org/zap"

	"paperless-api/internal/httpx"
)

type UserHandler struct {
	pool *pgxpool.Pool
	log  *zap.Logger
}

func NewUserHandler(pool *pgxpool.Pool, log *zap.Logger) *UserHandler {
	return &UserHandler{pool: pool, log: log}
}

// List godoc: GET /users
// Requires: workflow_admin or system_admin.
// Returns active users only — used by the workflow editor to pick step assignees.
// id is a string (FormatInt) to match the rest of the JSON API.
func (h *UserHandler) List(c *gin.Context) {
	ctx := c.Request.Context()

	rows, err := h.pool.Query(ctx, `
		SELECT id, username, display_name, status
		  FROM users
		 WHERE status='active'
		 ORDER BY display_name
		 LIMIT 500
	`)
	if err != nil {
		h.log.Error("list users", zap.Error(err))
		httpx.Error(c, http.StatusInternalServerError, "internal_error", "list failed")
		return
	}
	defer rows.Close()

	type userRow struct {
		ID          string `json:"id"`
		Username    string `json:"username"`
		DisplayName string `json:"display_name"`
		Status      string `json:"status"`
	}

	users := []userRow{}
	for rows.Next() {
		var u userRow
		var id int64
		if err := rows.Scan(&id, &u.Username, &u.DisplayName, &u.Status); err != nil {
			h.log.Error("scan user row", zap.Error(err))
			httpx.Error(c, http.StatusInternalServerError, "internal_error", "list failed")
			return
		}
		u.ID = strconv.FormatInt(id, 10)
		users = append(users, u)
	}
	if err := rows.Err(); err != nil {
		h.log.Error("users rows error", zap.Error(err))
		httpx.Error(c, http.StatusInternalServerError, "internal_error", "list failed")
		return
	}

	httpx.OK(c, http.StatusOK, users)
}
