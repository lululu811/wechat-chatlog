package http

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/sjzar/chatlog/internal/chatlog/database"
)

func corsMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Writer.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
		c.Writer.Header().Set("Access-Control-Allow-Headers", "Accept, Authorization, Content-Type, X-CSRF-Token")

		if c.Request.Method == "OPTIONS" {
			c.AbortWithStatus(204)
			return
		}

		c.Next()
	}
}

// authMiddleware 校验 auth token，支持 Authorization: Bearer <token> 或 ?token= 查询参数；
// 未配置 token 时直接放行
func (s *Service) authMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		token := s.conf.GetAuthToken()
		if token == "" {
			c.Next()
			return
		}

		if c.Query("token") == token {
			c.Next()
			return
		}

		if h := c.GetHeader("Authorization"); strings.HasPrefix(h, "Bearer ") && strings.TrimPrefix(h, "Bearer ") == token {
			c.Next()
			return
		}

		c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
	}
}

func (s *Service) checkDBStateMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		state, stateMsg := s.db.GetState()
		switch state {
		case database.StateInit:
			c.JSON(http.StatusServiceUnavailable, gin.H{"error": "database is not ready"})
			c.Abort()
			return
		case database.StateDecrypting:
			c.JSON(http.StatusServiceUnavailable, gin.H{"error": "database is decrypting, please wait"})
			c.Abort()
			return
		case database.StateError:
			c.JSON(http.StatusServiceUnavailable, gin.H{"error": "database is error: " + stateMsg})
			c.Abort()
			return
		}

		c.Next()
	}
}
