package utils

import (
	"regexp"
	"strings"
	"unicode/utf8"
)

const (
	MaxMessageLength = 2000
	MinMessageLength = 1
	MaxSessionIDLength = 100
)

// ValidateAndSanitizeMessage valida y sanitiza mensajes de chat
func ValidateAndSanitizeMessage(message string) (string, error) {
	// Verificar que no esté vacío
	trimmed := strings.TrimSpace(message)
	if len(trimmed) < MinMessageLength {
		return "", &ValidationError{Field: "message", Message: "El mensaje no puede estar vacío"}
	}

	// Verificar longitud máxima
	if utf8.RuneCountInString(trimmed) > MaxMessageLength {
		return "", &ValidationError{Field: "message", Message: "El mensaje es demasiado largo (máximo 2000 caracteres)"}
	}

	// Sanitizar caracteres de control peligrosos (mantener emojis y caracteres normales)
	sanitized := sanitizeControlChars(trimmed)

	// Detectar y rechazar intentos de inyección evidentes
	if isInjectionAttempt(sanitized) {
		return "", &ValidationError{Field: "message", Message: "Mensaje contiene patrones no permitidos"}
	}

	return sanitized, nil
}

// ValidateSessionID valida que el session ID sea seguro
func ValidateSessionID(sessionID string) error {
	if sessionID == "" {
		return nil // Session ID vacío es válido (se crea uno nuevo)
	}

	// Verificar longitud
	if len(sessionID) > MaxSessionIDLength {
		return &ValidationError{Field: "sessionId", Message: "Session ID demasiado largo"}
	}

	// Solo permitir caracteres alfanuméricos, guiones y guiones bajos
	matched, _ := regexp.MatchString(`^[a-zA-Z0-9_-]+$`, sessionID)
	if !matched {
		return &ValidationError{Field: "sessionId", Message: "Session ID contiene caracteres no válidos"}
	}

	return nil
}

// ValidateChannel valida que el canal sea válido
func ValidateChannel(channel string) error {
	validChannels := map[string]bool{
		"web":      true,
		"whatsapp": true,
		"api":      true,
	}

	if channel == "" {
		return nil // Canal vacío usa default
	}

	if !validChannels[strings.ToLower(channel)] {
		return &ValidationError{Field: "channel", Message: "Canal no válido (use: web, whatsapp, api)"}
	}

	return nil
}

// sanitizeControlChars elimina caracteres de control peligrosos pero mantiene saltos de línea y emojis
func sanitizeControlChars(s string) string {
	// Eliminar caracteres de control excepto \n, \r, \t
	// Los emojis están en rangos UTF-8 normales, no son caracteres de control
	var result strings.Builder
	for _, r := range s {
		// Permitir: letras, números, puntuación, espacios, saltos de línea, emojis
		// Rechazar: caracteres de control raros (0x00-0x08, 0x0B-0x0C, 0x0E-0x1F)
		if r == '\n' || r == '\r' || r == '\t' || r >= 0x20 || r > 0x7F {
			result.WriteRune(r)
		}
	}
	return result.String()
}

// isInjectionAttempt detecta intentos evidentes de inyección
func isInjectionAttempt(s string) bool {
	lowerS := strings.ToLower(s)

	// Patrones de SQL injection
	sqlPatterns := []string{
		"' or '1'='1",
		"' or 1=1",
		"'; drop table",
		"'; delete from",
		"union select",
		"<script",
		"javascript:",
		"onerror=",
		"onload=",
	}

	for _, pattern := range sqlPatterns {
		if strings.Contains(lowerS, pattern) {
			return true
		}
	}

	// Detectar múltiples comillas seguidas (típico de inyección)
	if strings.Count(s, "'") > 5 || strings.Count(s, "\"") > 10 {
		return true
	}

	return false
}

// ValidationError es un error de validación personalizado
type ValidationError struct {
	Field   string
	Message string
}

func (e *ValidationError) Error() string {
	return e.Message
}
