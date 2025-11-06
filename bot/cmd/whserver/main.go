// cmd/whserver/main.go
package main

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"log"
	"math/rand"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/joho/godotenv"

	"github.com/investigadorinexperto/bot/internal/config"
	"github.com/investigadorinexperto/bot/pkg/filters"
	"github.com/investigadorinexperto/bot/pkg/pipeline"
	"github.com/investigadorinexperto/bot/pkg/rules"
)

//
// =======================
// Modelos / wire format
// =======================
//

type Envelope struct {
	EventType   string           `json:"event_type"`
	Direction   string           `json:"direction"` // "in" | "out" (puede venir vacío en algunos eventos)
	EventRaw    any              `json:"event_raw"` // no se usa aquí
	ChatJID     string           `json:"chat_jid"`
	SenderJID   string           `json:"sender_jid"`
	ChatName    string           `json:"chat_name"`
	MessageID   string           `json:"message_id"`
	MessageIDs  []string         `json:"message_ids"`  // para receipts múltiples
	ReceiptType string           `json:"receipt_type"` // read|played|sender|""
	Text        string           `json:"text"`
	Media       map[string]any   `json:"media"`
	Context     []map[string]any `json:"context"`
	Extra       map[string]any   `json:"extra"`
	At          string           `json:"at"`
}

// Entrada de media guardada en el perfil
type MediaEntry struct {
	Direction        string    `json:"direction"`            // "in" | "out"
	ChatJID          string    `json:"chat_jid"`             // a quién pertenece el perfil (key)
	SenderJID        string    `json:"sender_jid,omitempty"` // emisor (en grupos útil)
	MessageID        string    `json:"message_id,omitempty"`
	Type             string    `json:"type,omitempty"`     // image|video|audio|document
	Mimetype         string    `json:"mimetype,omitempty"` // si viene
	Title            string    `json:"title,omitempty"`    // filename/título si aplica
	URL              string    `json:"url,omitempty"`      // link CDN
	Caption          string    `json:"caption,omitempty"`  // usamos Text del envelope
	At               time.Time `json:"at"`
	DirectPath       string    `json:"direct_path,omitempty"`
	MediaKeyB64      string    `json:"media_key_b64,omitempty"`
	FileSHA256B64    string    `json:"file_sha256_b64,omitempty"`
	FileEncSHA256B64 string    `json:"file_enc_sha256_b64,omitempty"`
	FileLength       uint64    `json:"file_length,omitempty"`
	Seconds          uint32    `json:"seconds,omitempty"` // útil en audio/notas de voz
}

type Profile struct {
	SenderJID string            `json:"sender_jid"` // usamos este campo para almacenar la "key" (ChatJID)
	Name      string            `json:"name,omitempty"`
	Lang      string            `json:"lang"`
	Tier      string            `json:"tier"`
	Tags      map[string]string `json:"tags"`

	FirstSeen time.Time `json:"first_seen"`
	LastConn  time.Time `json:"last_conn"`
	LastChat  string    `json:"last_chat"`
	LastText  string    `json:"last_text"`

	// Historial compacto de multimedia por dirección
	Media struct {
		In  []MediaEntry `json:"in"`
		Out []MediaEntry `json:"out"`
	} `json:"media"`

	Block struct {
		Spam      bool      `json:"spam"`
		Malicious bool      `json:"malicious"`
		Permanent bool      `json:"permanent"`
		Until     time.Time `json:"until,omitempty"`
	} `json:"block"`

	Metrics struct {
		MsgIn         int       `json:"msg_in"`
		MsgOut        int       `json:"msg_out"`
		LastMsgAt     time.Time `json:"last_msg_at"`
		LastMsgID     string    `json:"last_msg_id"`
		StreakDays    int       `json:"streak_days"`
		StreakLastDay string    `json:"streak_last_day"`
	} `json:"metrics"`
}

//
// =======================
// Logging helper
// =======================
//

type jlog struct{ json bool }

func (l jlog) kv(level, msg string, kv ...any) {
	if l.json {
		m := map[string]any{"level": level, "msg": msg, "ts": time.Now().Format(time.RFC3339)}
		for i := 0; i+1 < len(kv); i += 2 {
			if k, ok := kv[i].(string); ok {
				m[k] = kv[i+1]
			}
		}
		b, _ := json.Marshal(m)
		log.Println(string(b))
		return
	}
	log.Println(append([]any{"[" + strings.ToUpper(level) + "]", msg}, kv...)...)
}
func (l jlog) Info(msg string, kv ...any)  { l.kv("info", msg, kv...) }
func (l jlog) Warn(msg string, kv ...any)  { l.kv("warn", msg, kv...) }
func (l jlog) Error(msg string, kv ...any) { l.kv("error", msg, kv...) }

//
// =======================
// Utilidades de logging
// =======================
//

const maxLogText = 120

func previewText(s string, max int) string {
	s = strings.TrimSpace(s)
	if max <= 0 {
		return s
	}
	r := []rune(s)
	if len(r) <= max {
		return s
	}
	return string(r[:max]) + "…"
}

//
// =======================
/* Seguridad / firmas */
// =======================

func verifySignature(secret string, body []byte, headerSig string) bool {
	if secret == "" {
		return false
	}
	headerSig = strings.TrimSpace(headerSig)
	if !strings.HasPrefix(headerSig, "sha256=") {
		return false
	}
	got := strings.TrimPrefix(headerSig, "sha256=")
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	expected := hex.EncodeToString(mac.Sum(nil))
	return hmac.Equal([]byte(got), []byte(expected))
}

func verifyTimestamp(tsHeader string, skew time.Duration) error {
	if tsHeader == "" {
		return errors.New("missing timestamp")
	}
	var ts time.Time
	if n, err := strconv.ParseInt(tsHeader, 10, 64); err == nil {
		ts = time.Unix(n, 0)
	} else {
		t, err2 := time.Parse(time.RFC3339, tsHeader)
		if err2 != nil {
			return errors.New("bad timestamp format")
		}
		ts = t
	}
	diff := time.Since(ts)
	if diff < 0 {
		diff = -diff
	}
	if diff > skew {
		return errors.New("timestamp skew too large")
	}
	return nil
}

//
// =======================
// Dedupe in-memory
// =======================
//

type deduper struct {
	mu     sync.Mutex
	seen   map[string]time.Time
	window time.Duration
}

func newDeduper(win time.Duration) *deduper {
	d := &deduper{seen: make(map[string]time.Time), window: win}
	go d.gc()
	return d
}
func (d *deduper) gc() {
	t := time.NewTicker(1 * time.Minute)
	for range t.C {
		cut := time.Now().Add(-d.window)
		d.mu.Lock()
		for k, v := range d.seen {
			if v.Before(cut) {
				delete(d.seen, k)
			}
		}
		d.mu.Unlock()
	}
}
func (d *deduper) Seen(id string) bool {
	if id == "" {
		return false
	}
	now := time.Now()
	d.mu.Lock()
	defer d.mu.Unlock()
	if when, ok := d.seen[id]; ok {
		if now.Sub(when) <= d.window {
			return true
		}
	}
	d.seen[id] = now
	return false
}

//
// =======================
// Router (reglas simples)
// =======================
//

type SimpleRouter struct {
	log           jlog
	sendFn        func(to, msg string) error
	typingFn      func(chat string, typing bool, media string) error
	typingPause   time.Duration
	preReplyDelay time.Duration
	eng           *rules.Engine
	// waits tunables
	baseWait         time.Duration
	perCharMs        int
	jitterMs         int
	maxWait          time.Duration
	filterChain      filters.Chain
	aggregator       *pipeline.Aggregator
	muLast           sync.Mutex
	lastByChat       map[string]rules.Envelope
	lastActiveChat   string
	muMap            sync.Mutex
	lastChatBySender map[string]string
	// Nuevo: debounce de typing por chat (anti-ruido, no bloquea reset)
	lastTypingAt   map[string]time.Time
	typingDebounce time.Duration
	muProf         sync.Mutex
	profiles       map[string]*Profile // key: ChatJID (ver getOrCreateProfileByKey)
}

func NewSimpleRouter(
	l jlog,
	sendFn func(to, msg string) error,
	typingFn func(chat string, typing bool, media string) error,
	pause time.Duration,
	baseWait time.Duration,
	perCharMs int,
	jitterMs int,
	maxWait time.Duration,
	preReplyDelay time.Duration,
	eng *rules.Engine,
	agg *pipeline.Aggregator,
	chain filters.Chain,
) *SimpleRouter {
	return &SimpleRouter{
		log:              l,
		sendFn:           sendFn,
		typingFn:         typingFn,
		typingPause:      pause,
		preReplyDelay:    preReplyDelay,
		aggregator:       agg,
		filterChain:      chain,
		baseWait:         baseWait,
		perCharMs:        perCharMs,
		jitterMs:         jitterMs,
		maxWait:          maxWait,
		lastByChat:       make(map[string]rules.Envelope),
		lastChatBySender: make(map[string]string),
		lastTypingAt:     make(map[string]time.Time),
		typingDebounce:   700 * time.Millisecond,
		profiles:         make(map[string]*Profile),
	}
}

func (r *SimpleRouter) OnMessage(ctx context.Context, e Envelope) {
	// 1) Mensajes OUT: métricas + guardar media + salir
	if strings.EqualFold(e.Direction, "out") {
		if strings.TrimSpace(e.ChatJID) != "" {
			// guarda media OUT si viene (image/video/audio/document)
			r.appendMedia(e.ChatJID, e, 200) // cap de historial por dirección
			r.incOutboundFor(e.ChatJID)      // crea si no existe, incrementa MsgOut, toca LastMsgAt y persiste
		}
		r.log.Info("filtered", "reason", "direction_out", "chat", e.ChatJID)
		return
	}

	// 2) Mensajes IN: toca/crea perfil ANTES de filtros para conservar métricas y rutas
	r.touchProfileFromInbound(e)

	// 2.1) Capturar multimedia IN (si hay)
	if strings.TrimSpace(e.ChatJID) != "" {
		r.appendMedia(e.ChatJID, e, 200)
	}

	// 3) Filtros
	view := filters.EnvView{
		Direction: e.Direction,
		SenderJID: e.SenderJID,
		ChatJID:   e.ChatJID,
	}
	if !r.filterChain.Pass(view) {
		r.log.Info(
			"filtered",
			"reason", "filter_chain_reject",
			"dir", e.Direction,
			"chat", e.ChatJID,
			"from", e.SenderJID,
		)
		return
	}

	// 4) Agregador y últimos vistos
	if r.aggregator != nil {
		r.aggregator.Add(e.ChatJID)
	}
	if strings.TrimSpace(e.SenderJID) != "" && strings.TrimSpace(e.ChatJID) != "" {
		r.muMap.Lock()
		r.lastChatBySender[e.SenderJID] = e.ChatJID
		r.muMap.Unlock()
	}
	if strings.TrimSpace(e.ChatJID) != "" {
		r.muLast.Lock()
		r.lastActiveChat = e.ChatJID
		r.muLast.Unlock()
	}

	// 5) Evita responder a LID raros en 1:1
	if strings.Contains(e.SenderJID, "@lid") && !strings.Contains(e.ChatJID, "@g.us") {
		return
	}

	// 6) Adaptar envelope para el engine de reglas
	env := rules.Envelope{
		EventType: e.EventType,
		ChatJID:   e.ChatJID,
		SenderJID: e.SenderJID,
		ChatName:  e.ChatName,
		MessageID: e.MessageID,
		Text:      e.Text,
		At:        time.Now(),
	}
	if t, err := time.Parse(time.RFC3339, strings.TrimSpace(e.At)); err == nil {
		env.At = t
	}
	r.muLast.Lock()
	r.lastByChat[e.ChatJID] = env
	r.muLast.Unlock()

	// 7) Log
	r.log.Info(
		"message",
		"chat", e.ChatJID,
		"from", e.SenderJID,
		"text", previewText(e.Text, maxLogText),
		"text_len", len([]rune(strings.TrimSpace(e.Text))),
	)
}

func (r *SimpleRouter) OnReceipt(ctx context.Context, e Envelope) {
	view := filters.EnvView{Direction: e.Direction, SenderJID: e.SenderJID, ChatJID: e.ChatJID}
	if !r.filterChain.Pass(view) {
		r.log.Info("filtered", "reason", "filter_chain_reject", "dir", e.Direction, "chat", e.ChatJID, "from", e.SenderJID)
		return
	}
	if len(e.MessageIDs) > 0 {
		r.log.Info("receipt", "chat", e.ChatJID, "count", len(e.MessageIDs), "type", strings.TrimSpace(e.ReceiptType))
		return
	}
	if strings.TrimSpace(e.MessageID) == "" {
		return
	}
	r.log.Info("receipt", "chat", e.ChatJID, "msg_id", e.MessageID, "type", strings.TrimSpace(e.ReceiptType))
}

func (r *SimpleRouter) OnAny(ctx context.Context, e Envelope) {
	// 1) Typing SIEMPRE reinicia ventana, aunque venga sin SenderJID o con ChatJID raro.
	if isTypingEvent(e) && r.aggregator != nil {
		// Determinar chat destino con fallbacks robustos
		raw := strings.TrimSpace(e.ChatJID)
		// Preferir el mapeo por sender, incluso si el evento trae otro ChatJID “global”
		r.muMap.Lock()
		mapped := r.lastChatBySender[e.SenderJID]
		r.muMap.Unlock()
		chat := raw
		if mapped != "" && mapped != raw {
			chat = mapped
		}
		if chat == "" {
			// Último chat con mensajes (por si SenderJID es @lid sin mapeo)
			r.muLast.Lock()
			chat = r.lastActiveChat
			r.muLast.Unlock()
		}
		if chat != "" {
			// Solo tocar si ese chat ya tuvo mensajes (evita batches “fantasma”)
			r.muLast.Lock()
			_, seen := r.lastByChat[chat]
			r.muLast.Unlock()
			if seen {
				// Debounce suave para no spammear logs si vienen ráfagas de typing
				now := time.Now()
				r.muMap.Lock()
				lastT := r.lastTypingAt[chat]
				if now.Sub(lastT) >= r.typingDebounce {
					r.lastTypingAt[chat] = now
					r.muMap.Unlock()
					r.aggregator.Touch(chat)
				} else {
					r.muMap.Unlock()
				}
			}
		}
		// No aplicar filtros a typing; ya hicimos el touch.
		return
	}

	// 2) Resto de eventos → filtros normales y log informativo
	view := filters.EnvView{
		Direction: e.Direction,
		SenderJID: e.SenderJID,
		ChatJID:   e.ChatJID,
	}
	if !r.filterChain.Pass(view) {
		r.log.Info("filtered", "reason", "filter_chain_reject", "dir", e.Direction, "chat", e.ChatJID, "from", e.SenderJID)
		return
	}
	r.log.Info("event_any",
		"type", strings.TrimSpace(e.EventType),
		"chat", e.ChatJID,
		"from", e.SenderJID,
	)
}

func (r *SimpleRouter) replyWithTyping(chat, msg string) time.Duration {
	if r.typingFn != nil {
		_ = r.typingFn(chat, true, "text")
	}
	perChar := time.Duration(r.perCharMs) * time.Millisecond
	jitter := time.Duration(0)
	if r.jitterMs > 0 {
		jitter = time.Duration(rand.Intn(r.jitterMs)) * time.Millisecond
	}
	wait := r.baseWait + time.Duration(len([]rune(msg)))*perChar + jitter
	if wait > r.maxWait {
		wait = r.maxWait
	}
	time.Sleep(wait)
	if r.sendFn != nil {
		_ = r.sendFn(chat, msg)
	}
	if r.typingFn != nil {
		time.Sleep(r.typingPause)
		_ = r.typingFn(chat, false, "text")
	}
	return wait
}

// Heurística amplia para detectar "usuario está escribiendo"
func isTypingEvent(e Envelope) bool {
	et := strings.ToLower(strings.TrimSpace(e.EventType))
	if et == "chat_presence" {
		if e.Extra != nil {
			if v, ok := e.Extra["state"]; ok {
				if s, ok2 := v.(string); ok2 {
					s = strings.ToLower(strings.TrimSpace(s))
					return s == "composing" || s == "typing" || s == "recording"
				}
			}
		}
	}
	if et == "typing" || et == "presence" {
		if e.Extra != nil {
			if v, ok := e.Extra["state"]; ok {
				if s, ok2 := v.(string); ok2 {
					s = strings.ToLower(strings.TrimSpace(s))
					return s == "composing" || s == "typing" || s == "recording"
				}
			}
			if v, ok := e.Extra["typing"]; ok {
				if b, ok2 := v.(bool); ok2 && b {
					return true
				}
				if s, ok2 := v.(string); ok2 && (strings.EqualFold(s, "true") || s == "1") {
					return true
				}
			}
		}
	}
	rt := strings.ToLower(strings.TrimSpace(e.ReceiptType))
	return rt == "composing" || rt == "typing"
}

//
// =======================
// Helpers de perfil/NDJSON + Persistencia
// =======================

func outboxBase() string {
	if v := strings.TrimSpace(os.Getenv("OUTBOX_BASE")); v != "" {
		return v
	}
	return "outbox"
}

func sanitizePathPart(s string) string {
	s = strings.TrimSpace(s)
	var b strings.Builder
	for _, r := range s {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '.' || r == '_' || r == '-' {
			b.WriteRune(r)
		} else {
			b.WriteRune('_')
		}
	}
	if b.Len() == 0 {
		return "unknown"
	}
	return b.String()
}

func ndjsonContactPath(chatJID string) string {
	return filepath.Join(outboxBase(), "contacts", sanitizePathPart(chatJID)+".ndjson")
}
func ndjsonGroupPath(groupJID string) string {
	return filepath.Join(outboxBase(), "groups", sanitizePathPart(groupJID)+".ndjson")
}

// ===== Persistencia de perfiles (por ChatJID) =====

func profilesBase() string {
	return filepath.Join(outboxBase(), "profiles")
}

func profilePathFor(chatJID string) string {
	return filepath.Join(profilesBase(), sanitizePathPart(chatJID)+".json")
}

// Carga un perfil desde disco si existe
func loadProfileFromDisk(chatJID string) (*Profile, error) {
	path := profilePathFor(chatJID)
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var p Profile
	if err := json.Unmarshal(b, &p); err != nil {
		return nil, err
	}
	return &p, nil
}
func persistProfileSnapshotByChat(p *Profile, chatJID string) {
	if strings.TrimSpace(chatJID) == "" || p == nil {
		return
	}
	_ = os.MkdirAll(profilesBase(), 0o755)

	// Copia defensiva para serializar
	cp := *p

	b, _ := json.MarshalIndent(&cp, "", "  ")
	b = bytes.ReplaceAll(b, []byte(`\u0026`), []byte("&")) // ← one-liner: guarda URLs con & literal
	path := profilePathFor(chatJID)
	tmp := path + ".tmp"
	_ = os.WriteFile(tmp, b, 0o644)

	_ = os.Rename(tmp, path) // escritura atómica
}

// Mantiene solo los últimos N elementos (cola simple)
func keepLastN[T any](s []T, n int) []T {
	if n <= 0 || len(s) <= n {
		return s
	}
	return s[len(s)-n:]
}

// Extrae strings seguros desde map[string]any
func strFromMap(m map[string]any, key string) string {
	if m == nil {
		return ""
	}
	if v, ok := m[key]; ok {
		if s, ok2 := v.(string); ok2 {
			return s
		}
	}
	return ""
}

func u64FromMap(m map[string]any, key string) uint64 {
	if m == nil {
		return 0
	}
	v, ok := m[key]
	if !ok || v == nil {
		return 0
	}
	switch t := v.(type) {
	case float64:
		if t < 0 {
			return 0
		}
		return uint64(t)
	case int:
		if t < 0 {
			return 0
		}
		return uint64(t)
	case int64:
		if t < 0 {
			return 0
		}
		return uint64(t)
	case uint64:
		return t
	case string:
		if s := strings.TrimSpace(t); s != "" {
			if n, err := strconv.ParseUint(s, 10, 64); err == nil {
				return n
			}
		}
	}
	return 0
}

func u32FromMap(m map[string]any, key string) uint32 {
	return uint32(u64FromMap(m, key))
}

// Añade una entrada de media al perfil y persiste snapshot.
// maxPerDir limita el largo de cada arreglo (In/Out) para no crecer infinito.
func (r *SimpleRouter) appendMedia(chatKey string, e Envelope, maxPerDir int) {
	if strings.TrimSpace(chatKey) == "" {
		return
	}
	// sin media no hay nada que guardar
	if e.Media == nil {
		return
	}
	typ := strings.ToLower(strings.TrimSpace(strFromMap(e.Media, "type")))
	if typ == "" {
		return
	}

	p := r.getOrCreateProfileByKey(chatKey)
	if p == nil {
		return
	}

	entry := MediaEntry{
		Direction: strings.ToLower(strings.TrimSpace(e.Direction)),
		ChatJID:   chatKey,
		SenderJID: e.SenderJID,
		MessageID: e.MessageID,
		Type:      typ,
		Mimetype:  strings.TrimSpace(strFromMap(e.Media, "mimetype")),
		Title:     strings.TrimSpace(strFromMap(e.Media, "title")),
		URL:       strings.TrimSpace(strFromMap(e.Media, "url")),
		Caption:   strings.TrimSpace(e.Text),
		At:        time.Now(),

		// 🧷 Ticket completo para descargas/verificación posteriores
		DirectPath:       strings.TrimSpace(strFromMap(e.Media, "direct_path")),
		MediaKeyB64:      strings.TrimSpace(strFromMap(e.Media, "media_key_b64")),
		FileSHA256B64:    strings.TrimSpace(strFromMap(e.Media, "file_sha256_b64")),
		FileEncSHA256B64: strings.TrimSpace(strFromMap(e.Media, "file_enc_sha256_b64")),
		FileLength:       u64FromMap(e.Media, "file_length"),
		Seconds:          u32FromMap(e.Media, "seconds"),
	}

	// si At viene en Envelope.At, úsalo
	if t, err := time.Parse(time.RFC3339, strings.TrimSpace(e.At)); err == nil {
		entry.At = t
	}

	r.muProf.Lock()
	if entry.Direction == "out" {
		p.Media.Out = append(p.Media.Out, entry)
		p.Media.Out = keepLastN(p.Media.Out, maxPerDir)
	} else {
		p.Media.In = append(p.Media.In, entry)
		p.Media.In = keepLastN(p.Media.In, maxPerDir)
	}
	// snapshot defensivo
	cp := *p
	r.muProf.Unlock()

	persistProfileSnapshotByChat(&cp, chatKey)
}

// ===== Gestión en memoria por KEY (ChatJID) =====

// Getter que usa ChatJID (o key) como índice de r.profiles
func (r *SimpleRouter) getOrCreateProfileByKey(key string) *Profile {
	key = strings.TrimSpace(key)
	if key == "" {
		return nil
	}
	r.muProf.Lock()
	if p, ok := r.profiles[key]; ok {
		r.muProf.Unlock()
		return p
	}
	// Intentar rehidratar desde disco (fuera de lock para evitar bloquear I/O largo)
	r.muProf.Unlock()
	if pDisk, err := loadProfileFromDisk(key); err == nil && pDisk != nil {
		r.muProf.Lock()
		r.profiles[key] = pDisk
		r.muProf.Unlock()
		return pDisk
	}
	// No había en disco -> crear nuevo
	now := time.Now()
	p := &Profile{
		SenderJID: key,
		Lang:      "es",
		Tier:      "free",
		Tags:      map[string]string{},
		FirstSeen: now,
		LastConn:  now,
	}
	r.muProf.Lock()
	r.profiles[key] = p
	r.muProf.Unlock()
	return p
}

func (r *SimpleRouter) touchProfileFromInbound(e Envelope) {
	// Clave principal: ChatJID (para 1:1 y grupos)
	key := strings.TrimSpace(e.ChatJID)
	if key == "" {
		// Fallback extremo a sender si no viene chat
		key = strings.TrimSpace(e.SenderJID)
	}
	p := r.getOrCreateProfileByKey(key)
	if p == nil {
		return
	}

	now := time.Now()
	r.muProf.Lock()

	p.LastConn = now
	p.LastChat = e.ChatJID
	p.LastText = e.Text
	p.Metrics.MsgIn++
	p.Metrics.LastMsgAt = now
	p.Metrics.LastMsgID = e.MessageID

	day := now.Format("2006-01-02")
	if p.Metrics.StreakLastDay != day {
		if p.Metrics.StreakLastDay == now.Add(-24*time.Hour).Format("2006-01-02") {
			p.Metrics.StreakDays++
		} else {
			p.Metrics.StreakDays = 1
		}
		p.Metrics.StreakLastDay = day
	}

	// rutas NDJSON
	if strings.TrimSpace(e.ChatJID) != "" && !strings.HasSuffix(e.ChatJID, "@g.us") {
		p.Tags["out.contacts_ndjson"] = ndjsonContactPath(e.ChatJID)
	}
	if strings.Contains(e.ChatJID, "@g.us") {
		p.Tags["out.group."+e.ChatJID] = ndjsonGroupPath(e.ChatJID)
	}

	// Copia para persist fuera del lock
	cp := *p
	r.muProf.Unlock()

	// Persistir snapshot por ChatJID
	persistProfileSnapshotByChat(&cp, key)
}

func (r *SimpleRouter) incOutboundFor(chatKey string) {
	// chatKey es ChatJID (1:1 o grupo)
	if chatKey == "" {
		return
	}
	p := r.getOrCreateProfileByKey(chatKey)
	now := time.Now()
	r.muProf.Lock()
	p.Metrics.MsgOut++
	p.Metrics.LastMsgAt = now
	// Copia para persist fuera del lock
	cp := *p
	r.muProf.Unlock()

	// Persistir snapshot por ChatJID
	persistProfileSnapshotByChat(&cp, chatKey)
}

//
// =======================
// Integración con Backend BOB (Kevin)
// =======================
//

func callBOBBackend(fromPhone string, message string, logger jlog) string {
	sessionId := "wa-" + fromPhone

	payload := map[string]string{
		"sessionId": sessionId,
		"message":   message,
		"channel":   "whatsapp",
	}
	jsonData, _ := json.Marshal(payload)

	resp, err := http.Post(
		"http://localhost:3000/api/chat/message",
		"application/json",
		bytes.NewBuffer(jsonData),
	)
	if err != nil {
		logger.Warn("bob_backend_error", "err", err)
		return "Lo siento, hubo un error procesando tu mensaje."
	}
	defer resp.Body.Close()

	var result map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		logger.Warn("bob_backend_decode_error", "err", err)
		return "Error procesando la respuesta."
	}

	if reply, ok := result["reply"].(string); ok {
		// Log del lead score si viene
		if score, ok2 := result["leadScore"].(float64); ok2 {
			if category, ok3 := result["category"].(string); ok3 {
				logger.Info("bob_backend_reply",
					"from", fromPhone,
					"score", int(score),
					"category", category,
					"reply_len", len([]rune(reply)),
				)
			}
		}
		return reply
	}

	return "No se pudo obtener respuesta del sistema."
}

//
// =======================
// HTTP client helpers → engine
// =======================
//

var httpc = &http.Client{Timeout: 5 * time.Second}

func postJSON(url string, body any) (*http.Response, error) {
	b, _ := json.Marshal(body)
	req, _ := http.NewRequest(http.MethodPost, url, bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	return httpc.Do(req)
}

func makeSendFn(url string, log jlog) func(to, msg string) error {
	if strings.TrimSpace(url) == "" {
		return nil
	}
	return func(to, msg string) error {
		payload := map[string]any{
			"recipient": to,
			"message":   msg,
		}
		resp, err := postJSON(url, payload)
		if err != nil {
			log.Warn("send_error", "err", err)
			return err
		}
		defer resp.Body.Close()
		if resp.StatusCode/100 != 2 {
			log.Warn("send_non_2xx", "code", resp.StatusCode)
		}
		return nil
	}
}

func makeTypingFn(url string, log jlog) func(chat string, typing bool, media string) error {
	if strings.TrimSpace(url) == "" {
		return nil
	}
	return func(chat string, typing bool, media string) error {
		payload := map[string]any{
			"recipient": chat,
			"typing":    typing,
			"media":     media,
		}
		resp, err := postJSON(url, payload)
		if err != nil {
			log.Warn("typing_error", "err", err)
			return err
		}
		defer resp.Body.Close()
		if resp.StatusCode/100 != 2 {
			log.Warn("typing_non_2xx", "code", resp.StatusCode)
		}
		return nil
	}
}

// Nota: dejamos 'sender' opcional. El batch usa sender vacío y los grupos lo envían explícito.
func makeMarkReadFn(url string, log jlog) func(chat, sender string, ids []string) error {
	if strings.TrimSpace(url) == "" {
		return nil
	}
	return func(chat, sender string, ids []string) error {
		if chat == "" || len(ids) == 0 {
			return nil
		}
		payload := map[string]any{
			"recipient":    chat,
			"message_ids":  ids,
			"receipt_type": "read",
		}
		if strings.TrimSpace(sender) != "" {
			payload["sender"] = sender
		}
		resp, err := postJSON(url, payload)
		if err != nil {
			log.Warn("markread_error", "err", err)
			return err
		}
		defer resp.Body.Close()
		if resp.StatusCode/100 != 2 {
			log.Warn("markread_non_2xx", "code", resp.StatusCode)
		}
		return nil
	}
}

//
// =======================
// HTTP server
// =======================
//

func init() {
	rand.Seed(time.Now().UnixNano())
}

func main() {
	_ = godotenv.Load("/home/ivnx/labs/bob-hackathon/bot/.env")
	cfg := config.Load()
	log.Println("DEBUG secret_len:", len(cfg.WebhookSecret))

	// Mapea lo que usa el server
	addr := cfg.ServerAddr
	secret := cfg.WebhookSecret
	requireSig := cfg.ServerRequireSig
	bodyLimit := cfg.ServerBodyLimit
	tsSkew := cfg.ServerTSSkew
	logJSON := cfg.ServerLogJSON
	dedupeWindow := cfg.ServerDedupeWindow
	enableTimestamp := cfg.ServerUseTimestamp
	allowNoSecretDev := cfg.ServerAllowNoSecretDev

	// Integración hacia engine
	engineSendURL := cfg.ServerEngineSendURL
	engineTypingURL := cfg.ServerEngineTypingURL
	engineMarkReadURL := cfg.ServerEngineMarkReadURL // p. ej. http://127.0.0.1:8080/api/markread

	logger := jlog{json: logJSON}

	if requireSig && secret == "" && !allowNoSecretDev {
		logger.Error("WH_WEBHOOK_SECRET required (prod mode)")
		os.Exit(1)
	}

	// Helpers hacia engine
	sendFn := makeSendFn(engineSendURL, logger)
	typingFn := makeTypingFn(engineTypingURL, logger)
	markReadFn := makeMarkReadFn(engineMarkReadURL, logger)

	// Reglas (engine de negocio)
	var eng *rules.Engine
	switch strings.ToLower(strings.TrimSpace(cfg.RulesMode)) {
	case "code", "":
		eng = rules.NewEngine(rules.Builtin())
	default:
		eng = rules.NewEngine(rules.Builtin())
	}

	// Cadena de filtros (pkg/filters)
	chain := filters.Chain{
		Filters: []filters.Filter{
			filters.NotOut{},
			filters.RequireSender{},
			// agrega más filtros aquí…
		},
	}

	// Router sin cooldown, con pre-pausa
	router := NewSimpleRouter(
		logger,
		sendFn,
		typingFn,
		cfg.TypingPauseAfter,
		cfg.ReplyBaseWait,
		cfg.ReplyPerCharMs,
		cfg.ReplyJitterMs,
		cfg.ReplyMaxWait,
		cfg.PreReplyDelay,
		eng,
		nil,
		chain,
	)
	aggWindow := cfg.AggWindow
	if aggWindow <= 0 {
		aggWindow = 3 * time.Second
	}

	onFlush := func(chat string, count int) {
		// Pequeña pre-pausa separada de la ventana
		time.Sleep(cfg.PreReplyDelay)
		// Recupera el último envelope memorizado
		router.muLast.Lock()
		env, ok := router.lastByChat[chat]
		router.muLast.Unlock()

		if ok && strings.TrimSpace(env.Text) != "" {
			// Llamar al backend BOB de Kevin en vez del engine de reglas
			reply := callBOBBackend(env.SenderJID, env.Text, logger)

			if strings.TrimSpace(reply) != "" {
				wait := router.replyWithTyping(chat, reply)
				logger.Info("reply_bob_backend",
					"chat", chat,
					"count", count,
					"reply_len", len([]rune(reply)),
					"reply_preview", previewText(reply, maxLogText),
					"t_pre_delay_ms", cfg.PreReplyDelay.Milliseconds(),
					"t_typing_ms", wait.Milliseconds(),
				)
				return
			}
		}
		// Fallback si no hubo respuesta del backend
		msg := "Llegaron " + strconv.Itoa(count) + " mensaje(s) en la ventana."
		router.replyWithTyping(chat, msg)
	}
	// Callback de onReset para logs explícitos
	onReset := func(chat string, reason string, count int, win time.Duration) {
		secs := int(win / time.Second)
		switch reason {
		case "start":
			logger.Info("agg_window_start",
				"msg", "ventana de espera de mensajes inicializada, esperando",
				"chat", chat,
				"ventana_seg", secs,
				"mensajes_acumulados", count,
			)
		case "message":
			logger.Info("agg_window_reset_message",
				"msg", "detectado nuevo mensaje dentro de la ventana de espera de mensajes, reiniciando tiempo de espera",
				"chat", chat,
				"ventana_seg", secs,
				"mensajes_acumulados", count,
			)
		case "typing":
			logger.Info("agg_window_reset_typing",
				"msg", "detectado que usuario está escribiendo; ventana de espera reiniciada",
				"chat", chat,
				"ventana_seg", secs,
				"mensajes_acumulados", count,
			)
		}
	}

	agg := pipeline.NewAggregator(aggWindow, onFlush, onReset)
	router.aggregator = agg

	ded := newDeduper(dedupeWindow)
	mux := http.NewServeMux()

	// Health endpoints
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})
	mux.HandleFunc("/readyz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ready"))
	})
	mux.HandleFunc("/debug/profiles", func(w http.ResponseWriter, _ *http.Request) {
		router.muProf.Lock()
		defer router.muProf.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(router.profiles)
	})
	mux.HandleFunc("/", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("whserver up"))
	})

	// Webhook principal
	mux.HandleFunc("/wh", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}

		// Body limit + lectura
		r.Body = http.MaxBytesReader(w, r.Body, bodyLimit)
		body, err := io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, "bad request: body read", http.StatusBadRequest)
			return
		}

		// Timestamp opcional
		if enableTimestamp {
			if err := verifyTimestamp(r.Header.Get("X-Whatsbot-Timestamp"), tsSkew); err != nil {
				http.Error(w, "invalid timestamp", http.StatusUnauthorized)
				return
			}
		}

		// Firma HMAC (si está habilitada)
		if requireSig {
			if !verifySignature(secret, body, r.Header.Get("X-Whatsbot-Signature")) {
				http.Error(w, "invalid signature", http.StatusUnauthorized)
				return
			}
		} else if secret == "" {
			logger.Warn("signature_not_required_dev_mode")
		}

		// Decodificar envelope
		var env Envelope
		if err := json.Unmarshal(body, &env); err != nil {
			http.Error(w, "invalid json", http.StatusBadRequest)
			return
		}

		// Log básico del evento
		logger.Info(
			"event",
			"type", strings.TrimSpace(env.EventType),
			"dir", strings.TrimSpace(env.Direction),
			"chat", env.ChatJID,
			"sender", env.SenderJID,
			"msg_id", strings.TrimSpace(env.MessageID),
		)

		// Dedupe por message_id (solo para mensajes reales)
		if env.EventType == "message" && ded.Seen(env.MessageID) {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"ok":true,"dup":true}`))
			return
		}

		// ACK inmediato
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"ok":true}`))

		// Procesamiento async
		go func(e Envelope) {
			ctx := context.Background()
			if e.EventType == "message" && !strings.EqualFold(e.Direction, "out") && strings.TrimSpace(e.MessageID) != "" {
				mrStart := time.Now()
				var err error
				if strings.Contains(e.ChatJID, "@g.us") {
					err = markReadFn(e.ChatJID, e.SenderJID, []string{e.MessageID})
				} else {
					err = markReadFn(e.ChatJID, "", []string{e.MessageID})
				}
				if err != nil {
					logger.Warn("markread_fail", "chat", e.ChatJID, "msg_id", e.MessageID, "err", err.Error())
				} else {
					logger.Info("markread_ok", "chat", e.ChatJID, "msg_id", e.MessageID, "t_ms", time.Since(mrStart).Milliseconds())
				}
			}

			switch e.EventType {
			case "message":
				router.OnMessage(ctx, e)
			case "receipt":
				router.OnReceipt(ctx, e)
			default:
				router.OnAny(ctx, e)
			}
		}(env)
	})

	// Server + graceful shutdown
	srv := &http.Server{
		Addr:         addr,
		Handler:      mux,
		ReadTimeout:  5 * time.Second,
		WriteTimeout: 5 * time.Second,
		IdleTimeout:  60 * time.Second,
	}

	go func() {
		logger.Info("WH listening", "addr", addr)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			logger.Error("server_error", "err", err)
			os.Exit(1)
		}
	}()

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM)
	<-stop

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = srv.Shutdown(ctx)
	logger.Info("graceful shutdown complete")
}
