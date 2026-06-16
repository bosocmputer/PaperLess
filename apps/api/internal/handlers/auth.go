package handlers

import (
	"context"
	"crypto/sha256"
	"fmt"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"go.uber.org/zap"

	"paperless-api/internal/auth"
	"paperless-api/internal/httpx"
	"paperless-api/internal/middleware"
)

type AuthHandler struct {
	pool      *pgxpool.Pool
	jwtSecret string
	log       *zap.Logger
}

func NewAuthHandler(pool *pgxpool.Pool, jwtSecret string, log *zap.Logger) *AuthHandler {
	return &AuthHandler{pool: pool, jwtSecret: jwtSecret, log: log}
}

// Login godoc: POST /auth/login
func (h *AuthHandler) Login(c *gin.Context) {
	var req struct {
		Username string `json:"username" binding:"required"`
		Password string `json:"password" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		httpx.Error(c, http.StatusBadRequest, "invalid_request", "username and password are required")
		return
	}

	var (
		userID       int64
		displayName  string
		passwordHash string
		status       string
	)
	err := h.pool.QueryRow(context.Background(),
		`SELECT id, display_name, password_hash, status FROM users WHERE username = $1`, req.Username,
	).Scan(&userID, &displayName, &passwordHash, &status)
	if err == pgx.ErrNoRows || !auth.CheckPassword(req.Password, passwordHash) {
		httpx.Error(c, http.StatusUnauthorized, "invalid_credentials", "invalid username or password")
		return
	}
	if err != nil {
		h.log.Error("login db error", zap.Error(err))
		httpx.Error(c, http.StatusInternalServerError, "internal_error", "login failed")
		return
	}
	if status != "active" {
		httpx.Error(c, http.StatusForbidden, "account_inactive", "account is not active")
		return
	}

	roles, err := h.userRoles(userID)
	if err != nil {
		h.log.Error("fetch roles", zap.Error(err), zap.Int64("user_id", userID))
		httpx.Error(c, http.StatusInternalServerError, "internal_error", "login failed")
		return
	}

	accessToken, err := auth.IssueAccessToken(h.jwtSecret, userID, req.Username, roles)
	if err != nil {
		h.log.Error("issue access token", zap.Error(err))
		httpx.Error(c, http.StatusInternalServerError, "internal_error", "login failed")
		return
	}

	rawRefresh, err := auth.GenerateRefreshToken()
	if err != nil {
		h.log.Error("generate refresh token", zap.Error(err))
		httpx.Error(c, http.StatusInternalServerError, "internal_error", "login failed")
		return
	}
	refreshHash := hashToken(rawRefresh)
	expiresAt := time.Now().Add(auth.RefreshTokenTTL)
	_, err = h.pool.Exec(context.Background(),
		`INSERT INTO refresh_tokens (user_id, token_hash, expires_at) VALUES ($1, $2, $3)`,
		userID, refreshHash, expiresAt,
	)
	if err != nil {
		h.log.Error("store refresh token", zap.Error(err))
		httpx.Error(c, http.StatusInternalServerError, "internal_error", "login failed")
		return
	}

	h.log.Info("login", zap.String("username", req.Username), zap.Int64("user_id", userID))
	httpx.OK(c, http.StatusOK, gin.H{
		"access_token":  accessToken,
		"refresh_token": rawRefresh,
		"expires_in":    int(auth.AccessTokenTTL.Seconds()),
		"user": gin.H{
			"id":           userID,
			"username":     req.Username,
			"display_name": displayName,
			"roles":        roles,
		},
	})
}

// Refresh godoc: POST /auth/refresh
func (h *AuthHandler) Refresh(c *gin.Context) {
	var req struct {
		RefreshToken string `json:"refresh_token" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		httpx.Error(c, http.StatusBadRequest, "invalid_request", "refresh_token is required")
		return
	}

	tokenHash := hashToken(req.RefreshToken)
	var (
		tokenID  int64
		userID   int64
		username string
		expAt    time.Time
	)
	err := h.pool.QueryRow(context.Background(),
		`SELECT rt.id, rt.user_id, u.username, rt.expires_at
		   FROM refresh_tokens rt JOIN users u ON u.id=rt.user_id
		  WHERE rt.token_hash = $1`, tokenHash,
	).Scan(&tokenID, &userID, &username, &expAt)
	if err == pgx.ErrNoRows {
		httpx.Error(c, http.StatusUnauthorized, "invalid_token", "refresh token not found")
		return
	}
	if err != nil {
		h.log.Error("refresh db error", zap.Error(err))
		httpx.Error(c, http.StatusInternalServerError, "internal_error", "refresh failed")
		return
	}
	if time.Now().After(expAt) {
		_, _ = h.pool.Exec(context.Background(), `DELETE FROM refresh_tokens WHERE id=$1`, tokenID)
		httpx.Error(c, http.StatusUnauthorized, "token_expired", "refresh token has expired")
		return
	}

	roles, err := h.userRoles(userID)
	if err != nil {
		httpx.Error(c, http.StatusInternalServerError, "internal_error", "refresh failed")
		return
	}

	accessToken, err := auth.IssueAccessToken(h.jwtSecret, userID, username, roles)
	if err != nil {
		httpx.Error(c, http.StatusInternalServerError, "internal_error", "refresh failed")
		return
	}

	httpx.OK(c, http.StatusOK, gin.H{
		"access_token": accessToken,
		"expires_in":   int(auth.AccessTokenTTL.Seconds()),
	})
}

// Logout godoc: POST /auth/logout
func (h *AuthHandler) Logout(c *gin.Context) {
	var req struct {
		RefreshToken string `json:"refresh_token"`
	}
	_ = c.ShouldBindJSON(&req)
	if req.RefreshToken != "" {
		_, _ = h.pool.Exec(context.Background(),
			`DELETE FROM refresh_tokens WHERE token_hash=$1`, hashToken(req.RefreshToken))
	}
	httpx.OK(c, http.StatusOK, gin.H{"message": "logged out"})
}

// Me godoc: GET /auth/me
func (h *AuthHandler) Me(c *gin.Context) {
	claims := middleware.ClaimsFrom(c)
	if claims == nil {
		httpx.Error(c, http.StatusUnauthorized, "unauthorized", "not authenticated")
		return
	}

	var displayName string
	_ = h.pool.QueryRow(context.Background(),
		`SELECT display_name FROM users WHERE id=$1`, claims.UserID,
	).Scan(&displayName)

	httpx.OK(c, http.StatusOK, gin.H{
		"id":           claims.UserID,
		"username":     claims.Username,
		"display_name": displayName,
		"roles":        claims.Roles,
	})
}

func (h *AuthHandler) userRoles(userID int64) ([]string, error) {
	rows, err := h.pool.Query(context.Background(),
		`SELECT r.code FROM roles r
		   JOIN user_roles ur ON ur.role_id=r.id
		  WHERE ur.user_id=$1`, userID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var roles []string
	for rows.Next() {
		var code string
		if err := rows.Scan(&code); err != nil {
			return nil, err
		}
		roles = append(roles, code)
	}
	return roles, rows.Err()
}

func hashToken(raw string) string {
	sum := sha256.Sum256([]byte(raw))
	return fmt.Sprintf("%x", sum)
}
