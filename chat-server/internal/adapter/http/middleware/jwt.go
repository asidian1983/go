package middleware

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/asidian1983/chat-server/internal/infrastructure/auth"
)

// UserIDKey is the gin.Context key set by the JWT middleware.
const UserIDKey = "userID"

// JWT returns a Gin middleware that validates Bearer JWTs.
//
// Token lookup order:
//  1. Authorization: Bearer <token> header  (API clients, wscat)
//  2. ?token=<jwt> query parameter          (browser WebSocket — JS cannot set headers)
//
// On success: sets UserIDKey in gin.Context and calls Next().
// On failure: aborts with 401 — no information about WHY the token failed is leaked.
func JWT(svc *auth.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		tokenStr := extractToken(c)
		if tokenStr == "" {
			c.AbortWithStatusJSON(http.StatusUnauthorized,
				gin.H{"error": "missing token"})
			return
		}

		claims, err := svc.Validate(tokenStr)
		if err != nil {
			c.AbortWithStatusJSON(http.StatusUnauthorized,
				gin.H{"error": "invalid or expired token"})
			return
		}

		c.Set(UserIDKey, claims.Subject) // Subject == userID
		c.Next()
	}
}

func extractToken(c *gin.Context) string {
	if h := c.GetHeader("Authorization"); strings.HasPrefix(h, "Bearer ") {
		return strings.TrimPrefix(h, "Bearer ")
	}
	return c.Query("token")
}
