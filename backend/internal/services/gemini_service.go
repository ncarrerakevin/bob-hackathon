package services

import (
	"bob-hackathon/internal/config"
	"bob-hackathon/internal/models"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"strings"
	"sync"
	"time"

	"github.com/google/generative-ai-go/genai"
	"google.golang.org/api/option"
)

type GeminiService struct {
	client *genai.Client
	model  *genai.GenerativeModel
	mu     sync.Mutex
}

var geminiServiceInstance *GeminiService
var geminiServiceOnce sync.Once

func GetGeminiService() *GeminiService {
	geminiServiceOnce.Do(func() {
		ctx := context.Background()
		client, err := genai.NewClient(ctx, option.WithAPIKey(config.AppConfig.GeminiAPIKey))
		if err != nil {
			log.Fatalf("Error al crear cliente Gemini: %v", err)
		}

		model := client.GenerativeModel(config.AppConfig.GeminiModel)
		model.SetTemperature(0.7)
		model.SetTopP(0.9)
		model.SetTopK(40)

		geminiServiceInstance = &GeminiService{
			client: client,
			model:  model,
		}

		log.Printf("Servicio Gemini inicializado con modelo: %s", config.AppConfig.GeminiModel)
	})
	return geminiServiceInstance
}

func (g *GeminiService) ProcessMessage(sessionID, userMessage string) (string, error) {
	g.mu.Lock()
	defer g.mu.Unlock()

	// Crear contexto con timeout de 30 segundos
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Obtener historial de conversación
	sessionService := GetSessionService()
	messages := sessionService.GetMessages(sessionID)

	// Construir contexto
	faqService := GetFAQService()
	bobAPIService := GetBOBAPIService()

	systemPrompt := g.buildSystemPrompt()
	faqContext := faqService.GetFAQsContext()
	vehiclesContext := bobAPIService.GetVehiclesContext(5)

	// Construir historial de conversación
	var conversationHistory strings.Builder
	conversationHistory.WriteString(systemPrompt)
	conversationHistory.WriteString("\n\n")
	conversationHistory.WriteString(faqContext)
	conversationHistory.WriteString("\n\n")
	conversationHistory.WriteString(vehiclesContext)
	conversationHistory.WriteString("\n\n--- Conversación ---\n\n")

	for _, msg := range messages {
		conversationHistory.WriteString(fmt.Sprintf("%s: %s\n", msg.Role, msg.Content))
	}

	conversationHistory.WriteString(fmt.Sprintf("user: %s\n", userMessage))
	conversationHistory.WriteString("assistant: ")

	// Generar respuesta
	resp, err := g.model.GenerateContent(ctx, genai.Text(conversationHistory.String()))
	if err != nil {
		return "", fmt.Errorf("error al generar respuesta: %w", err)
	}

	if len(resp.Candidates) == 0 || len(resp.Candidates[0].Content.Parts) == 0 {
		return "", fmt.Errorf("no se recibió respuesta del modelo")
	}

	reply := fmt.Sprintf("%v", resp.Candidates[0].Content.Parts[0])

	log.Printf("Respuesta generada para sesión %s", sessionID)
	return reply, nil
}

func (g *GeminiService) CalculateScore(sessionID string) (*models.ScoreResponse, error) {
	g.mu.Lock()
	defer g.mu.Unlock()

	// Crear contexto con timeout de 30 segundos
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Obtener historial de conversación
	sessionService := GetSessionService()
	messages := sessionService.GetMessages(sessionID)

	if len(messages) == 0 {
		return &models.ScoreResponse{
			Success:      true,
			Score:        0,
			Category:     "cold",
			Reasons:      []string{"Sin conversación"},
			Urgency:      "none",
			Budget:       "undefined",
			BusinessType: "unknown",
		}, nil
	}

	// Construir prompt para scoring
	var conversationText strings.Builder
	for _, msg := range messages {
		conversationText.WriteString(fmt.Sprintf("%s: %s\n", msg.Role, msg.Content))
	}

	scoringPrompt := fmt.Sprintf(`Analiza esta conversación y calcula un score de lead (0-100).

Conversación:
%s

Responde SOLO con un JSON en este formato exacto:
{
  "score": número entre 0-100,
  "category": "hot" (80-100) | "warm" (50-79) | "cold" (0-49),
  "reasons": ["razón 1", "razón 2"],
  "urgency": "high" | "medium" | "low" | "none",
  "budget": "defined" | "exploring" | "undefined",
  "businessType": "company" | "individual" | "unknown"
}

Criterios de scoring:
- Necesidad clara y urgente: +30 puntos
- Presupuesto definido: +25 puntos
- Empresa/negocio: +20 puntos
- Preguntas específicas sobre productos: +15 puntos
- Intención de compra explícita: +10 puntos
- Solo curiosidad o preguntas muy generales: -20 puntos`, conversationText.String())

	resp, err := g.model.GenerateContent(ctx, genai.Text(scoringPrompt))
	if err != nil {
		return nil, fmt.Errorf("error al calcular score: %w", err)
	}

	if len(resp.Candidates) == 0 || len(resp.Candidates[0].Content.Parts) == 0 {
		return nil, fmt.Errorf("no se recibió respuesta del modelo para scoring")
	}

	responseText := fmt.Sprintf("%v", resp.Candidates[0].Content.Parts[0])

	// Extraer JSON de la respuesta
	jsonStart := strings.Index(responseText, "{")
	jsonEnd := strings.LastIndex(responseText, "}")

	if jsonStart == -1 || jsonEnd == -1 {
		log.Printf("No se pudo parsear respuesta de scoring: %s", responseText)
		return &models.ScoreResponse{
			Success:      true,
			Score:        25,
			Category:     "cold",
			Reasons:      []string{"Error al calcular score"},
			Urgency:      "low",
			Budget:       "undefined",
			BusinessType: "unknown",
		}, nil
	}

	jsonText := responseText[jsonStart : jsonEnd+1]

	var scoreData struct {
		Score        int      `json:"score"`
		Category     string   `json:"category"`
		Reasons      []string `json:"reasons"`
		Urgency      string   `json:"urgency"`
		Budget       string   `json:"budget"`
		BusinessType string   `json:"businessType"`
	}

	if err := json.Unmarshal([]byte(jsonText), &scoreData); err != nil {
		log.Printf("Error al parsear JSON de scoring: %v", err)
		return &models.ScoreResponse{
			Success:      true,
			Score:        25,
			Category:     "cold",
			Reasons:      []string{"Error al parsear score"},
			Urgency:      "low",
			Budget:       "undefined",
			BusinessType: "unknown",
		}, nil
	}

	scoreResponse := &models.ScoreResponse{
		Success:      true,
		Score:        scoreData.Score,
		Category:     scoreData.Category,
		Reasons:      scoreData.Reasons,
		Urgency:      scoreData.Urgency,
		Budget:       scoreData.Budget,
		BusinessType: scoreData.BusinessType,
	}

	log.Printf("Score calculado para %s: %d (%s)", sessionID, scoreData.Score, scoreData.Category)
	return scoreResponse, nil
}

func (g *GeminiService) buildSystemPrompt() string {
	return `Eres un asistente virtual de BOB Subastas, una plataforma líder en Perú para subastas de vehículos e inmuebles.

PERSONALIDAD:
- Amigable, profesional y conversacional
- Breve y directo (máximo 3 líneas)
- Usa emojis ocasionalmente
- Tutea al usuario

OBJETIVO:
- Ayudar a encontrar vehículos en subasta
- Responder preguntas sobre el proceso
- Calificar el interés del lead

REGLAS:
1. Responde siempre en máximo 3 líneas
2. Si preguntan por vehículos, usa los datos disponibles
3. Si no sabes algo, revisa las FAQs
4. Invita a dar más detalles sobre necesidades
5. Sé conversacional, no uses bullet points

Ejemplo:
Usuario: "Busco un auto"
Tú: "¡Perfecto! 🚗 Tenemos varias opciones en subasta. ¿Tienes alguna marca o modelo en mente? ¿Y qué presupuesto manejas?"`
}

func (g *GeminiService) Close() {
	if g.client != nil {
		g.client.Close()
	}
}
