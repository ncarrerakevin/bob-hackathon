package services

import (
	"bob-hackathon/internal/config"
	"bob-hackathon/internal/models"
	"encoding/json"
	"log"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/google/uuid"
)

type SessionService struct {
	sessions     map[string]*models.Session
	leads        map[string]*models.Lead
	mu           sync.RWMutex
	sessionsFile string
	leadsFile    string
}

var sessionServiceInstance *SessionService
var sessionServiceOnce sync.Once

func GetSessionService() *SessionService {
	sessionServiceOnce.Do(func() {
		dataDir := config.AppConfig.DataDir
		sessionServiceInstance = &SessionService{
			sessions:     make(map[string]*models.Session),
			leads:        make(map[string]*models.Lead),
			sessionsFile: filepath.Join(dataDir, "sessions.json"),
			leadsFile:    filepath.Join(dataDir, "leads.json"),
		}
		sessionServiceInstance.loadFromDisk()
	})
	return sessionServiceInstance
}

func (s *SessionService) GetOrCreateSession(sessionID, channel string) *models.Session {
	s.mu.Lock()
	defer s.mu.Unlock()

	// Si no hay sessionID, generar uno nuevo
	if sessionID == "" {
		sessionID = channel + "-" + uuid.New().String()
	}

	// Si la sesión existe, retornarla
	if session, exists := s.sessions[sessionID]; exists {
		return session
	}

	// Crear nueva sesión
	now := time.Now()
	session := &models.Session{
		SessionID: sessionID,
		Channel:   channel,
		Messages:  []models.Message{},
		CreatedAt: now,
		UpdatedAt: now,
		LeadScore: 0,
		Category:  "cold",
		Metadata:  make(map[string]string),
	}

	s.sessions[sessionID] = session
	s.saveToDisk()

	log.Printf("Nueva sesión creada: %s (canal: %s)", sessionID, channel)
	return session
}

func (s *SessionService) AddMessage(sessionID, role, content string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	session, exists := s.sessions[sessionID]
	if !exists {
		log.Printf("Sesión no encontrada: %s", sessionID)
		return
	}

	message := models.Message{
		Role:      role,
		Content:   content,
		Timestamp: time.Now(),
	}

	session.Messages = append(session.Messages, message)
	session.UpdatedAt = time.Now()

	s.saveToDisk()
}

func (s *SessionService) GetSession(sessionID string) *models.Session {
	s.mu.RLock()
	defer s.mu.RUnlock()

	return s.sessions[sessionID]
}

func (s *SessionService) GetMessages(sessionID string) []models.Message {
	s.mu.RLock()
	defer s.mu.RUnlock()

	session, exists := s.sessions[sessionID]
	if !exists {
		return []models.Message{}
	}

	return session.Messages
}

func (s *SessionService) UpdateScore(sessionID string, score int, category string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	session, exists := s.sessions[sessionID]
	if !exists {
		return
	}

	session.LeadScore = score
	session.Category = category
	session.UpdatedAt = time.Now()

	s.saveToDisk()
}

func (s *SessionService) CreateOrUpdateLead(leadData *models.Lead) {
	s.mu.Lock()
	defer s.mu.Unlock()

	leadData.UpdatedAt = time.Now()

	if _, exists := s.leads[leadData.SessionID]; !exists {
		leadData.CreatedAt = time.Now()
	}

	s.leads[leadData.SessionID] = leadData
	s.saveToDisk()

	log.Printf("Lead actualizado: %s - Score: %d (%s)", leadData.SessionID, leadData.Score, leadData.Category)
}

func (s *SessionService) GetAllLeads(category, channel string) []*models.Lead {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var result []*models.Lead

	for _, lead := range s.leads {
		// Filtrar por categoría si se especifica
		if category != "" && lead.Category != category {
			continue
		}

		// Filtrar por canal si se especifica
		if channel != "" && lead.Channel != channel {
			continue
		}

		result = append(result, lead)
	}

	return result
}

func (s *SessionService) GetLead(sessionID string) *models.Lead {
	s.mu.RLock()
	defer s.mu.RUnlock()

	return s.leads[sessionID]
}

func (s *SessionService) GetLeadsStats() *models.LeadStats {
	s.mu.RLock()
	defer s.mu.RUnlock()

	stats := &models.LeadStats{
		Total:     len(s.leads),
		Hot:       0,
		Warm:      0,
		Cold:      0,
		AvgScore:  0,
		ByChannel: make(map[string]int),
	}

	totalScore := 0

	for _, lead := range s.leads {
		totalScore += lead.Score

		switch lead.Category {
		case "hot":
			stats.Hot++
		case "warm":
			stats.Warm++
		case "cold":
			stats.Cold++
		case "discarded":
			stats.Discarded++
		}

		stats.ByChannel[lead.Channel]++
	}

	if stats.Total > 0 {
		stats.AvgScore = float64(totalScore) / float64(stats.Total)
	}

	return stats
}

func (s *SessionService) loadFromDisk() {
	// Cargar sesiones
	if data, err := os.ReadFile(s.sessionsFile); err == nil {
		if err := json.Unmarshal(data, &s.sessions); err != nil {
			log.Printf("Error al cargar sesiones: %v", err)
		} else {
			log.Printf("%d sesiones cargadas desde disco", len(s.sessions))
		}
	}

	// Cargar leads
	if data, err := os.ReadFile(s.leadsFile); err == nil {
		if err := json.Unmarshal(data, &s.leads); err != nil {
			log.Printf("Error al cargar leads: %v", err)
		} else {
			log.Printf("%d leads cargados desde disco", len(s.leads))
		}
	}
}

func (s *SessionService) saveToDisk() {
	// Guardar sesiones
	if data, err := json.MarshalIndent(s.sessions, "", "  "); err == nil {
		if err := os.WriteFile(s.sessionsFile, data, 0644); err != nil {
			log.Printf("Error al guardar sesiones: %v", err)
		}
	}

	// Guardar leads
	if data, err := json.MarshalIndent(s.leads, "", "  "); err == nil {
		if err := os.WriteFile(s.leadsFile, data, 0644); err != nil {
			log.Printf("Error al guardar leads: %v", err)
		}
	}
}
