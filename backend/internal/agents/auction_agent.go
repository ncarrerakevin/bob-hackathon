package agents

import (
	"bob-hackathon/internal/config"
	"bob-hackathon/internal/services"
	"context"
	"fmt"
	"strings"

	"github.com/google/generative-ai-go/genai"
	"google.golang.org/api/option"
)

type AuctionAgent struct {
	client         *genai.Client
	model          *genai.GenerativeModel
	bobAPIService  *services.BOBAPIService
}

func NewAuctionAgent() (*AuctionAgent, error) {
	ctx := context.Background()
	client, err := genai.NewClient(ctx, option.WithAPIKey(config.AppConfig.GeminiAPIKey))
	if err != nil {
		return nil, err
	}

	return &AuctionAgent{
		client:        client,
		model:         client.GenerativeModel(config.AppConfig.GeminiModel),
		bobAPIService: services.GetBOBAPIService(),
	}, nil
}

func (a *AuctionAgent) Name() string {
	return "Auction_Agent"
}

func (a *AuctionAgent) Process(ctx context.Context, input *AgentInput) (*AgentOutput, error) {
	vehicles, err := a.bobAPIService.GetSublots(false)
	if err != nil {
		// Si hay error en la API, retornar respuesta de fallback pero sin error
		// para que el sistema siga funcionando
		return &AgentOutput{
			Response: "En este momento estoy teniendo dificultades para consultar el inventario de vehículos. Te puedo ayudar con información general sobre nuestro proceso de subastas. ¿Tienes alguna pregunta específica sobre cómo funciona?",
		}, nil
	}

	// Limitar a 10 vehículos
	if len(vehicles) > 10 {
		vehicles = vehicles[:10]
	}

	prompt := a.buildPrompt(input, vehicles)

	resp, err := a.model.GenerateContent(ctx, genai.Text(prompt))
	if err != nil {
		return nil, err
	}

	if len(resp.Candidates) == 0 || len(resp.Candidates[0].Content.Parts) == 0 {
		return nil, fmt.Errorf("no response from model")
	}

	responseText := fmt.Sprintf("%v", resp.Candidates[0].Content.Parts[0])

	return &AgentOutput{
		Response: strings.TrimSpace(responseText),
	}, nil
}

func (a *AuctionAgent) buildPrompt(input *AgentInput, vehicles interface{}) string {
	return fmt.Sprintf(`Eres el Agente de Subastas de BOB. Tu especialidad es ayudar a encontrar vehículos en subasta.

MENSAJE DEL USUARIO: "%s"

VEHÍCULOS DISPONIBLES:
%v

INSTRUCCIONES:
1. Analiza qué tipo de vehículo busca el usuario (marca, modelo, año, tipo)
2. Recomienda vehículos que coincidan con sus necesidades
3. Menciona precios iniciales y estado
4. Sé específico con los detalles de cada vehículo
5. Si no hay coincidencias exactas, sugiere alternativas similares
6. Invita a ver más en https://www.somosbob.com/subastas
7. Pregunta sobre presupuesto, urgencia y uso previsto para afinarlo scoring

Responde de manera útil y orientada a cerrar la venta.`, input.Message, vehicles)
}
