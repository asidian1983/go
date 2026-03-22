package http

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

	"github.com/asidian1983/chat-server/internal/infrastructure/auth"
)

// AuthHandler handles credential-based authentication.
type AuthHandler struct {
	users *auth.UserStore
	jwt   *auth.Service
	log   *zap.Logger
}

// NewAuthHandler constructs an AuthHandler.
func NewAuthHandler(users *auth.UserStore, jwt *auth.Service, log *zap.Logger) *AuthHandler {
	return &AuthHandler{users: users, jwt: jwt, log: log}
}

type loginRequest struct {
	Username string `json:"username" binding:"required"`
	Password string `json:"password" binding:"required"`
}

type loginResponse struct {
	Token string `json:"token"`
}

// Login validates credentials and returns a signed JWT.
//
// POST /auth/login
// Body: {"username":"alice","password":"secret"}
// Response 200: {"token":"<jwt>"}
// Response 400: missing fields
// Response 401: wrong credentials (same message regardless of whether user exists)
func (h *AuthHandler) Login(c *gin.Context) {
	var req loginRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "username and password are required"})
		return
	}

	user, err := h.users.Authenticate(req.Username, req.Password)
	if err != nil {
		// Unified error message — never reveal whether the username exists.
		c.JSON(http.StatusUnauthorized, gin.H{"error": "invalid credentials"})
		return
	}

	token, err := h.jwt.Generate(user.ID, user.Username)
	if err != nil {
		h.log.Error("jwt generation failed", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "could not issue token"})
		return
	}

	h.log.Info("user authenticated", zap.String("userID", user.ID))
	c.JSON(http.StatusOK, loginResponse{Token: token})
}
