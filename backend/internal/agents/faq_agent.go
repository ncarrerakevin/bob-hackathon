package agents

import (
	"bob-hackathon/internal/config"
	"bob-hackathon/internal/models"
	"bob-hackathon/internal/services"
	"context"
	"fmt"
	"strings"

	"github.com/google/generative-ai-go/genai"
	"google.golang.org/api/option"
)

type FAQAgent struct {
	client     *genai.Client
	model      *genai.GenerativeModel
	faqService *services.FAQService
}

func NewFAQAgent() (*FAQAgent, error) {
	ctx := context.Background()
	client, err := genai.NewClient(ctx, option.WithAPIKey(config.AppConfig.GeminiAPIKey))
	if err != nil {
		return nil, err
	}

	return &FAQAgent{
		client:     client,
		model:      client.GenerativeModel(config.AppConfig.GeminiModel),
		faqService: services.GetFAQService(),
	}, nil
}

func (f *FAQAgent) Name() string {
	return "FAQ_Agent"
}

func (f *FAQAgent) Process(ctx context.Context, input *AgentInput) (*AgentOutput, error) {
	faqs := f.faqService.SearchFAQs(input.Message, "", "")

	if len(faqs) == 0 {
		return &AgentOutput{
			Response: "No encontré información específica sobre eso en nuestras FAQs. ¿Podrías reformular tu pregunta o ser más específico?",
		}, nil
	}

	prompt := f.buildPrompt(input, faqs)

	resp, err := f.model.GenerateContent(ctx, genai.Text(prompt))
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

func (f *FAQAgent) buildPrompt(input *AgentInput, faqs []models.FAQ) string {
	faqContext := "\n\nFAQs RELEVANTES:\n"
	for i, faq := range faqs {
		if i >= 5 {
			break
		}
		faqContext += fmt.Sprintf("\nP: %s\nR: %s\n", faq.Pregunta, faq.Respuesta)
	}

	return fmt.Sprintf(`Eres el Agente de FAQ de BOB Subastas. Tu especialidad es responder preguntas frecuentes.

PREGUNTA DEL USUARIO: "%s"
%s

INSTRUCCIONES:
1. Responde la pregunta usando la información de las FAQs
2. Si hay múltiples FAQs relevantes, combina la información de manera coherente
3. Sé conciso pero completo
4. Usa un tono amigable y profesional
5. Si la información no está en las FAQs, reconócelo y ofrece ayuda alternativa
6. NO inventes información que no esté en las FAQs
7. Incluye enlaces relevantes si están en las FAQs

Responde de manera directa y útil.`, input.Message, faqContext)
}
