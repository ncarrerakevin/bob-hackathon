package controllers

import (
	"bob-hackathon/internal/agents"
	"bob-hackathon/internal/models"
	"bob-hackathon/internal/services"
	"bob-hackathon/internal/utils"
	"context"
	"log"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
)

type ChatController struct {
	orchestrator   agents.Agent
	faqAgent       agents.Agent
	auctionAgent   agents.Agent
	scoringAgent   agents.Agent
	sessionService *services.SessionService
}

func NewChatController() *ChatController {
	orchestrator, err := agents.NewOrchestratorAgent()
	if err != nil {
		log.Fatalf("❌ Error creando OrchestratorAgent: %v", err)
	}

	faqAgent, err := agents.NewFAQAgent()
	if err != nil {
		log.Fatalf("❌ Error creando FAQAgent: %v", err)
	}

	auctionAgent, err := agents.NewAuctionAgent()
	if err != nil {
		log.Fatalf("❌ Error creando AuctionAgent: %v", err)
	}

	scoringAgent, err := agents.NewScoringAgent()
	if err != nil {
		log.Fatalf("❌ Error creando ScoringAgent: %v", err)
	}

	return &ChatController{
		orchestrator:   orchestrator,
		faqAgent:       faqAgent,
		auctionAgent:   auctionAgent,
		scoringAgent:   scoringAgent,
		sessionService: services.GetSessionService(),
	}
}

func (c *ChatController) SendMessage(ctx *gin.Context) {
	var req models.ChatRequest
	if err := ctx.ShouldBindJSON(&req); err != nil {
		ctx.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"error":   "Datos inválidos: " + err.Error(),
		})
		return
	}

	// VALIDACIÓN Y SANITIZACIÓN DE INPUTS
	// 1. Validar y sanitizar mensaje
	sanitizedMessage, err := utils.ValidateAndSanitizeMessage(req.Message)
	if err != nil {
		ctx.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"error":   err.Error(),
		})
		return
	}
	req.Message = sanitizedMessage

	// 2. Validar sessionID
	if err := utils.ValidateSessionID(req.SessionID); err != nil {
		ctx.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"error":   err.Error(),
		})
		return
	}

	// 3. Validar channel
	if err := utils.ValidateChannel(req.Channel); err != nil {
		ctx.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"error":   err.Error(),
		})
		return
	}

	// Obtener o crear sesión
	session := c.sessionService.GetOrCreateSession(req.SessionID, req.Channel)

	// Agregar mensaje del usuario
	c.sessionService.AddMessage(session.SessionID, "user", req.Message)

	// FASE 1: ORCHESTRATOR - Analiza intención y rutea
	agentInput := &agents.AgentInput{
		Message:             req.Message,
		SessionID:           session.SessionID,
		Channel:             req.Channel,
		ConversationHistory: session.Messages,
	}

	orchestratorOutput, err := c.orchestrator.Process(context.Background(), agentInput)
	if err != nil {
		log.Printf("❌ Error en Orchestrator: %v", err)
		ctx.JSON(http.StatusInternalServerError, gin.H{
			"success": false,
			"error":   "Error procesando mensaje: " + err.Error(),
		})
		return
	}

	var finalReply string

	// FASE 2: ROUTING - Según decisión del orchestrator
	if orchestratorOutput.ShouldRoute {
		var subAgentOutput *agents.AgentOutput

		switch orchestratorOutput.RouteTo {
		case "faq_agent":
			log.Printf("🔀 Ruteando a FAQ Agent")
			subAgentOutput, err = c.faqAgent.Process(context.Background(), agentInput)
		case "auction_agent":
			log.Printf("🔀 Ruteando a Auction Agent")
			subAgentOutput, err = c.auctionAgent.Process(context.Background(), agentInput)
		default:
			log.Printf("⚠️ RouteTo desconocido: %s, usando respuesta del orchestrator", orchestratorOutput.RouteTo)
			finalReply = orchestratorOutput.Response
		}

		if err != nil {
			log.Printf("❌ Error en SubAgent: %v", err)
			finalReply = orchestratorOutput.Response // Fallback a respuesta del orchestrator
		} else if subAgentOutput != nil {
			finalReply = subAgentOutput.Response
		}
	} else {
		// El orchestrator maneja directamente (general, spam, ambiguo)
		finalReply = orchestratorOutput.Response
	}

	// Agregar respuesta del asistente
	c.sessionService.AddMessage(session.SessionID, "assistant", finalReply)

	// FASE 3: SCORING - Calcular después de 3+ mensajes
	var leadScore int
	var category string

	if len(session.Messages) >= 6 { // 3 pares user-assistant mínimo
		log.Printf("📊 Calculando scoring con %d mensajes", len(session.Messages))

		scoringOutput, err := c.scoringAgent.Process(context.Background(), agentInput)
		if err != nil {
			log.Printf("⚠️ Error en ScoringAgent: %v", err)
			leadScore = 0
			category = "cold"
		} else if scoringOutput.ScoringData != nil {
			rawScore := scoringOutput.ScoringData.TotalScore

			// Aplicar smoothing temporal para evitar saltos bruscos
			existingLead := c.sessionService.GetLead(session.SessionID)
			if existingLead != nil && existingLead.Score > 0 {
				// Smoothing: 70% score previo + 30% score nuevo
				prevScore := existingLead.Score
				leadScore = int(float64(prevScore)*0.7 + float64(rawScore)*0.3)
				log.Printf("📈 Smoothing aplicado: %d (prev) → %d (raw) → %d (final)", prevScore, rawScore, leadScore)
			} else {
				leadScore = rawScore
			}

			// Recalcular categoría basada en score con smoothing
			if leadScore >= 85 {
				category = "hot"
			} else if leadScore >= 65 {
				category = "warm"
			} else if leadScore >= 45 {
				category = "cold"
			} else {
				category = "discarded"
			}

			// Actualizar lead con scoring detallado
			lead := &models.Lead{
				SessionID:    session.SessionID,
				Channel:      session.Channel,
				Score:        leadScore,
				Category:     category,
				LastMessage:  req.Message,
				CreatedAt:    session.CreatedAt,
				UpdatedAt:    time.Now(),
			}
			c.sessionService.CreateOrUpdateLead(lead)

			log.Printf("✅ Score calculado: %d/100 - Categoría: %s", leadScore, category)
		}
	} else {
		// Score provisional para conversaciones cortas
		leadScore = session.LeadScore
		category = session.Category
		if category == "" {
			category = "cold"
		}
	}

	// Actualizar score en sesión
	c.sessionService.UpdateScore(session.SessionID, leadScore, category)

	// Responder
	response := models.ChatResponse{
		Success:   true,
		SessionID: session.SessionID,
		Reply:     finalReply,
		LeadScore: leadScore,
		Category:  category,
		Timestamp: time.Now(),
	}

	ctx.JSON(http.StatusOK, response)
}

func (c *ChatController) GetScore(ctx *gin.Context) {
	var req models.ScoreRequest
	if err := ctx.ShouldBindJSON(&req); err != nil {
		ctx.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"error":   "Datos inválidos: " + err.Error(),
		})
		return
	}

	// Validar sessionID
	if err := utils.ValidateSessionID(req.SessionID); err != nil {
		ctx.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"error":   err.Error(),
		})
		return
	}

	// Obtener sesión
	session := c.sessionService.GetSession(req.SessionID)
	if session == nil {
		ctx.JSON(http.StatusNotFound, gin.H{
			"success": false,
			"error":   "Sesión no encontrada",
		})
		return
	}

	// Usar ScoringAgent para calcular score detallado
	agentInput := &agents.AgentInput{
		Message:             "Calcular scoring completo",
		SessionID:           session.SessionID,
		Channel:             session.Channel,
		ConversationHistory: session.Messages,
	}

	scoringOutput, err := c.scoringAgent.Process(context.Background(), agentInput)
	if err != nil {
		ctx.JSON(http.StatusInternalServerError, gin.H{
			"success": false,
			"error":   "Error al calcular score: " + err.Error(),
		})
		return
	}

	if scoringOutput.ScoringData == nil {
		ctx.JSON(http.StatusInternalServerError, gin.H{
			"success": false,
			"error":   "No se pudo generar scoring",
		})
		return
	}

	// Construir respuesta en formato compatible
	scoreResponse := models.ScoreResponse{
		Success: true,
		Score:   scoringOutput.ScoringData.TotalScore,
		Category: scoringOutput.ScoringData.Category,
		Reasons: []string{
			scoringOutput.ScoringData.AccionRecomendada,
			"Tiempo contacto: " + scoringOutput.ScoringData.TiempoContacto,
			"Seguimiento: " + scoringOutput.ScoringData.TipoSeguimiento,
		},
	}

	// Agregar boosts y penalizaciones a reasons
	if len(scoringOutput.ScoringData.Boosts) > 0 {
		for _, boost := range scoringOutput.ScoringData.Boosts {
			scoreResponse.Reasons = append(scoreResponse.Reasons, "✅ "+boost)
		}
	}

	if len(scoringOutput.ScoringData.Penalizaciones) > 0 {
		for _, penalty := range scoringOutput.ScoringData.Penalizaciones {
			scoreResponse.Reasons = append(scoreResponse.Reasons, "⚠️ "+penalty)
		}
	}

	ctx.JSON(http.StatusOK, scoreResponse)
}

func (c *ChatController) GetHistory(ctx *gin.Context) {
	sessionID := ctx.Param("sessionId")

	// Validar sessionID
	if err := utils.ValidateSessionID(sessionID); err != nil {
		ctx.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"error":   err.Error(),
		})
		return
	}

	session := c.sessionService.GetSession(sessionID)
	if session == nil {
		ctx.JSON(http.StatusNotFound, gin.H{
			"success": false,
			"error":   "Sesión no encontrada",
		})
		return
	}

	ctx.JSON(http.StatusOK, gin.H{
		"success":  true,
		"session":  session,
		"messages": session.Messages,
	})
}

func (c *ChatController) DeleteSession(ctx *gin.Context) {
	sessionID := ctx.Param("sessionId")

	// Validar sessionID
	if err := utils.ValidateSessionID(sessionID); err != nil {
		ctx.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"error":   err.Error(),
		})
		return
	}

	// Por ahora solo retornamos éxito
	// Se puede implementar eliminación si es necesario
	ctx.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "Sesión eliminada (funcionalidad pendiente)",
	})
}
