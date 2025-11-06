// cmd/whbot/main.go
package main

import (
	"context"
	"log"
	"strings"

	"github.com/investigadorinexperto/bot/engine"
	"github.com/investigadorinexperto/bot/internal/config"
	"github.com/joho/godotenv"
	"go.mau.fi/whatsmeow/types/events"
)

func main() {
	_ = godotenv.Load("/home/ivnx/labs/bob-hackathon/bot/.env")
	cfgApp := config.Load()

	// ====== Mapear config → engine.Config
	// Forward mode: "folder" (on) o "off" (solo webhook si está habilitado)
	forwardMode := engine.ForwardOff
	if strings.ToLower(cfgApp.ForwardMode) == "folder" {
		forwardMode = engine.ForwardFolder
	}

	engCfg := engine.Config{
		DBPath:             cfgApp.DBPath,
		MsgDBPath:          cfgApp.MsgDBPath,
		EnableStatus:       cfgApp.EnableStatus,
		BackupEvery:        cfgApp.BackupEvery,
		MaxConnAttempts:    cfgApp.MaxConnAttempts,
		ReconnectBaseDelay: cfgApp.ReconnectBaseDelay,
		HTTPPort:           cfgApp.HTTPPort,
		Forward: engine.ForwardingConfig{
			Mode:         forwardMode,         // folder u off (webhook va aparte)
			ContextDepth: cfgApp.ContextDepth, // contexto N últimos mensajes
			OutFolder:    cfgApp.Outbox,       // usado si Mode=folder
			ExtraParams:  cfgApp.ForwardExtraJSON,
			Webhook: engine.WebhookConfig{
				Enabled: cfgApp.WebhookEnabled,
				URL:     cfgApp.WebhookURL,
				Secret:  cfgApp.WebhookSecret,
				Headers: cfgApp.WebhookHeaders,
			},
		},
	}

	// ====== Crear Engine
	e, err := engine.NewEngine(engCfg)
	if err != nil {
		log.Fatalf("engine init error: %v", err)
	}
	// ====== Handlers del engine
	h := engine.Handlers{
		OnMessage: func(ctx context.Context, m *events.Message) error {
			log.Printf("[OnMessage] chat=%s from=%s id=%s", m.Info.Chat, m.Info.Sender, m.Info.ID)
			return nil
		},
		OnReceipt: func(ctx context.Context, r *events.Receipt) error {
			log.Printf("[OnReceipt] chat=%s ids=%v type=%s", r.Chat, r.MessageIDs, r.Type)
			return nil
		},
		OnPresence: func(ctx context.Context, p *events.Presence) error {
			log.Printf("[OnPresence] from=%s unavailable=%v", p.From, p.Unavailable)
			return nil
		},
		OnGroupUpdate: func(ctx context.Context, g *events.GroupInfo) error {
			log.Printf("[OnGroupUpdate] jid=%s", g.JID)
			return nil
		},
		OnStatus: func(ctx context.Context, s any) error { return nil },
		OnError: func(ctx context.Context, err error) {
			log.Printf("[OnError] %v", err)
		},
	}

	// ====== Run (bloquea hasta SIGINT/SIGTERM)
	if err := e.Run(context.Background(), h); err != nil {
		log.Fatalf("engine run error: %v", err)
	}
}
