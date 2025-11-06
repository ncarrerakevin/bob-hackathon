package main

import (
	"bob-hackathon/internal/config"
	"bob-hackathon/internal/controllers"
	"bob-hackathon/internal/models"
	"bob-hackathon/internal/services"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/gin-contrib/cors"
	"github.com/gin-gonic/gin"
)

func main() {
	// Cargar configuración
	config.LoadConfig()

	// Inicializar servicios
	log.Println("Inicializando servicios...")
	services.GetFAQService()
	services.GetBOBAPIService()
	services.GetSessionService()
	services.GetGeminiService()

	// Crear router
	router := gin.Default()

	// Configurar CORS
	corsOrigins := strings.Split(config.AppConfig.CORSOrigins, ",")
	router.Use(cors.New(cors.Config{
		AllowOrigins:     corsOrigins,
		AllowMethods:     []string{"GET", "POST", "PUT", "DELETE", "OPTIONS"},
		AllowHeaders:     []string{"Origin", "Content-Type", "Accept", "Authorization"},
		ExposeHeaders:    []string{"Content-Length"},
		AllowCredentials: true,
		MaxAge:           12 * time.Hour,
	}))

	// Crear controllers
	chatController := controllers.NewChatController()
	leadController := controllers.NewLeadController()

	// Health check
	router.GET("/health", func(ctx *gin.Context) {
		ctx.JSON(200, models.HealthResponse{
			Status:    "ok",
			Timestamp: time.Now(),
			Service:   "BOB Chatbot API - Go Version",
		})
	})

	// Ruta raíz con documentación
	router.GET("/", func(ctx *gin.Context) {
		ctx.JSON(200, gin.H{
			"service": "BOB Chatbot API - Go Version",
			"version": "2.0.0",
			"status":  "running",
			"endpoints": gin.H{
				"health": "GET /health",
				"chat": gin.H{
					"message": "POST /api/chat/message",
					"score":   "POST /api/chat/score",
					"history": "GET /api/chat/history/:sessionId",
					"delete":  "DELETE /api/chat/session/:sessionId",
				},
				"leads": gin.H{
					"list":   "GET /api/leads",
					"get":    "GET /api/leads/:sessionId",
					"stats":  "GET /api/leads/stats",
				},
				"resources": gin.H{
					"faqs":     "GET /api/faqs",
					"vehicles": "GET /api/vehicles",
					"vehicle":  "GET /api/vehicles/:id",
				},
			},
		})
	})

	// Rutas de Chat
	chatRoutes := router.Group("/api/chat")
	{
		chatRoutes.POST("/message", chatController.SendMessage)
		chatRoutes.POST("/score", chatController.GetScore)
		chatRoutes.GET("/history/:sessionId", chatController.GetHistory)
		chatRoutes.DELETE("/session/:sessionId", chatController.DeleteSession)
	}

	// Rutas de Leads
	leadRoutes := router.Group("/api/leads")
	{
		leadRoutes.GET("", leadController.GetAllLeads)
		leadRoutes.GET("/stats", leadController.GetLeadsStats)
		leadRoutes.GET("/:sessionId", leadController.GetLead)
	}

	// Rutas de Recursos
	router.GET("/api/faqs", leadController.GetFAQs)
	router.GET("/api/vehicles", leadController.GetVehicles)
	router.GET("/api/vehicles/:id", leadController.GetVehicleByID)

	// Iniciar servidor
	port := config.AppConfig.Port
	log.Printf("Servidor corriendo en puerto %s", port)
	log.Printf("URL: http://localhost:%s", port)
	log.Printf("Health: http://localhost:%s/health", port)

	if err := router.Run(fmt.Sprintf(":%s", port)); err != nil {
		log.Fatalf("❌ Error al iniciar servidor: %v", err)
	}
}
