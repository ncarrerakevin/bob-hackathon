package middleware

import (
	"bob-hackathon/internal/config"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
)

// AdminAuth middleware para proteger endpoints administrativos
func AdminAuth() gin.HandlerFunc {
	return func(c *gin.Context) {
		// Soportar dos formatos de autenticación:
		// 1. Header: X-Admin-Key: tu_key
		// 2. Header: Authorization: Bearer tu_key

		apiKey := c.GetHeader("X-Admin-Key")
		if apiKey == "" {
			// Intentar extraer de Authorization header
			auth := c.GetHeader("Authorization")
			if len(auth) > 7 && strings.HasPrefix(auth, "Bearer ") {
				apiKey = auth[7:]
			}
		}

		// Verificar que se proporcionó una key
		if apiKey == "" {
			c.JSON(http.StatusUnauthorized, gin.H{
				"error": "Missing admin API key",
				"hint":  "Add X-Admin-Key header or Authorization: Bearer <key>",
			})
			c.Abort()
			return
		}

		// Verificar que la key es correcta
		if apiKey != config.AppConfig.AdminAPIKey {
			c.JSON(http.StatusUnauthorized, gin.H{
				"error": "Invalid admin API key",
			})
			c.Abort()
			return
		}

		// Key válida, continuar
		c.Next()
	}
}
