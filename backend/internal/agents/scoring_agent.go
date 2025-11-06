package agents

import (
	"bob-hackathon/internal/config"
	"bob-hackathon/internal/models"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"strings"

	"github.com/google/generative-ai-go/genai"
	"google.golang.org/api/option"
)

type ScoringAgent struct {
	client *genai.Client
	model  *genai.GenerativeModel
}

func NewScoringAgent() (*ScoringAgent, error) {
	ctx := context.Background()
	client, err := genai.NewClient(ctx, option.WithAPIKey(config.AppConfig.GeminiAPIKey))
	if err != nil {
		return nil, err
	}

	return &ScoringAgent{
		client: client,
		model:  client.GenerativeModel(config.AppConfig.GeminiModel),
	}, nil
}

func (s *ScoringAgent) Name() string {
	return "Scoring_Agent"
}

func (s *ScoringAgent) Process(ctx context.Context, input *AgentInput) (*AgentOutput, error) {
	prompt := s.buildPrompt(input)

	resp, err := s.model.GenerateContent(ctx, genai.Text(prompt))
	if err != nil {
		return nil, err
	}

	if len(resp.Candidates) == 0 || len(resp.Candidates[0].Content.Parts) == 0 {
		return nil, fmt.Errorf("no response from model")
	}

	responseText := fmt.Sprintf("%v", resp.Candidates[0].Content.Parts[0])

	scoringData := s.parseScoring(responseText)

	return &AgentOutput{
		Response:    s.generateScoringMessage(scoringData),
		ScoringData: scoringData,
		ShouldRoute: false,
	}, nil
}

func (s *ScoringAgent) buildPrompt(input *AgentInput) string {
	historyText := ""
	if len(input.ConversationHistory) > 0 {
		historyText = "\n\nHISTORIAL COMPLETO DE CONVERSACIÓN:\n"
		for i, msg := range input.ConversationHistory {
			historyText += fmt.Sprintf("[Mensaje %d] %s: %s\n", i+1, msg.Role, msg.Content)
		}
	}

	return fmt.Sprintf(`Eres el Agente de Scoring de BOB Subastas. Tu tarea es analizar la conversación completa y calcular un score preciso de 0-100 puntos basado en 7 dimensiones oficiales.

CONVERSACIÓN A ANALIZAR:
SessionID: %s
Canal: %s%s

SISTEMA DE SCORING OFICIAL (Total: 0-100 puntos):

**DIMENSIÓN 1: Perfil Demográfico (0-10 puntos)**
- Ubicación: +2 (Perú) / +1 (Latinoamérica) / +0.5 (Otros)
- Profesión: +4 (empresario/PYME) / +2 (empleado) / +1 (no especifica)
- Coherencia: +2 (consistente) / +1 (parcial) / +0 (incoherente)
- Contexto apropiado: +2 (edad/situación coherente con compra)

**DIMENSIÓN 2: Comportamiento Digital (0-15 puntos)**
- Velocidad respuesta: +4 (<5min) / +2 (<30min) / +1 (>30min)
- Nivel detalle: +4 (específico) / +2 (moderado) / +1 (vago)
- Engagement: +4 (preguntas específicas) / +2 (completo) / +1 (básico)
- Completitud: +3 (datos completos) / +2 (parcial) / +1 (mínima)

**DIMENSIÓN 3: Capacidad Financiera (0-25 puntos)**
- Presupuesto: +8 (monto específico) / +6 (rango) / +4 (referencia) / +2 (vago) / +0 (no menciona)
- Autoridad: +8 (decisor) / +6 (influenciador) / +4 (participante) / +2 (consultor) / +0 (sin autoridad)
- Timeframe: +5 (inmediato) / +4 (corto plazo) / +3 (mediano) / +1 (largo) / +0 (sin urgencia)
- Experiencia: +4 (tiene) / +2 (poca) / +0 (primera vez)

**DIMENSIÓN 4: Necesidad/Urgencia (0-15 puntos)**
- Nivel urgencia: +6 (inmediato) / +4 (pronto) / +2 (futuro) / +0 (sin urgencia)
- Consecuencias: +5 (críticas) / +3 (importantes) / +1 (menores) / +0 (ninguna)
- Presión temporal: +4 (deadline específico) / +2 (general) / +1 (flexible) / +0 (ninguna)

**DIMENSIÓN 5: Experiencia Previa (0-10 puntos)**
- En subastas: +5 (experimentado) / +3 (alguna) / +1 (novato) / +0 (nunca)
- En compras online: +5 (frecuente) / +3 (ocasional) / +1 (rara vez) / +0 (primera vez)

**DIMENSIÓN 6: Engagement Actual (0-10 puntos)**
- Disponibilidad: +3 (explícita) / +1 (implícita) / +0 (no clara)
- Interés demo/visita: +4 (solicita) / +2 (acepta) / +0 (rechaza)
- Solicitudes específicas: +3 (pide detalles) / +1 (básicas) / +0 (ninguna)

**DIMENSIÓN 7: Contexto de Compra (0-15 puntos)**
- Motivo: +5 (reemplazo urgente) / +4 (expansión) / +3 (mejora) / +2 (exploración) / +1 (curiosidad)
- Investigación: +5 (comparó opciones) / +3 (parcial) / +1 (primera búsqueda)
- Conocimiento: +5 (experto) / +3 (intermedio) / +1 (básico)

**BOOSTS (+3 a +7):**
- Referido por cliente: +7
- Mencionó competencia: +6
- Solicitó especialista: +6
- Fecha específica: +5
- Preguntó garantías: +4
- Conocimiento técnico: +3

**PENALIZACIONES (-2 a -6):**
- Comportamiento "tire-patadas": -6
- Inconsistencias: -5
- Evasivo sobre presupuesto: -4
- Múltiples consultas sin compromiso: -2

CLASIFICACIÓN:
- HOT (85-100): Contacto inmediato (1h) por especialista, seguimiento 4h
- WARM (65-84): Contacto 4-8h por especialista, seguimiento 24h
- COLD (45-64): Invitar a comunidad, seguimiento 1 mes
- DISCARDED (<45): No contactar

FORMATO DE RESPUESTA (JSON ESTRICTO):
{
  "dimension1_perfilDemografico": {
    "ubicacion": "string (Perú/Latinoamérica/Otros/No especificado)",
    "profesion": "string (empresario/empleado/no especifica)",
    "coherencia": "string (consistente/parcial/incoherente)",
    "contexto": "string (apropiado/parcial/inadecuado)",
    "score": 0-10,
    "reasoning": "explicación breve"
  },
  "dimension2_comportamientoDigital": {
    "velocidadRespuesta": "string (<5min/<30min/>30min/desconocido)",
    "nivelDetalle": "string (específico/moderado/vago)",
    "engagement": "string (preguntas específicas/completo/básico)",
    "completitud": "string (completos/parcial/mínima)",
    "score": 0-15,
    "reasoning": "explicación breve"
  },
  "dimension3_capacidadFinanciera": {
    "presupuestoMencionado": "string (monto específico/rango/referencia/vago/no menciona)",
    "autoridadCompra": "string (decisor/influenciador/participante/consultor/sin autoridad)",
    "timeframe": "string (inmediato/corto/mediano/largo/sin urgencia)",
    "experienciaCompras": "string (tiene/poca/primera vez)",
    "score": 0-25,
    "reasoning": "explicación breve"
  },
  "dimension4_necesidadUrgencia": {
    "nivelUrgencia": "string (inmediato/pronto/futuro/sin urgencia)",
    "consecuencias": "string (críticas/importantes/menores/ninguna)",
    "presionTemporal": "string (deadline específico/general/flexible/ninguna)",
    "score": 0-15,
    "reasoning": "explicación breve"
  },
  "dimension5_experienciaPrevia": {
    "enSubastas": "string (experimentado/alguna/novato/nunca)",
    "enComprasOnline": "string (frecuente/ocasional/rara vez/primera vez)",
    "score": 0-10,
    "reasoning": "explicación breve"
  },
  "dimension6_engagementActual": {
    "disponibilidad": "string (explícita/implícita/no clara)",
    "interesDemo": "string (solicita/acepta/rechaza)",
    "solicitudesEspecificas": "string (pide detalles/básicas/ninguna)",
    "score": 0-10,
    "reasoning": "explicación breve"
  },
  "dimension7_contextoCompra": {
    "motivoCompra": "string (reemplazo urgente/expansión/mejora/exploración/curiosidad)",
    "investigacionRealizada": "string (comparó opciones/parcial/primera búsqueda)",
    "conocimientoProducto": "string (experto/intermedio/básico)",
    "score": 0-15,
    "reasoning": "explicación breve"
  },
  "boosts": ["lista de boosts aplicados con formato: 'nombre: +X puntos'"],
  "penalizaciones": ["lista de penalizaciones con formato: 'nombre: -X puntos'"],
  "totalScore": 0-100,
  "category": "hot|warm|cold|discarded",
  "accionRecomendada": "string (descripción de acción)",
  "tiempoContacto": "string (cuándo contactar)",
  "tipoSeguimiento": "string (tipo de seguimiento)",
  "resumenEjecutivo": "string (2-3 líneas resumiendo por qué este score)"
}

IMPORTANTE:
1. Analiza TODA la conversación, no solo el último mensaje
2. Sé estricto con los criterios oficiales
3. Justifica cada puntuación en el reasoning
4. El totalScore debe ser la suma de todas las dimensiones + boosts - penalizaciones
5. La categoría debe corresponder exactamente al rango de puntos
6. Responde SOLO con JSON válido, sin texto adicional

Analiza y genera el scoring:`, input.SessionID, input.Channel, historyText)
}

type ScoringResponse struct {
	Dimension1 struct {
		Ubicacion  string `json:"ubicacion"`
		Profesion  string `json:"profesion"`
		Coherencia string `json:"coherencia"`
		Contexto   string `json:"contexto"`
		Score      int    `json:"score"`
		Reasoning  string `json:"reasoning"`
	} `json:"dimension1_perfilDemografico"`
	Dimension2 struct {
		VelocidadRespuesta string `json:"velocidadRespuesta"`
		NivelDetalle       string `json:"nivelDetalle"`
		Engagement         string `json:"engagement"`
		Completitud        string `json:"completitud"`
		Score              int    `json:"score"`
		Reasoning          string `json:"reasoning"`
	} `json:"dimension2_comportamientoDigital"`
	Dimension3 struct {
		PresupuestoMencionado string `json:"presupuestoMencionado"`
		AutoridadCompra       string `json:"autoridadCompra"`
		Timeframe             string `json:"timeframe"`
		ExperienciaCompras    string `json:"experienciaCompras"`
		Score                 int    `json:"score"`
		Reasoning             string `json:"reasoning"`
	} `json:"dimension3_capacidadFinanciera"`
	Dimension4 struct {
		NivelUrgencia   string `json:"nivelUrgencia"`
		Consecuencias   string `json:"consecuencias"`
		PresionTemporal string `json:"presionTemporal"`
		Score           int    `json:"score"`
		Reasoning       string `json:"reasoning"`
	} `json:"dimension4_necesidadUrgencia"`
	Dimension5 struct {
		EnSubastas      string `json:"enSubastas"`
		EnComprasOnline string `json:"enComprasOnline"`
		Score           int    `json:"score"`
		Reasoning       string `json:"reasoning"`
	} `json:"dimension5_experienciaPrevia"`
	Dimension6 struct {
		Disponibilidad         string `json:"disponibilidad"`
		InteresDemo            string `json:"interesDemo"`
		SolicitudesEspecificas string `json:"solicitudesEspecificas"`
		Score                  int    `json:"score"`
		Reasoning              string `json:"reasoning"`
	} `json:"dimension6_engagementActual"`
	Dimension7 struct {
		MotivoCompra           string `json:"motivoCompra"`
		InvestigacionRealizada string `json:"investigacionRealizada"`
		ConocimientoProducto   string `json:"conocimientoProducto"`
		Score                  int    `json:"score"`
		Reasoning              string `json:"reasoning"`
	} `json:"dimension7_contextoCompra"`
	Boosts             []string `json:"boosts"`
	Penalizaciones     []string `json:"penalizaciones"`
	TotalScore         int      `json:"totalScore"`
	Category           string   `json:"category"`
	AccionRecomendada  string   `json:"accionRecomendada"`
	TiempoContacto     string   `json:"tiempoContacto"`
	TipoSeguimiento    string   `json:"tipoSeguimiento"`
	ResumenEjecutivo   string   `json:"resumenEjecutivo"`
}

func (s *ScoringAgent) parseScoring(responseText string) *models.ScoringData {
	responseText = strings.TrimSpace(responseText)

	start := strings.Index(responseText, "{")
	end := strings.LastIndex(responseText, "}")

	if start == -1 || end == -1 {
		return s.defaultScoring("Error parseando respuesta del modelo")
	}

	jsonStr := responseText[start : end+1]

	var scoring ScoringResponse
	if err := json.Unmarshal([]byte(jsonStr), &scoring); err != nil {
		return s.defaultScoring(fmt.Sprintf("Error JSON: %v", err))
	}

	dimensionScores := map[string]int{
		"perfil_demografico":      scoring.Dimension1.Score,
		"comportamiento_digital":  scoring.Dimension2.Score,
		"capacidad_financiera":    scoring.Dimension3.Score,
		"necesidad_urgencia":      scoring.Dimension4.Score,
		"experiencia_previa":      scoring.Dimension5.Score,
		"engagement_actual":       scoring.Dimension6.Score,
		"contexto_compra":         scoring.Dimension7.Score,
	}

	// Calcular score real sumando dimensiones
	calculatedScore := 0
	for _, score := range dimensionScores {
		calculatedScore += score
	}

	// Agregar boosts
	for _, boost := range scoring.Boosts {
		// Extraer puntos del formato "nombre: +X puntos"
		if strings.Contains(boost, "+") {
			parts := strings.Split(boost, "+")
			if len(parts) >= 2 {
				pointsStr := strings.TrimSpace(strings.Split(parts[1], " ")[0])
				if points, err := fmt.Sscanf(pointsStr, "%d", new(int)); points > 0 && err == nil {
					var p int
					fmt.Sscanf(pointsStr, "%d", &p)
					calculatedScore += p
				}
			}
		}
	}

	// Restar penalizaciones
	for _, penalty := range scoring.Penalizaciones {
		// Extraer puntos del formato "nombre: -X puntos"
		if strings.Contains(penalty, "-") {
			parts := strings.Split(penalty, "-")
			if len(parts) >= 2 {
				pointsStr := strings.TrimSpace(strings.Split(parts[1], " ")[0])
				if points, err := fmt.Sscanf(pointsStr, "%d", new(int)); points > 0 && err == nil {
					var p int
					fmt.Sscanf(pointsStr, "%d", &p)
					calculatedScore -= p
				}
			}
		}
	}

	// Validar score
	finalScore := scoring.TotalScore
	if finalScore != calculatedScore {
		log.Printf("⚠️ Warning: Gemini score %d != calculated %d, usando calculado", finalScore, calculatedScore)
		finalScore = calculatedScore
	}

	// Limitar score a rango válido 0-100
	if finalScore > 100 {
		log.Printf("⚠️ Warning: Score %d excede 100, limitando", finalScore)
		finalScore = 100
	}
	if finalScore < 0 {
		log.Printf("⚠️ Warning: Score %d menor que 0, limitando", finalScore)
		finalScore = 0
	}

	// Recalcular categoría basada en score validado
	category := scoring.Category
	if finalScore >= 85 {
		category = "hot"
	} else if finalScore >= 65 {
		category = "warm"
	} else if finalScore >= 45 {
		category = "cold"
	} else {
		category = "discarded"
	}

	return &models.ScoringData{
		TotalScore:         finalScore,
		Category:           category,
		DimensionScores:    dimensionScores,
		Boosts:             scoring.Boosts,
		Penalizaciones:     scoring.Penalizaciones,
		AccionRecomendada:  scoring.AccionRecomendada,
		TiempoContacto:     scoring.TiempoContacto,
		TipoSeguimiento:    scoring.TipoSeguimiento,
	}
}

func (s *ScoringAgent) defaultScoring(reason string) *models.ScoringData {
	return &models.ScoringData{
		TotalScore:         0,
		Category:           "discarded",
		DimensionScores:    map[string]int{},
		Boosts:             []string{},
		Penalizaciones:     []string{fmt.Sprintf("Error en scoring: %s", reason)},
		AccionRecomendada:  "Revisar manualmente - error en análisis automático",
		TiempoContacto:     "N/A",
		TipoSeguimiento:    "Manual",
	}
}

func (s *ScoringAgent) generateScoringMessage(data *models.ScoringData) string {
	var categoryEmoji string
	switch data.Category {
	case "hot":
		categoryEmoji = "🔥"
	case "warm":
		categoryEmoji = "🌡️"
	case "cold":
		categoryEmoji = "❄️"
	default:
		categoryEmoji = "📊"
	}

	msg := fmt.Sprintf("%s **Lead Score: %d/100** - Categoría: %s\n\n",
		categoryEmoji, data.TotalScore, data.Category)

	msg += fmt.Sprintf("**Acción recomendada:** %s\n", data.AccionRecomendada)
	msg += fmt.Sprintf("**Tiempo de contacto:** %s\n", data.TiempoContacto)
	msg += fmt.Sprintf("**Tipo de seguimiento:** %s\n", data.TipoSeguimiento)

	if len(data.Boosts) > 0 {
		msg += fmt.Sprintf("\n✅ **Boosts aplicados:** %s", strings.Join(data.Boosts, ", "))
	}

	if len(data.Penalizaciones) > 0 {
		msg += fmt.Sprintf("\n⚠️ **Penalizaciones:** %s", strings.Join(data.Penalizaciones, ", "))
	}

	return msg
}
