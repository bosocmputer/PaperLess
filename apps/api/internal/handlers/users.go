package handlers

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"go.uber.org/zap"

	"paperless-api/internal/auth"
	"paperless-api/internal/httpx"
	"paperless-api/internal/middleware"
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

// ── Phase C: user management (system_admin only, mounted under /admin/users) ──

type adminUserResponse struct {
	ID          string   `json:"id"`
	Username    string   `json:"username"`
	DisplayName string   `json:"display_name"`
	Email       string   `json:"email"`
	Phone       string   `json:"phone"`
	Status      string   `json:"status"`
	Roles       []string `json:"roles"`
}

// AdminList godoc: GET /admin/users[?include_inactive=1]
// Requires: system_admin. Full fields + role codes per user.
func (h *UserHandler) AdminList(c *gin.Context) {
	ctx := c.Request.Context()
	includeInactive := c.Query("include_inactive") == "1"

	where := "WHERE u.status='active'"
	if includeInactive {
		where = ""
	}

	rows, err := h.pool.Query(ctx, `
		SELECT u.id, u.username, u.display_name,
		       COALESCE(u.email,''), COALESCE(u.phone,''), u.status, r.code
		  FROM users u
		  LEFT JOIN user_roles ur ON ur.user_id = u.id
		  LEFT JOIN roles r ON r.id = ur.role_id
		  `+where+`
		 ORDER BY u.display_name, u.id
		 LIMIT 1000
	`)
	if err != nil {
		h.log.Error("admin list users", zap.Error(err))
		httpx.Error(c, http.StatusInternalServerError, "internal_error", "list failed")
		return
	}
	defer rows.Close()

	userMap := map[int64]*adminUserResponse{}
	var order []int64
	for rows.Next() {
		var (
			id          int64
			username    string
			displayName string
			email       string
			phone       string
			status      string
			roleCode    *string
		)
		if err := rows.Scan(&id, &username, &displayName, &email, &phone, &status, &roleCode); err != nil {
			h.log.Error("scan admin user row", zap.Error(err))
			httpx.Error(c, http.StatusInternalServerError, "internal_error", "list failed")
			return
		}
		u, ok := userMap[id]
		if !ok {
			u = &adminUserResponse{
				ID: strconv.FormatInt(id, 10), Username: username, DisplayName: displayName,
				Email: email, Phone: phone, Status: status, Roles: []string{},
			}
			userMap[id] = u
			order = append(order, id)
		}
		if roleCode != nil {
			u.Roles = append(u.Roles, *roleCode)
		}
	}
	if err := rows.Err(); err != nil {
		h.log.Error("admin users rows error", zap.Error(err))
		httpx.Error(c, http.StatusInternalServerError, "internal_error", "list failed")
		return
	}

	out := make([]adminUserResponse, 0, len(order))
	for _, id := range order {
		out = append(out, *userMap[id])
	}
	httpx.OK(c, http.StatusOK, out)
}

// ListRoles godoc: GET /roles — Requires system_admin. Role codes + names for the UI.
func (h *UserHandler) ListRoles(c *gin.Context) {
	ctx := c.Request.Context()
	rows, err := h.pool.Query(ctx, `SELECT code, name FROM roles ORDER BY code`)
	if err != nil {
		h.log.Error("list roles", zap.Error(err))
		httpx.Error(c, http.StatusInternalServerError, "internal_error", "list failed")
		return
	}
	defer rows.Close()

	type roleRow struct {
		Code string `json:"code"`
		Name string `json:"name"`
	}
	roles := []roleRow{}
	for rows.Next() {
		var r roleRow
		if err := rows.Scan(&r.Code, &r.Name); err != nil {
			h.log.Error("scan role", zap.Error(err))
			httpx.Error(c, http.StatusInternalServerError, "internal_error", "list failed")
			return
		}
		roles = append(roles, r)
	}
	httpx.OK(c, http.StatusOK, roles)
}

// resolveRoleIDs validates that every code exists and returns their ids.
// Returns (ids, true) on success; (nil, false) if any code is unknown.
func (h *UserHandler) resolveRoleIDs(ctx context.Context, q pgx.Tx, codes []string) ([]int64, bool, error) {
	if len(codes) == 0 {
		return nil, true, nil
	}
	rows, err := q.Query(ctx, `SELECT id, code FROM roles WHERE code = ANY($1)`, codes)
	if err != nil {
		return nil, false, err
	}
	defer rows.Close()
	found := map[string]int64{}
	for rows.Next() {
		var id int64
		var code string
		if err := rows.Scan(&id, &code); err != nil {
			return nil, false, err
		}
		found[code] = id
	}
	if err := rows.Err(); err != nil {
		return nil, false, err
	}
	ids := make([]int64, 0, len(codes))
	for _, code := range codes {
		id, ok := found[code]
		if !ok {
			return nil, false, nil // unknown role code
		}
		ids = append(ids, id)
	}
	return ids, true, nil
}

func validatePassword(pw string) bool {
	// bcrypt accepts at most 72 bytes; require a sane minimum.
	return len(pw) >= 6 && len(pw) <= 72
}

func nilIfEmpty(s string) any {
	if strings.TrimSpace(s) == "" {
		return nil
	}
	return strings.TrimSpace(s)
}

type createUserBody struct {
	Username    string   `json:"username"`
	DisplayName string   `json:"display_name"`
	Email       string   `json:"email"`
	Phone       string   `json:"phone"`
	Roles       []string `json:"roles"`
	Password    string   `json:"password"`
}

// Create godoc: POST /admin/users — Requires system_admin.
// Creates an active user, optionally with a password (bcrypt'd server-side,
// never logged). Without a password the user cannot log in until one is set.
func (h *UserHandler) Create(c *gin.Context) {
	var body createUserBody
	if err := c.ShouldBindJSON(&body); err != nil {
		httpx.Error(c, http.StatusBadRequest, "invalid_request", "invalid JSON body")
		return
	}
	username := strings.TrimSpace(body.Username)
	displayName := strings.TrimSpace(body.DisplayName)
	if username == "" || len(username) > 100 {
		httpx.Error(c, http.StatusBadRequest, "invalid_request", "username is required (≤100 chars)")
		return
	}
	if displayName == "" || len(displayName) > 200 {
		httpx.Error(c, http.StatusBadRequest, "invalid_request", "display_name is required (≤200 chars)")
		return
	}
	var pwHash any
	if body.Password != "" {
		if !validatePassword(body.Password) {
			httpx.Error(c, http.StatusBadRequest, "invalid_request", "password must be 6–72 characters")
			return
		}
		hash, err := auth.HashPassword(body.Password)
		if err != nil {
			h.log.Error("create user: hash password", zap.Error(err))
			httpx.Error(c, http.StatusInternalServerError, "internal_error", "create failed")
			return
		}
		pwHash = hash
	}

	claims := middleware.ClaimsFrom(c)
	ctx := c.Request.Context()

	tx, err := h.pool.Begin(ctx)
	if err != nil {
		h.log.Error("create user: begin tx", zap.Error(err))
		httpx.Error(c, http.StatusInternalServerError, "internal_error", "create failed")
		return
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	roleIDs, ok, err := h.resolveRoleIDs(ctx, tx, body.Roles)
	if err != nil {
		h.log.Error("create user: resolve roles", zap.Error(err))
		httpx.Error(c, http.StatusInternalServerError, "internal_error", "create failed")
		return
	}
	if !ok {
		httpx.Error(c, http.StatusBadRequest, "invalid_role", "one or more role codes are invalid")
		return
	}

	var newID int64
	err = tx.QueryRow(ctx, `
		INSERT INTO users (username, display_name, email, phone, status, password_hash)
		VALUES ($1, $2, $3, $4, 'active', $5)
		RETURNING id
	`, username, displayName, nilIfEmpty(body.Email), nilIfEmpty(body.Phone), pwHash).Scan(&newID)
	if isDuplicateKeyHandler(err) {
		httpx.Error(c, http.StatusConflict, "username_taken", "username is already in use")
		return
	}
	if err != nil {
		h.log.Error("create user: insert", zap.Error(err))
		httpx.Error(c, http.StatusInternalServerError, "internal_error", "create failed")
		return
	}

	for _, rid := range roleIDs {
		if _, err := tx.Exec(ctx, `INSERT INTO user_roles (user_id, role_id) VALUES ($1, $2)`, newID, rid); err != nil {
			h.log.Error("create user: insert role", zap.Error(err))
			httpx.Error(c, http.StatusInternalServerError, "internal_error", "create failed")
			return
		}
	}

	_, _ = tx.Exec(ctx, `
		INSERT INTO audit_logs (actor_type, actor_id, action, entity_type, entity_id)
		VALUES ('user', $1, 'user_created', 'user', $2)
	`, strconv.FormatInt(claims.UserID, 10), strconv.FormatInt(newID, 10))

	if err := tx.Commit(ctx); err != nil {
		h.log.Error("create user: commit", zap.Error(err))
		httpx.Error(c, http.StatusInternalServerError, "internal_error", "create failed")
		return
	}

	httpx.OK(c, http.StatusCreated, gin.H{
		"id": strconv.FormatInt(newID, 10), "username": username,
		"display_name": displayName, "status": "active", "roles": body.Roles,
	})
}

type updateUserBody struct {
	DisplayName string   `json:"display_name"`
	Email       string   `json:"email"`
	Phone       string   `json:"phone"`
	Status      string   `json:"status"`
	Roles       []string `json:"roles"`
	Password    string   `json:"password"` // optional reset
}

// Update godoc: PUT /admin/users/:id — Requires system_admin.
// Replaces display_name/email/phone/status/roles (+ optional password reset).
// Guards against self-lockout: a system_admin cannot deactivate or de-admin themselves.
func (h *UserHandler) Update(c *gin.Context) {
	userID, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		httpx.Error(c, http.StatusBadRequest, "invalid_id", "user id must be an integer")
		return
	}
	var body updateUserBody
	if err := c.ShouldBindJSON(&body); err != nil {
		httpx.Error(c, http.StatusBadRequest, "invalid_request", "invalid JSON body")
		return
	}
	displayName := strings.TrimSpace(body.DisplayName)
	if displayName == "" || len(displayName) > 200 {
		httpx.Error(c, http.StatusBadRequest, "invalid_request", "display_name is required (≤200 chars)")
		return
	}
	if body.Status != "active" && body.Status != "inactive" {
		httpx.Error(c, http.StatusBadRequest, "invalid_request", "status must be active or inactive")
		return
	}

	// Self-lockout guard: editing your own account, you must stay active + system_admin.
	claims := middleware.ClaimsFrom(c)
	if userID == claims.UserID {
		hasSysAdmin := false
		for _, r := range body.Roles {
			if r == "system_admin" {
				hasSysAdmin = true
				break
			}
		}
		if body.Status == "inactive" || !hasSysAdmin {
			httpx.Error(c, http.StatusConflict, "cannot_demote_self",
				"you cannot deactivate or remove system_admin from your own account")
			return
		}
	}

	var pwHash any
	if body.Password != "" {
		if !validatePassword(body.Password) {
			httpx.Error(c, http.StatusBadRequest, "invalid_request", "password must be 6–72 characters")
			return
		}
		hash, err := auth.HashPassword(body.Password)
		if err != nil {
			h.log.Error("update user: hash password", zap.Error(err))
			httpx.Error(c, http.StatusInternalServerError, "internal_error", "update failed")
			return
		}
		pwHash = hash
	}

	ctx := c.Request.Context()
	tx, err := h.pool.Begin(ctx)
	if err != nil {
		h.log.Error("update user: begin tx", zap.Error(err))
		httpx.Error(c, http.StatusInternalServerError, "internal_error", "update failed")
		return
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	var exists int
	err = tx.QueryRow(ctx, `SELECT 1 FROM users WHERE id=$1 FOR UPDATE`, userID).Scan(&exists)
	if errors.Is(err, pgx.ErrNoRows) {
		httpx.Error(c, http.StatusNotFound, "not_found", "user not found")
		return
	}
	if err != nil {
		h.log.Error("update user: find", zap.Error(err))
		httpx.Error(c, http.StatusInternalServerError, "internal_error", "update failed")
		return
	}

	roleIDs, ok, err := h.resolveRoleIDs(ctx, tx, body.Roles)
	if err != nil {
		h.log.Error("update user: resolve roles", zap.Error(err))
		httpx.Error(c, http.StatusInternalServerError, "internal_error", "update failed")
		return
	}
	if !ok {
		httpx.Error(c, http.StatusBadRequest, "invalid_role", "one or more role codes are invalid")
		return
	}

	// Update core fields. Password only when provided (COALESCE keeps the old hash).
	if _, err := tx.Exec(ctx, `
		UPDATE users
		   SET display_name=$1, email=$2, phone=$3, status=$4,
		       password_hash=COALESCE($5, password_hash), updated_at=now()
		 WHERE id=$6
	`, displayName, nilIfEmpty(body.Email), nilIfEmpty(body.Phone), body.Status, pwHash, userID); err != nil {
		h.log.Error("update user: update", zap.Error(err))
		httpx.Error(c, http.StatusInternalServerError, "internal_error", "update failed")
		return
	}

	// Replace-all roles.
	if _, err := tx.Exec(ctx, `DELETE FROM user_roles WHERE user_id=$1`, userID); err != nil {
		h.log.Error("update user: clear roles", zap.Error(err))
		httpx.Error(c, http.StatusInternalServerError, "internal_error", "update failed")
		return
	}
	for _, rid := range roleIDs {
		if _, err := tx.Exec(ctx, `INSERT INTO user_roles (user_id, role_id) VALUES ($1, $2)`, userID, rid); err != nil {
			h.log.Error("update user: insert role", zap.Error(err))
			httpx.Error(c, http.StatusInternalServerError, "internal_error", "update failed")
			return
		}
	}

	_, _ = tx.Exec(ctx, `
		INSERT INTO audit_logs (actor_type, actor_id, action, entity_type, entity_id)
		VALUES ('user', $1, 'user_updated', 'user', $2)
	`, strconv.FormatInt(claims.UserID, 10), strconv.FormatInt(userID, 10))

	if err := tx.Commit(ctx); err != nil {
		h.log.Error("update user: commit", zap.Error(err))
		httpx.Error(c, http.StatusInternalServerError, "internal_error", "update failed")
		return
	}

	httpx.OK(c, http.StatusOK, gin.H{
		"id": strconv.FormatInt(userID, 10), "display_name": displayName,
		"status": body.Status, "roles": body.Roles,
	})
}
