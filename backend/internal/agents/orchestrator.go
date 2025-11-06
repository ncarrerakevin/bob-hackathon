package agents

import (
	"bob-hackathon/internal/config"
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/google/generative-ai-go/genai"
	"google.golang.org/api/option"
)

type OrchestratorAgent struct {
	client *genai.Client
	model  *genai.GenerativeModel
}

func NewOrchestratorAgent() (*OrchestratorAgent, error) {
	ctx := context.Background()
	client, err := genai.NewClient(ctx, option.WithAPIKey(config.AppConfig.GeminiAPIKey))
	if err != nil {
		return nil, err
	}

	return &OrchestratorAgent{
		client: client,
		model:  client.GenerativeModel(config.AppConfig.GeminiModel),
	}, nil
}

func (o *OrchestratorAgent) Name() string {
	return "Orchestrator"
}

func (o *OrchestratorAgent) Process(ctx context.Context, input *AgentInput) (*AgentOutput, error) {
	prompt := o.buildPrompt(input)

	resp, err := o.model.GenerateContent(ctx, genai.Text(prompt))
	if err != nil {
		return nil, err
	}

	if len(resp.Candidates) == 0 || len(resp.Candidates[0].Content.Parts) == 0 {
		return nil, fmt.Errorf("no response from model")
	}

	responseText := fmt.Sprintf("%v", resp.Candidates[0].Content.Parts[0])

	decision := o.parseDecision(responseText)

	return decision, nil
}

func (o *OrchestratorAgent) buildPrompt(input *AgentInput) string {
	historyText := ""
	if len(input.ConversationHistory) > 0 {
		historyText = "\n\nHISTORIAL DE CONVERSACIÓN:\n"
		for _, msg := range input.ConversationHistory {
			historyText += fmt.Sprintf("%s: %s\n", msg.Role, msg.Content)
		}
	}

	return fmt.Sprintf(`Eres el Agente Orquestador de BOB Subastas. Tu tarea es analizar el mensaje del usuario y decidir cómo manejarlo.

MENSAJE DEL USUARIO: "%s"
CANAL: %s%s

ANÁLISIS REQUERIDO:

1. DETECCIÓN DE SPAM/AMBIGÜEDAD:
   - ¿Es spam? (publicidad, mensajes sin sentido, trolling)
   - ¿Es ambiguo? (no se entiende la intención)
   - ¿Es un saludo inicial? (primera interacción)

2. CLASIFICACIÓN DE INTENCIÓN:
   - FAQ: Preguntas sobre cómo funciona BOB, proceso de subasta, pagos, etc.
   - SUBASTA: Búsqueda de vehículos, preguntas sobre subastas específicas
   - GENERAL: Conversación general, necesita respuesta del orquestador
   - SPAM: Mensaje no válido
   - AMBIGUO: No está clara la intención

3. ROUTING:
   - Si es FAQ → ruta a "faq_agent"
   - Si es SUBASTA → ruta a "auction_agent"
   - Si es GENERAL → responde tú mismo
   - Si es SPAM → responde mensaje educado de rechazo
   - Si es AMBIGUO → pide clarificación

FORMATO DE RESPUESTA (JSON):
{
  "intent": "faq|auction|general|spam|ambiguous",
  "confidence": 0.0-1.0,
  "shouldRoute": true/false,
  "routeTo": "faq_agent|auction_agent|null",
  "response": "tu respuesta si no se rutea",
  "reasoning": "breve explicación de tu decisión"
}

IMPORTANTE:
- Sé conciso y directo
- Si detectas spam, sé educado pero firme
- Si es ambiguo, pide específicamente qué necesita
- Si es saludo inicial, da bienvenida cálida y explica cómo puedes ayudar

Responde SOLO con el JSON, sin texto adicional.`, input.Message, input.Channel, historyText)
}

type OrchestratorDecision struct {
	Intent      string  `json:"intent"`
	Confidence  float64 `json:"confidence"`
	ShouldRoute bool    `json:"shouldRoute"`
	RouteTo     string  `json:"routeTo"`
	Response    string  `json:"response"`
	Reasoning   string  `json:"reasoning"`
}

func (o *OrchestratorAgent) parseDecision(responseText string) *AgentOutput {
	responseText = strings.TrimSpace(responseText)

	start := strings.Index(responseText, "{")
	end := strings.LastIndex(responseText, "}")

	if start == -1 || end == -1 {
		return &AgentOutput{
			Response:       "Lo siento, hubo un error procesando tu mensaje. ¿Podrías reformularlo?",
			ShouldRoute:    false,
			IntentDetected: string(IntentAmbiguo),
			Confidence:     0.0,
		}
	}

	jsonStr := responseText[start : end+1]

	var decision OrchestratorDecision
	if err := json.Unmarshal([]byte(jsonStr), &decision); err != nil {
		return &AgentOutput{
			Response:       "Lo siento, hubo un error procesando tu mensaje. ¿Podrías reformularlo?",
			ShouldRoute:    false,
			IntentDetected: string(IntentAmbiguo),
			Confidence:     0.0,
		}
	}

	return &AgentOutput{
		Response:       decision.Response,
		ShouldRoute:    decision.ShouldRoute,
		RouteTo:        decision.RouteTo,
		IntentDetected: decision.Intent,
		Confidence:     decision.Confidence,
	}
}
