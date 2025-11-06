package config

import (
	"log"
	"os"

	"github.com/joho/godotenv"
)

type Config struct {
	GeminiAPIKey    string
	GeminiModel     string
	Port            string
	BOBAPIBaseURL   string
	CORSOrigins     string
	FrontendURL     string
	DataDir         string
	AdminAPIKey     string
}

var AppConfig *Config

func LoadConfig() {
	// Cargar .env
	if err := godotenv.Load(); err != nil {
		log.Println("No se encontró archivo .env, usando variables de entorno del sistema")
	}

	AppConfig = &Config{
		GeminiAPIKey:  getEnv("GEMINI_API_KEY", ""),
		GeminiModel:   getEnv("GEMINI_MODEL", "gemini-2.0-flash-exp"),
		Port:          getEnv("PORT", "3000"),
		BOBAPIBaseURL: getEnv("BOB_API_BASE_URL", "https://apiv3.somosbob.com/v3"),
		CORSOrigins:   getEnv("CORS_ORIGINS", "http://localhost:5173,http://localhost:3000"),
		FrontendURL:   getEnv("FRONTEND_URL", "http://localhost:5173"),
		DataDir:       getEnv("DATA_DIR", "data"),
		AdminAPIKey:   getEnv("ADMIN_API_KEY", ""),
	}

	if AppConfig.GeminiAPIKey == "" {
		log.Fatal("GEMINI_API_KEY es requerido")
	}

	if AppConfig.AdminAPIKey == "" {
		log.Println("⚠️  WARNING: ADMIN_API_KEY not set - Admin endpoints will be UNPROTECTED!")
	} else {
		log.Println("✅ Admin API protection enabled")
	}

	log.Printf("Configuración cargada - Puerto: %s, Modelo: %s, DataDir: %s", AppConfig.Port, AppConfig.GeminiModel, AppConfig.DataDir)
}

func getEnv(key, defaultValue string) string {
	value := os.Getenv(key)
	if value == "" {
		return defaultValue
	}
	return value
}
