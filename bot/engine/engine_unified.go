// engine_unified.go
package engine

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"math/rand"
	"mime"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"syscall"
	"time"

	_ "github.com/mattn/go-sqlite3"
	"github.com/mdp/qrterminal"

	"golang.org/x/term"
	"golang.org/x/time/rate"

	// WhatsMeow core
	wm "go.mau.fi/whatsmeow"
	waProto "go.mau.fi/whatsmeow/binary/proto"
	"go.mau.fi/whatsmeow/store/sqlstore"
	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"
	waLog "go.mau.fi/whatsmeow/util/log"

	"google.golang.org/protobuf/proto"
)

//
// =========================
// 0) Tipos y configuración
// =========================
//

var ErrUnsupported = errors.New("unsupported capability")

type Capabilities struct {
	Status         bool // stories
	BroadcastLists bool
	Calls          bool
}

// ===== Forwarding (Folder/Webhook) =====
type ForwardMode string

const (
	ForwardOff    ForwardMode = "off"
	ForwardFolder ForwardMode = "folder"
)

type ForwardingConfig struct {
	Mode         ForwardMode
	ContextDepth int // mantenido por compatibilidad (no se incluye en NDJSON)
	OutFolder    string
	ExtraParams  map[string]any
	Webhook      WebhookConfig
}

type WebhookConfig struct {
	Enabled bool
	URL     string
	Secret  string
	Headers map[string]string
}

type Config struct {
	DBPath             string
	MsgDBPath          string
	EnableStatus       bool
	BackupEvery        time.Duration
	MaxConnAttempts    int
	ReconnectBaseDelay time.Duration
	HTTPPort           int

	Forward ForwardingConfig
}

type Engine struct {
	client       *wm.Client
	caps         Capabilities
	cfg          Config
	logger       waLog.Logger
	store        Store
	msgStore     *MessageStore
	limiterSend  *rate.Limiter
	limiterMedia *rate.Limiter
	limiterStat  *rate.Limiter

	fileSink *FlatSink
}

//
// =========================
// 1) Store (session + app)
// =========================
//

type Store interface {
	LoadSession(ctx context.Context) error
	SaveSession(ctx context.Context) error
	LoadAppState(ctx context.Context) error
	SaveAppState(ctx context.Context) error
	RunBackup(ctx context.Context) error
}

// Implementación simple de mensajes (historial)
type MessageStore struct{ db *sql.DB }

func NewMessageStore(path string) (*MessageStore, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, fmt.Errorf("create store dir: %w", err)
	}
	db, err := sql.Open("sqlite3", "file:"+path+"?_foreign_keys=on")
	if err != nil {
		return nil, err
	}
	_, err = db.Exec(`
	CREATE TABLE IF NOT EXISTS chats (
		jid TEXT PRIMARY KEY,
		name TEXT,
		last_message_time TIMESTAMP
	);
	CREATE TABLE IF NOT EXISTS messages (
		id TEXT,
		chat_jid TEXT,
		sender TEXT,
		content TEXT,
		timestamp TIMESTAMP,
		is_from_me BOOLEAN,
		media_type TEXT,
		filename TEXT,
		url TEXT,
		media_key BLOB,
		file_sha256 BLOB,
		file_enc_sha256 BLOB,
		file_length INTEGER,
		PRIMARY KEY (id, chat_jid),
		FOREIGN KEY (chat_jid) REFERENCES chats(jid)
	);
	`)
	if err != nil {
		_ = db.Close()
		return nil, err
	}
	return &MessageStore{db: db}, nil
}
func b64(b []byte) string {
	if len(b) == 0 {
		return ""
	}
	return base64.StdEncoding.EncodeToString(b)
}
func (s *MessageStore) ensureChat(jid string) error {
	_, err := s.db.Exec(`INSERT OR IGNORE INTO chats (jid) VALUES (?)`, jid)
	return err
}

func (s *MessageStore) Close() error { return s.db.Close() }

// Guarda un mensaje entrante/saliente (IN/OUT) para dar contexto a la IA
func (s *MessageStore) SaveMessage(chatJID, id, sender, content string, ts time.Time, isFromMe bool, mediaType, filename, url sql.NullString) error {
	if err := s.ensureChat(chatJID); err != nil {
		return err
	}
	_, err := s.db.Exec(`
		INSERT OR REPLACE INTO messages
		(id, chat_jid, sender, content, timestamp, is_from_me, media_type, filename, url)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		id, chatJID, sender, content, ts, isFromMe, mediaType, filename, url,
	)
	return err
}

func (s *MessageStore) GetRecentMessages(chatJID string, limit int) ([]map[string]any, error) {
	rows, err := s.db.Query(`
		SELECT id, sender, content, timestamp, is_from_me, media_type, filename, url
		FROM messages
		WHERE chat_jid = ?
		ORDER BY timestamp DESC
		LIMIT ?`, chatJID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []map[string]any
	for rows.Next() {
		var id, sender, content, mediaType, filename, url sql.NullString
		var ts time.Time
		var isFromMe bool
		if err := rows.Scan(&id, &sender, &content, &ts, &isFromMe, &mediaType, &filename, &url); err != nil {
			return nil, err
		}
		out = append(out, map[string]any{
			"id": id.String, "sender": sender.String, "content": content.String,
			"timestamp": ts.UTC().Format(time.RFC3339), "is_from_me": isFromMe,
			"media_type": mediaType.String, "filename": filename.String, "url": url.String,
		})
	}
	// invertimos para cronología natural (antiguo -> nuevo)
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}
	return out, nil
}

//
// ===============================
// 2) Decoradores (RL + retries)
// ===============================
//

type SendFunc func(ctx context.Context, to types.JID, payload any) (string, error)

func WithRateLimit(lim *rate.Limiter, next SendFunc) SendFunc {
	return func(ctx context.Context, to types.JID, payload any) (string, error) {
		if err := lim.Wait(ctx); err != nil {
			return "", err
		}
		return next(ctx, to, payload)
	}
}

func WithRetry(attempts int, delay time.Duration, next SendFunc) SendFunc {
	return func(ctx context.Context, to types.JID, payload any) (string, error) {
		var id string
		var err error
		d := delay
		for i := 0; i < attempts; i++ {
			id, err = next(ctx, to, payload)
			if err == nil {
				return id, nil
			}
			select {
			case <-time.After(d):
			case <-ctx.Done():
				return "", ctx.Err()
			}
			if d < 5*time.Second {
				d *= 2
			}
		}
		return id, err
	}
}

//
// ========================================
// 3) Session Manager (checks “recursivos”)
// ========================================
//

func (e *Engine) CheckSession(ctx context.Context) error {
	dbLog := waLog.Stdout("Database", "INFO", term.IsTerminal(int(os.Stdout.Fd())))
	container, err := sqlstore.New(ctx, "sqlite3", "file:"+e.cfg.DBPath+"?_foreign_keys=on", dbLog)
	if err != nil {
		return err
	}
	device, err := container.GetFirstDevice(ctx)
	if err == sql.ErrNoRows {
		device = container.NewDevice()
	} else if err != nil {
		return err
	}
	e.client = wm.NewClient(device, e.logger)
	if e.client == nil {
		return errors.New("failed to create client")
	}

	if e.client.Store.ID == nil {
		qrChan, _ := e.client.GetQRChannel(ctx)
		if err := e.client.Connect(); err != nil {
			return err
		}
		for evt := range qrChan {
			if evt.Event == "code" {
				qrterminal.GenerateHalfBlock(evt.Code, qrterminal.L, os.Stdout)
			} else if evt.Event == "success" {
				break
			}
		}
	} else {
		if err := e.client.Connect(); err != nil {
			return err
		}
	}

	if err := e.connectWithRetry(ctx, e.cfg.MaxConnAttempts, e.cfg.ReconnectBaseDelay); err != nil {
		return err
	}
	_ = e.client.SendPresence(ctx, types.PresenceAvailable)
	e.caps = Capabilities{
		Status:         e.cfg.EnableStatus && e.detectStatusSupport(ctx),
		BroadcastLists: false,
		Calls:          false,
	}
	return nil
}

func (e *Engine) connectWithRetry(ctx context.Context, attempts int, delay time.Duration) error {
	var err error
	for i := 0; i < attempts; i++ {
		if e.client.IsConnected() {
			return nil
		}
		if err = e.client.Connect(); err == nil && e.client.IsConnected() {
			return nil
		}
		select {
		case <-time.After(delay + time.Duration(i)*250*time.Millisecond):
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return err
}

//nolint:unusedparams
func (e *Engine) detectStatusSupport(_ context.Context) bool { return false }

//
// ====================
// 4) Logs con ANSI
// ====================
//

func useANSI() bool { return term.IsTerminal(int(os.Stdout.Fd())) }

const (
	ansiReset   = "\x1b[0m"
	ansiBold    = "\x1b[1m"
	ansiIN      = "\x1b[38;5;39m"
	ansiOUT     = "\x1b[38;5;208m"
	ansiRCPT    = "\x1b[38;5;70m"
	ansiWARN    = "\x1b[38;5;196m"
	ansiPRES    = "\x1b[38;5;147m"
	ansiGROUP   = "\x1b[38;5;214m"
	ansiSTATE   = "\x1b[38;5;45m"
	ansiDEFAULT = "\x1b[37m"
)

func colorize(c, s string) string {
	if !useANSI() || s == "" {
		return s
	}
	return c + s + ansiReset
}

func short(s string, n int) string {
	s = strings.ReplaceAll(s, "\n", " ")
	if len([]rune(s)) <= n {
		return s
	}
	r := []rune(s)
	return string(r[:n]) + "…"
}

func (e *Engine) humanInfof(format string, args ...any) {
	if e.logger != nil {
		e.logger.Infof(format, args...)
	}
}
func (e *Engine) humanWarnf(format string, args ...any) {
	if e.logger != nil {
		e.logger.Warnf(format, args...)
	}
}

func kindOfChat(j types.JID) string {
	if j.Server == "g.us" {
		return "GRUPO"
	}
	if j.String() == "status@broadcast" {
		return "STATUS"
	}
	return "PRIVADO"
}
func storageChatJID(s string) string {
    if s == "" {
        return ""
    }
    if strings.HasSuffix(s, "@lid") {
        return strings.TrimSuffix(s, "@lid") + "@s.whatsapp.net"
    }
    return s
}

// ===== Normalización de JID =====
// Preserva @lid/@s.whatsapp.net/@g.us/status. Si no hay '@', asume 1:1 clásico.
func canonicalChatJID(s string) string {
    if s == "" {
        return ""
    }
    s = strings.TrimSpace(s)
    if s == "status@broadcast" {
        return s
    }
    if strings.Contains(s, "@") {
        return s
    }
    return s + "@s.whatsapp.net"
}

// ===== Dedupe simple para RCPT =====
var rcptSeen = struct {
	mu sync.Mutex
	m  map[string]time.Time
}{m: map[string]time.Time{}}

func seenReceiptOnce(key string) bool {
	rcptSeen.mu.Lock()
	defer rcptSeen.mu.Unlock()
	now := time.Now()
	for k, t := range rcptSeen.m {
		if now.Sub(t) > 5*time.Second {
			delete(rcptSeen.m, k)
		}
	}
	if _, ok := rcptSeen.m[key]; ok {
		return true
	}
	rcptSeen.m[key] = now
	return false
}

//
// =======================
// 4.1) Flat NDJSON sink
// =======================
//

type FlatSink struct {
	base     string
	maxBytes int64
}

func NewFlatSink(base string, maxBytes int64) *FlatSink {
	if base == "" {
		base = "outbox"
	}
	return &FlatSink{base: base, maxBytes: maxBytes}
}

func (s *FlatSink) ensureDir(p string) error { return os.MkdirAll(p, 0o755) }

func sanitizePathPart(s string) string {
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

func hostID() string {
	h, _ := os.Hostname()
	return sanitizePathPart(h)
}

// decide categoría/archivo UNIFICANDO privados+status por contacto
func categoryAndFileNoDate(env *ForwardEnvelope) (category, filename string) {
	// 1) grupos
	if env.ChatJID != "" && (strings.HasSuffix(env.ChatJID, "@g.us") || strings.HasSuffix(env.ChatJID, "g.us")) {
		return "groups", sanitizePathPart(env.ChatJID) + ".ndjson"
	}
	// 2) status: usar el contacto (sender_jid)
	if env.ChatJID == "status@broadcast" && env.SenderJID != "" {
		return "contacts", sanitizePathPart(env.SenderJID) + ".ndjson"
	}
	// 3) privados: usar chat_jid (contacto)
	if env.ChatJID != "" {
		return "contacts", sanitizePathPart(env.ChatJID) + ".ndjson"
	}
	// 4) device/system
	switch env.EventType {
	case "connected", "logged_out", "history_sync", "offline_sync_completed", "events.IdentityChange":
		return "devices", hostID() + ".ndjson"
	default:
		return "system", sanitizePathPart(env.EventType) + ".ndjson"
	}
}

func (s *FlatSink) maybeRotate(path string) (string, error) {
	if s.maxBytes <= 0 {
		return path, nil
	}
	info, err := os.Stat(path)
	if err != nil {
		return path, nil
	}
	if info.Size() < s.maxBytes {
		return path, nil
	}
	for i := 1; i < 1000; i++ {
		next := fmt.Sprintf("%s.part%d", path, i)
		if _, err := os.Stat(next); errors.Is(err, os.ErrNotExist) {
			return next, nil
		}
	}
	return path, nil
}

func (s *FlatSink) Append(env *ForwardEnvelope, payload []byte) error {
	cat, file := categoryAndFileNoDate(env)
	base := filepath.Join(s.base, cat)
	if err := s.ensureDir(base); err != nil {
		return err
	}
	path := filepath.Join(base, file)
	path, _ = s.maybeRotate(path)

	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	if _, err := f.Write(payload); err != nil {
		return err
	}
	if _, err := f.Write([]byte("\n")); err != nil {
		return err
	}
	return nil
}

//
// ====================
// 5) Event Loop
// ====================
//

type Handlers struct {
	OnMessage     func(ctx context.Context, m *events.Message) error
	OnReceipt     func(ctx context.Context, r *events.Receipt) error
	OnPresence    func(ctx context.Context, p *events.Presence) error
	OnGroupUpdate func(ctx context.Context, g *events.GroupInfo) error
	OnStatus      func(ctx context.Context, s any) error
	OnError       func(ctx context.Context, err error)
}

// ===== Envelope estándar =====
type ForwardEnvelope struct {
	EventType   string         `json:"event_type"`
	Direction   string         `json:"direction,omitempty"` // "in" | "out"
	EventRaw    any            `json:"event_raw"`           // se nulifica al guardar
	ChatJID     string         `json:"chat_jid,omitempty"`
	SenderJID   string         `json:"sender_jid,omitempty"`
	ChatName    string         `json:"chat_name,omitempty"`
	MessageID   string         `json:"message_id,omitempty"`
	MessageIDs  []string       `json:"message_ids,omitempty"`
	ReceiptType string         `json:"receipt_type,omitempty"`
	Text        string         `json:"text,omitempty"`
	Media       map[string]any `json:"media,omitempty"`
	Extra       map[string]any `json:"extra,omitempty"`
	At          string         `json:"at"`
}

func (e *Engine) marshalEnvelopeForIO(env *ForwardEnvelope) ([]byte, error) {
	env.At = time.Now().UTC().Format(time.RFC3339)
	if env.Extra == nil && e.cfg.Forward.ExtraParams != nil {
		env.Extra = e.cfg.Forward.ExtraParams
	}
	slim := *env
	slim.EventRaw = nil
	return json.Marshal(&slim)
}

func (e *Engine) writeEnvelopeToFolder(env *ForwardEnvelope) error {
	if e.cfg.Forward.Mode != ForwardFolder {
		return nil
	}
	if e.fileSink == nil {
		return errors.New("file sink not initialized")
	}
	b, err := e.marshalEnvelopeForIO(env)
	if err != nil {
		return err
	}
	return e.fileSink.Append(env, b)
}

func (e *Engine) signBodyHMACSHA256(secret string, body []byte) string {
	if secret == "" {
		return ""
	}
	h := hmac.New(sha256.New, []byte(secret))
	h.Write(body)
	return "sha256=" + hex.EncodeToString(h.Sum(nil))
}

func (e *Engine) postJSONWithRetry(ctx context.Context, url string, secret string, headers map[string]string, payload any) error {
	body, _ := json.Marshal(payload)
	client := &http.Client{Timeout: 7 * time.Second}

	var lastErr error
	for i := 0; i < 3; i++ {
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
		if err != nil {
			return err
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-Whatsbot-Timestamp", time.Now().UTC().Format(time.RFC3339))
		for k, v := range headers {
			req.Header.Set(k, v)
		}
		if sig := e.signBodyHMACSHA256(secret, body); sig != "" {
			req.Header.Set("X-Whatsbot-Signature", sig)
		}

		resp, err := client.Do(req)
		if err != nil {
			lastErr = err
		} else {
			_ = resp.Body.Close()
			if resp.StatusCode >= 200 && resp.StatusCode < 300 {
				return nil
			}
			lastErr = fmt.Errorf("webhook non-2xx: %d", resp.StatusCode)
		}

		select {
		case <-time.After(time.Duration(250*(1<<i)) * time.Millisecond):
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return lastErr
}

func (e *Engine) sendEnvelopeToWebhook(_ context.Context, env *ForwardEnvelope) {
	if !e.cfg.Forward.Webhook.Enabled || e.cfg.Forward.Webhook.URL == "" {
		return
	}
	go func(en ForwardEnvelope) {
		b, err := e.marshalEnvelopeForIO(&en)
		if err != nil {
			if e.logger != nil {
				e.logger.Warnf("marshal envelope failed: %v", err)
			}
			return
		}
		if err := e.postJSONWithRetry(context.Background(),
			e.cfg.Forward.Webhook.URL,
			e.cfg.Forward.Webhook.Secret,
			e.cfg.Forward.Webhook.Headers,
			json.RawMessage(b),
		); err != nil && e.logger != nil {
			e.logger.Warnf("webhook post failed: %v", err)
		}
	}(*env)
}

// helper para forwardear OUT
func (e *Engine) forwardOutgoing(to types.JID, id, text, mt, mime, filename string) {
	env := &ForwardEnvelope{
		EventType: "message",
		Direction: "out",
		ChatJID:   canonicalChatJID(to.String()),
		SenderJID: "", // somos nosotros; opcional
		ChatName:  e.ResolveChatName(to, canonicalChatJID(to.String()), nil, ""),
		MessageID: id,
		Text:      text,
	}
	if mt != "" {
		m := map[string]any{"type": strings.ToLower(mt)}
		if mime != "" {
			m["mimetype"] = mime
		}
		if filename != "" {
			m["title"] = filename
		}
		env.Media = m
	}
	_ = e.writeEnvelopeToFolder(env)
	e.sendEnvelopeToWebhook(context.Background(), env)
}
func (e *Engine) forwardOutgoingWithMedia(to types.JID, id, text string, media map[string]any) {
	env := &ForwardEnvelope{
		EventType: "message",
		Direction: "out",
		ChatJID:   canonicalChatJID(to.String()),
		SenderJID: "",
		ChatName:  e.ResolveChatName(to, canonicalChatJID(to.String()), nil, ""),
		MessageID: id,
		Text:      text,
		Media:     media,
	}
	_ = e.writeEnvelopeToFolder(env)
	e.sendEnvelopeToWebhook(context.Background(), env)
}

func extractJIDFromIdentityChange(ev any) string {
	if ev == nil {
		return ""
	}
	val := reflect.ValueOf(ev)
	if val.Kind() == reflect.Ptr && !val.IsNil() {
		val = val.Elem()
	}
	if val.Kind() != reflect.Struct {
		return ""
	}
	tryOne := func(field string) string {
		f := val.FieldByName(field)
		if !f.IsValid() {
			return ""
		}
		if f.Kind() == reflect.Struct {
			m := f.MethodByName("String")
			if m.IsValid() {
				out := m.Call(nil)
				if len(out) == 1 {
					if s, ok := out[0].Interface().(string); ok {
						return canonicalChatJID(s)
					}
				}
			}
		}
		if f.Kind() == reflect.Slice && f.Len() > 0 {
			elem := f.Index(0)
			m := elem.MethodByName("String")
			if m.IsValid() {
				out := m.Call(nil)
				if len(out) == 1 {
					if s, ok := out[0].Interface().(string); ok {
						return canonicalChatJID(s)
					}
				}
			}
		}
		return ""
	}
	if s := tryOne("JID"); s != "" {
		return s
	}
	if s := tryOne("Chat"); s != "" {
		return s
	}
	if s := tryOne("JIDs"); s != "" {
		return s
	}
	if s := tryOne("Chats"); s != "" {
		return s
	}
	return ""
}

func (e *Engine) RunEventLoop(ctx context.Context, h Handlers) {
	e.client.AddEventHandler(func(evt interface{}) {
		switch v := evt.(type) {

		case *events.Message:
			// Si el mensaje es nuestro, trátalo como OUT y no dispares OnMessage
			if v.Info.IsFromMe {
				id := v.Info.ID
				to := v.Info.Chat
				txt := ""
				if v.Message != nil {
					switch {
					case v.Message.GetExtendedTextMessage() != nil && v.Message.GetExtendedTextMessage().GetText() != "":
						txt = v.Message.GetExtendedTextMessage().GetText()
					case v.Message.GetConversation() != "":
						txt = v.Message.GetConversation()
					}
				}

				// Si hay media, extrae el ticket como en IN y forwardea con media
				if m := v.Message; m != nil {
					var media map[string]any
					if im := m.GetImageMessage(); im != nil {
						media = map[string]any{
							"type":                "image",
							"mimetype":            im.GetMimetype(),
							"url":                 im.GetURL(),
							"direct_path":         im.GetDirectPath(),
							"file_length":         im.GetFileLength(),
							"media_key_b64":       b64(im.GetMediaKey()),
							"file_sha256_b64":     b64(im.GetFileSHA256()),
							"file_enc_sha256_b64": b64(im.GetFileEncSHA256()),
							"media_type":          "image",
						}
					} else if au := m.GetAudioMessage(); au != nil {
						media = map[string]any{
							"type":                "audio",
							"mimetype":            au.GetMimetype(),
							"seconds":             au.GetSeconds(),
							"url":                 au.GetURL(),
							"direct_path":         au.GetDirectPath(),
							"file_length":         au.GetFileLength(),
							"media_key_b64":       b64(au.GetMediaKey()),
							"file_sha256_b64":     b64(au.GetFileSHA256()),
							"file_enc_sha256_b64": b64(au.GetFileEncSHA256()),
							"media_type":          "audio",
						}
					} else if vi := m.GetVideoMessage(); vi != nil {
						media = map[string]any{
							"type":                "video",
							"mimetype":            vi.GetMimetype(),
							"url":                 vi.GetURL(),
							"direct_path":         vi.GetDirectPath(),
							"file_length":         vi.GetFileLength(),
							"media_key_b64":       b64(vi.GetMediaKey()),
							"file_sha256_b64":     b64(vi.GetFileSHA256()),
							"file_enc_sha256_b64": b64(vi.GetFileEncSHA256()),
							"media_type":          "video",
						}
					} else if doc := m.GetDocumentMessage(); doc != nil {
						media = map[string]any{
							"type":                "document",
							"mimetype":            doc.GetMimetype(),
							"title":               doc.GetTitle(),
							"url":                 doc.GetURL(),
							"direct_path":         doc.GetDirectPath(),
							"file_length":         doc.GetFileLength(),
							"media_key_b64":       b64(doc.GetMediaKey()),
							"file_sha256_b64":     b64(doc.GetFileSHA256()),
							"file_enc_sha256_b64": b64(doc.GetFileEncSHA256()),
							"media_type":          "document",
						}
					}
					if media != nil {
						e.forwardOutgoingWithMedia(to, id, txt, media)
					} else {
						e.forwardOutgoing(to, id, txt, "", "", "")
					}
				} else {
					e.forwardOutgoing(to, id, txt, "", "", "")
				}
				return
			}

			env := &ForwardEnvelope{EventType: "message", Direction: "in", EventRaw: v}
			msg := v.Message
			from := v.Info.Sender
			chat := v.Info.Chat

			env.ChatJID = canonicalChatJID(chat.String())
			env.SenderJID = from.String()
			env.ChatName = e.ResolveChatName(chat, env.ChatJID, v, v.Info.Sender.User)
			env.MessageID = v.Info.ID

			if msg != nil {
				switch {
				case msg.GetExtendedTextMessage() != nil && msg.GetExtendedTextMessage().GetText() != "":
					env.Text = msg.GetExtendedTextMessage().GetText()
				case msg.GetConversation() != "":
					env.Text = msg.GetConversation()
				case msg.GetImageMessage() != nil && msg.GetImageMessage().GetCaption() != "":
					env.Text = msg.GetImageMessage().GetCaption()
				case msg.GetVideoMessage() != nil && msg.GetVideoMessage().GetCaption() != "":
					env.Text = msg.GetVideoMessage().GetCaption()
				}

				// 🔐 Ticket completo por tipo
				if im := msg.GetImageMessage(); im != nil {
					env.Media = map[string]any{
						"type":                "image",
						"mimetype":            im.GetMimetype(),
						"url":                 im.GetURL(),
						"direct_path":         im.GetDirectPath(),
						"file_length":         im.GetFileLength(),
						"media_key_b64":       b64(im.GetMediaKey()),
						"file_sha256_b64":     b64(im.GetFileSHA256()),
						"file_enc_sha256_b64": b64(im.GetFileEncSHA256()),
						"media_type":          "image",
					}
				} else if au := msg.GetAudioMessage(); au != nil {
					env.Media = map[string]any{
						"type":                "audio",
						"mimetype":            au.GetMimetype(),
						"seconds":             au.GetSeconds(),
						"url":                 au.GetURL(),
						"direct_path":         au.GetDirectPath(),
						"file_length":         au.GetFileLength(),
						"media_key_b64":       b64(au.GetMediaKey()),
						"file_sha256_b64":     b64(au.GetFileSHA256()),
						"file_enc_sha256_b64": b64(au.GetFileEncSHA256()),
						"media_type":          "audio",
					}
				} else if vi := msg.GetVideoMessage(); vi != nil {
					env.Media = map[string]any{
						"type":                "video",
						"mimetype":            vi.GetMimetype(),
						"url":                 vi.GetURL(),
						"direct_path":         vi.GetDirectPath(),
						"file_length":         vi.GetFileLength(),
						"media_key_b64":       b64(vi.GetMediaKey()),
						"file_sha256_b64":     b64(vi.GetFileSHA256()),
						"file_enc_sha256_b64": b64(vi.GetFileEncSHA256()),
						"media_type":          "video",
					}
				} else if doc := msg.GetDocumentMessage(); doc != nil {
					env.Media = map[string]any{
						"type":                "document",
						"mimetype":            doc.GetMimetype(),
						"title":               doc.GetTitle(),
						"url":                 doc.GetURL(),
						"direct_path":         doc.GetDirectPath(),
						"file_length":         doc.GetFileLength(),
						"media_key_b64":       b64(doc.GetMediaKey()),
						"file_sha256_b64":     b64(doc.GetFileSHA256()),
						"file_enc_sha256_b64": b64(doc.GetFileEncSHA256()),
						"media_type":          "document",
					}
				}
			}

			if e.msgStore != nil {
				var mediaType, filename, url sql.NullString
				if msg.GetImageMessage() != nil {
					mediaType = sql.NullString{String: "image", Valid: true}
				} else if msg.GetAudioMessage() != nil {
					mediaType = sql.NullString{String: "audio", Valid: true}
				} else if msg.GetVideoMessage() != nil {
					mediaType = sql.NullString{String: "video", Valid: true}
				} else if msg.GetDocumentMessage() != nil {
					mediaType = sql.NullString{String: "document", Valid: true}
					filename = sql.NullString{String: msg.GetDocumentMessage().GetTitle(), Valid: msg.GetDocumentMessage().GetTitle() != ""}
				}
				ts := v.Info.Timestamp
				if ts.IsZero() {
					ts = time.Now()
				}
				_ = e.msgStore.SaveMessage(
					storageChatJID(env.ChatJID),
					env.MessageID,
					env.SenderJID,
					env.Text,
					ts,
					false,
					mediaType, filename, url,
				)
			}

			{
				k := kindOfChat(v.Info.Chat)
				who := env.SenderJID
				txt := env.Text
				mt := ""
				if env.Media != nil {
					if t, ok := env.Media["type"].(string); ok && t != "" {
						mt = strings.ToUpper(t)
					}
				}
				prefix := colorize(ansiIN, "[IN]") + " "
				if env.ChatJID == "status@broadcast" {
					e.humanInfof(prefix+"[STATUS] De %s | ID:%s | %s",
						colorize(ansiBold, who),
						env.MessageID,
						map[bool]string{true: "CON MEDIA", false: "SIN MEDIA"}[mt != ""],
					)
				} else if mt != "" {
					e.humanInfof(prefix+"[%s] Chat:%s | De:%s | ID:%s | MEDIA:%s | Caption:\"%s\"",
						k, colorize(ansiBold, env.ChatJID), colorize(ansiBold, who),
						env.MessageID, mt, short(txt, 60))
				} else {
					e.humanInfof(prefix+"[%s] Chat:%s | De:%s | ID:%s | Texto:\"%s\"",
						k, colorize(ansiBold, env.ChatJID), colorize(ansiBold, who),
						env.MessageID, short(txt, 80))
				}
			}

			if err := e.writeEnvelopeToFolder(env); err != nil && h.OnError != nil {
				h.OnError(ctx, err)
			}
			e.sendEnvelopeToWebhook(ctx, env)

			if h.OnMessage != nil {
				_ = h.OnMessage(ctx, v)
			}

		case *events.Receipt:
			env := &ForwardEnvelope{
				EventType:   "receipt",
				EventRaw:    v,
				ChatJID:     canonicalChatJID(v.Chat.String()),
				SenderJID:   v.MessageSender.String(),
				MessageIDs:  append([]string{}, v.MessageIDs...),
				ReceiptType: string(v.Type),
			}
			key := env.ChatJID + "|" + env.ReceiptType + "|" + strings.Join(env.MessageIDs, ",")
			if seenReceiptOnce(key) {
				break
			}
			{
				k := kindOfChat(v.Chat)
				tag := map[string]string{"": "ENTREGADO", "read": "LEÍDO", "played": "REPRODUCIDO", "sender": "ENVIADO"}[env.ReceiptType]
				prefix := colorize(ansiRCPT, "[RCPT]") + " "
				if len(env.MessageIDs) == 1 {
					e.humanInfof(prefix+"[%s] Chat:%s | Tipo:%s | MsgID:%s", k, colorize(ansiBold, env.ChatJID), tag, env.MessageIDs[0])
				} else {
					e.humanInfof(prefix+"[%s] Chat:%s | Tipo:%s | MsgIDs:%d", k, colorize(ansiBold, env.ChatJID), tag, len(env.MessageIDs))
				}
			}
			_ = e.writeEnvelopeToFolder(env)
			e.sendEnvelopeToWebhook(ctx, env)
			if h.OnReceipt != nil {
				_ = h.OnReceipt(ctx, v)
			}

		case *events.ChatPresence:
			env := &ForwardEnvelope{
				EventType: "chat_presence",
				EventRaw:  v,
				ChatJID:   canonicalChatJID(v.Chat.String()),
				SenderJID: v.Sender.String(),
				Extra: map[string]any{
					"state": string(v.State),
					"media": string(v.Media),
				},
			}
			prefix := colorize(ansiPRES, "[PRESENCIA][CHAT]") + " "
			add := ""
			if v.Media != "" {
				add = " (" + string(v.Media) + ")"
			}
			e.humanInfof(prefix+"Chat:%s | %s%s", colorize(ansiBold, env.ChatJID), strings.ToUpper(string(v.State)), add)
			_ = e.writeEnvelopeToFolder(env)
			e.sendEnvelopeToWebhook(ctx, env)

		case *events.Presence:
			env := &ForwardEnvelope{
				EventType: "presence",
				EventRaw:  v,
				SenderJID: canonicalChatJID(v.From.String()),
				Extra: map[string]any{
					"unavailable": v.Unavailable,
					"last_seen":   v.LastSeen.UTC().Format(time.RFC3339),
				},
			}
			if v.Unavailable {
				e.humanInfof(colorize(ansiPRES, "[PRESENCIA] ")+"%s ahora OFFLINE | last_seen=%s", colorize(ansiBold, env.SenderJID), env.Extra["last_seen"])
			} else {
				e.humanInfof(colorize(ansiPRES, "[PRESENCIA] ")+"%s ahora ONLINE", colorize(ansiBold, env.SenderJID))
			}
			_ = e.writeEnvelopeToFolder(env)
			e.sendEnvelopeToWebhook(ctx, env)
			if h.OnPresence != nil {
				_ = h.OnPresence(ctx, v)
			}

		case *events.GroupInfo:
			env := &ForwardEnvelope{EventType: "group_update", EventRaw: v, ChatJID: canonicalChatJID(v.JID.String())}
			e.humanInfof(colorize(ansiGROUP, "[GRUPO][UPDATE] ")+"JID:%s | (cambios en miembros/título/foto…)", colorize(ansiBold, env.ChatJID))
			_ = e.writeEnvelopeToFolder(env)
			e.sendEnvelopeToWebhook(ctx, env)
			if h.OnGroupUpdate != nil {
				_ = h.OnGroupUpdate(ctx, v)
			}

		case *events.JoinedGroup:
			env := &ForwardEnvelope{EventType: "joined_group", EventRaw: v, ChatJID: canonicalChatJID(v.JID.String())}
			e.humanInfof(colorize(ansiGROUP, "[GRUPO][JOINED] ")+"Te uniste a %s", colorize(ansiBold, env.ChatJID))
			_ = e.writeEnvelopeToFolder(env)
			e.sendEnvelopeToWebhook(ctx, env)

		case *events.HistorySync:
			env := &ForwardEnvelope{EventType: "history_sync", EventRaw: v}
			e.humanInfof(colorize(ansiDEFAULT, "[HISTORY] ") + "Se recibió HistorySync (mensajes antiguos).")
			_ = e.writeEnvelopeToFolder(env)
			e.sendEnvelopeToWebhook(ctx, env)
			e.handleHistorySync(v)

		case *events.Connected:
			env := &ForwardEnvelope{EventType: "connected", EventRaw: v}
			e.humanInfof(colorize(ansiSTATE, "[ESTADO] ") + "Autenticado y CONECTADO.")

			// 👉 Reafirma presencia tras reconectar
			_ = e.client.SendPresence(ctx, types.PresenceAvailable)
			_ = e.writeEnvelopeToFolder(env)
			e.sendEnvelopeToWebhook(ctx, env)

		case *events.LoggedOut:
			env := &ForwardEnvelope{EventType: "logged_out", EventRaw: v}
			e.humanWarnf(colorize(ansiWARN, "[ESTADO] ") + "Sesión cerrada. Puede requerir re-vinculación (QR).")
			_ = e.writeEnvelopeToFolder(env)
			e.sendEnvelopeToWebhook(ctx, env)

		case *events.IdentityChange:
			chatJID := extractJIDFromIdentityChange(v)
			env := &ForwardEnvelope{
				EventType: "events.IdentityChange",
				EventRaw:  v,
				ChatJID:   canonicalChatJID(chatJID),
			}
			if chatJID != "" {
				e.humanInfof(colorize(ansiDEFAULT, "[IDENTITY] ")+"Cambio de identidad en %s", colorize(ansiBold, env.ChatJID))
			} else {
				e.humanInfof(colorize(ansiDEFAULT, "[IDENTITY] ") + "Cambio de identidad (sin JID detectable)")
			}
			_ = e.writeEnvelopeToFolder(env)
			e.sendEnvelopeToWebhook(ctx, env)

		default:
			env := &ForwardEnvelope{EventType: fmt.Sprintf("%T", v), EventRaw: v}
			e.humanInfof(colorize(ansiDEFAULT, "[EVENTO] ")+"%T", v)
			_ = e.writeEnvelopeToFolder(env)
			e.sendEnvelopeToWebhook(ctx, env)
		}
	})
}

//
// ================================
// 6) Funciones concretas (façade)
// ================================
//

type MediaInput struct {
	Bytes        []byte
	Mime         string
	FileName     string
	Caption      string
	MediaType    wm.MediaType
	AudioSeconds uint32
	Waveform     []byte
}

func (e *Engine) SendText(ctx context.Context, to types.JID, text string) (string, error) {
	base := func(ctx context.Context, to types.JID, payload any) (string, error) {
		msg := &waProto.Message{Conversation: proto.String(text)}
		resp, err := e.client.SendMessage(ctx, to, msg)
		if err != nil {
			return "", err
		}
		id := resp.ID

		// Log OUT
		k := kindOfChat(to)
		prefix := colorize(ansiOUT, "[OUT]") + " "
		e.humanInfof(prefix+"[%s] To:%s | ID:%s | Texto:\"%s\"", k, colorize(ansiBold, to.String()), id, short(text, 80))

		// Persistencia OUT
		if e.msgStore != nil {
			_ = e.msgStore.SaveMessage(
				storageChatJID(to.String()),
				id,
				"me",
				text,
				time.Now(),
				true,
				sql.NullString{},                      // media_type
				sql.NullString{},                      // filename
				sql.NullString{},                      // url
			)
		}
		// Forward OUT a carpeta/webhook unificado por contacto
		e.forwardOutgoing(to, id, text, "", "", "")
		return id, nil
	}
	fn := WithRetry(3, 250*time.Millisecond, WithRateLimit(e.limiterSend, base))
	return fn(ctx, to, text)
}

func (e *Engine) SendMedia(ctx context.Context, to types.JID, in MediaInput) (string, error) {
	base := func(ctx context.Context, to types.JID, payload any) (string, error) {
		respUp, err := e.client.Upload(ctx, in.Bytes, in.MediaType)

		if err != nil {
			return "", err
		}
		msg := &waProto.Message{}
		mt := "DOCUMENT"
		switch in.MediaType {
		case wm.MediaImage:
			mt = "IMAGE"
			msg.ImageMessage = &waProto.ImageMessage{
				Caption:       proto.String(in.Caption),
				Mimetype:      proto.String(in.Mime),
				URL:           &respUp.URL,
				DirectPath:    &respUp.DirectPath,
				MediaKey:      respUp.MediaKey,
				FileEncSHA256: respUp.FileEncSHA256,
				FileSHA256:    respUp.FileSHA256,
				FileLength:    &respUp.FileLength,
			}
		case wm.MediaVideo:
			mt = "VIDEO"
			msg.VideoMessage = &waProto.VideoMessage{
				Caption:       proto.String(in.Caption),
				Mimetype:      proto.String(in.Mime),
				URL:           &respUp.URL,
				DirectPath:    &respUp.DirectPath,
				MediaKey:      respUp.MediaKey,
				FileEncSHA256: respUp.FileEncSHA256,
				FileSHA256:    respUp.FileSHA256,
				FileLength:    &respUp.FileLength,
			}
		case wm.MediaAudio:
			mt = "AUDIO"
			secs := in.AudioSeconds
			wave := in.Waveform
			if (secs == 0 || len(wave) == 0) && (strings.Contains(strings.ToLower(in.Mime), "ogg") || strings.Contains(strings.ToLower(in.Mime), "opus")) {
				if d, wf, err := analyzeOggOpus(in.Bytes); err == nil {
					if secs == 0 {
						secs = d
					}
					if len(wave) == 0 {
						wave = wf
					}
				}
			}
			if secs == 0 {
				secs = 30
			}
			if len(wave) == 0 {
				wave = placeholderWaveform(secs)
			}
			msg.AudioMessage = &waProto.AudioMessage{
				Mimetype:      proto.String(in.Mime),
				URL:           &respUp.URL,
				DirectPath:    &respUp.DirectPath,
				MediaKey:      respUp.MediaKey,
				FileEncSHA256: respUp.FileEncSHA256,
				FileSHA256:    respUp.FileSHA256,
				FileLength:    &respUp.FileLength,
				Seconds:       proto.Uint32(secs),
				PTT:           proto.Bool(true),
				Waveform:      wave,
			}
		default:
			mt = "DOCUMENT"
			msg.DocumentMessage = &waProto.DocumentMessage{
				Title:         proto.String(in.FileName),
				Caption:       proto.String(in.Caption),
				Mimetype:      proto.String(in.Mime),
				URL:           &respUp.URL,
				DirectPath:    &respUp.DirectPath,
				MediaKey:      respUp.MediaKey,
				FileEncSHA256: respUp.FileEncSHA256,
				FileSHA256:    respUp.FileSHA256,
				FileLength:    &respUp.FileLength,
			}
		}
		respSend, err := e.client.SendMessage(ctx, to, msg)
		if err != nil {
			return "", err
		}
		id := respSend.ID

		// Log OUT
		k := kindOfChat(to)
		prefix := colorize(ansiOUT, "[OUT]") + " "
		e.humanInfof(prefix+"[%s] To:%s | ID:%s | MEDIA:%s | Caption:\"%s\"", k, colorize(ansiBold, to.String()), id, mt, short(in.Caption, 60))

		// Persistencia OUT (media)
		if e.msgStore != nil {
			_ = e.msgStore.SaveMessage(
				storageChatJID(to.String()),
				id,
				"me",
				in.Caption,
				time.Now(),
				true,
				sql.NullString{String: strings.ToLower(mt), Valid: true}, // media_type correcto
				sql.NullString{String: in.FileName, Valid: in.FileName != ""}, // filename
				sql.NullString{}, // url (si no quieres persistir el CDN aquí)
			)
		}


		// Construir ticket/mediaMap desde respUp (subida) y metadatos locales
		mediaMap := map[string]any{
			"type":                strings.ToLower(mt), // "image" | "video" | "audio" | "document"
			"mimetype":            in.Mime,
			"title":               in.FileName,
			"url":                 respUp.URL,
			"direct_path":         respUp.DirectPath,
			"file_length":         respUp.FileLength,
			"media_key_b64":       b64(respUp.MediaKey),
			"file_sha256_b64":     b64(respUp.FileSHA256),
			"file_enc_sha256_b64": b64(respUp.FileEncSHA256),
			"media_type":          strings.ToLower(mt),
		}

		// Campos opcionales útiles según tipo
		if mt == "AUDIO" {
			// si quieres, envía “seconds” y “waveform” cuando los tengas
			// mediaMap["seconds"] = secs
		}
		if mt == "DOCUMENT" && in.FileName != "" {
			mediaMap["title"] = in.FileName
		}

		// Forward OUT con ticket completo
		e.forwardOutgoingWithMedia(to, id, in.Caption, mediaMap)

		return id, nil
	}
	fn := WithRetry(3, 400*time.Millisecond, WithRateLimit(e.limiterMedia, base))
	return fn(ctx, to, in)
}

// --- receipts / presence

func toMsgIDs(ids []string) []types.MessageID {
	out := make([]types.MessageID, 0, len(ids))
	for _, s := range ids {
		if s != "" {
			out = append(out, types.MessageID(s))
		}
	}
	return out
}

func (e *Engine) MarkRead(ctx context.Context, chat types.JID, ids []string) error {
	msgIDs := toMsgIDs(ids)
	if len(msgIDs) == 0 {
		return nil
	}
	// 1:1 → sender vacío
	return e.client.MarkRead(ctx, msgIDs, time.Now(), chat, types.EmptyJID)
}

func (e *Engine) MarkReadWithSender(ctx context.Context, chat, sender types.JID, ids []string) error {
	msgIDs := toMsgIDs(ids)
	if len(msgIDs) == 0 {
		return nil
	}
	// Grupos → sender obligatorio (todos los IDs deben ser del mismo remitente)
	return e.client.MarkRead(ctx, msgIDs, time.Now(), chat, sender)
}

func (e *Engine) MarkPlayedVoice(ctx context.Context, chat, sender types.JID, ids []string) error {
	msgIDs := toMsgIDs(ids)
	if len(msgIDs) == 0 {
		return nil
	}
	// “played” (notas de voz) en grupos → requiere sender
	return e.client.MarkRead(ctx, msgIDs, time.Now(), chat, sender, types.ReceiptTypePlayed)
}

// Nota: este stub alinea la firma con whserver/whbot y el endpoint /api/markread.
// Para hacer el "read" real en WA hay que llamar a la primitiva exacta de tu versión de whatsmeow.
// Dímela y lo cableo (MessageKey por msg en ese chat, y enviar tipo "read").

func (e *Engine) SetTyping(ctx context.Context, chat types.JID, typing bool, media types.ChatPresenceMedia) error {
	state := types.ChatPresencePaused
	if typing {
		state = types.ChatPresenceComposing
	}
	return e.client.SendChatPresence(ctx, chat, state, media)
}

// --- grupos (stubs)
func (e *Engine) CreateGroup(ctx context.Context, subject string, members []types.JID) (types.JID, error) {
	_ = ctx
	_ = subject
	_ = members
	return types.JID{}, nil
}
func (e *Engine) AddGroupParticipants(ctx context.Context, gid types.JID, members []types.JID) error {
	_ = ctx
	_ = gid
	_ = members
	return nil
}

// --- status (stories)
func (e *Engine) PostStatus(ctx context.Context, in MediaInput) (string, error) {
	if !e.caps.Status {
		return "", ErrUnsupported
	}
	base := func(ctx context.Context, _ types.JID, payload any) (string, error) { return "status-sent", nil }
	fn := WithRetry(2, 600*time.Millisecond, WithRateLimit(e.limiterStat, base))
	return fn(ctx, types.JID{}, in)
}

func (e *Engine) SubscribeStatus(ctx context.Context) error {
	_ = ctx
	if !e.caps.Status {
		return ErrUnsupported
	}
	return nil
}

func (e *Engine) DownloadStatusMedia(ctx context.Context, owner types.JID, statusID string) ([]byte, error) {
	_ = ctx
	_ = owner
	_ = statusID
	if !e.caps.Status {
		return nil, ErrUnsupported
	}
	return nil, nil
}

// --- history sync
func (e *Engine) RequestHistorySync(ctx context.Context) error {
	msg := e.client.BuildHistorySyncRequest(nil, 100)
	if msg == nil {
		return errors.New("failed to build history sync req")
	}
	_, err := e.client.SendMessage(ctx, types.JID{Server: "s.whatsapp.net", User: "status"}, msg)
	return err
}

//
// ===========================
// 7) Media download helper
// ===========================
//

type downloadable struct {
	URL, DirectPath string
	MediaKey        []byte
	FileLength      uint64
	FileSHA256      []byte
	FileEncSHA256   []byte
	MT              wm.MediaType
}

func (d *downloadable) GetDirectPath() string      { return d.DirectPath }
func (d *downloadable) GetURL() string             { return d.URL }
func (d *downloadable) GetMediaKey() []byte        { return d.MediaKey }
func (d *downloadable) GetFileLength() uint64      { return d.FileLength }
func (d *downloadable) GetFileSHA256() []byte      { return d.FileSHA256 }
func (d *downloadable) GetFileEncSHA256() []byte   { return d.FileEncSHA256 }
func (d *downloadable) GetMediaType() wm.MediaType { return d.MT }

func (e *Engine) DownloadMedia(ctx context.Context, info struct {
	URL           string
	DirectPath    string
	MediaKey      []byte
	FileLength    uint64
	FileSHA256    []byte
	FileEncSHA256 []byte
	MediaType     wm.MediaType
	LocalPath     string
}) (string, error) {
	d := &downloadable{
		URL:           info.URL,
		DirectPath:    info.DirectPath,
		MediaKey:      info.MediaKey,
		FileLength:    info.FileLength,
		FileSHA256:    info.FileSHA256,
		FileEncSHA256: info.FileEncSHA256,
		MT:            info.MediaType,
	}
	b, err := e.client.Download(ctx, d)
	if err != nil {
		return "", err
	}
	if err := os.WriteFile(info.LocalPath, b, 0o644); err != nil {
		return "", err
	}
	return info.LocalPath, nil
}

//
// =======================
// 8) REST (control plane)
// =======================
//

type SendMessageRequest struct {
	Recipient string `json:"recipient"`
	Message   string `json:"message"`
	MediaPath string `json:"media_path,omitempty"`
}

type TypingRequest struct {
	Recipient string `json:"recipient"`
	Typing    bool   `json:"typing"`
	Media     string `json:"media,omitempty"` // text|audio
}

type MarkReadRequest struct {
	Sender      string   `json:"sender,omitempty"`
	Recipient   string   `json:"recipient"`
	MessageIDs  []string `json:"message_ids"`
	ReceiptType string   `json:"receipt_type,omitempty"` // read|played (por ahora ignorado)
}

func (e *Engine) StartREST() {
	// /api/send
	http.HandleFunc("/api/send", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}

		var req SendMessageRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}

		// Parse recipient → types.JID
		var to types.JID
		rcpt := canonicalChatJID(req.Recipient)
		if strings.Contains(rcpt, "@") {
		    if j, err := types.ParseJID(rcpt); err == nil {
		        to = j
		    } else if strings.HasSuffix(rcpt, "@lid") {
		        parts := strings.SplitN(rcpt, "@", 2)
		        to = types.JID{User: parts[0], Server: "lid"}
		    } else {
		        http.Error(w, "bad jid", http.StatusBadRequest)
		        return
		    }
		} else {
		    to = types.JID{User: rcpt, Server: "s.whatsapp.net"}
		}


		// Enviar texto o media
		var id string
		var err error
		if req.MediaPath == "" {
			id, err = e.SendText(r.Context(), to, req.Message)
		} else {
			// dentro del handler /api/send, en el else de MediaPath:
			data, readErr := os.ReadFile(req.MediaPath)
			if readErr != nil {
				http.Error(w, readErr.Error(), http.StatusBadRequest)
				return
			}

			ext := strings.ToLower(filepath.Ext(req.MediaPath))
			mimeType := mime.TypeByExtension(ext)
			if mimeType == "" {
				mimeType = "application/octet-stream"
			}

			mediaType := wm.MediaDocument
			switch ext {
			case ".jpg", ".jpeg", ".png", ".webp", ".gif":
				mediaType = wm.MediaImage
			case ".mp4", ".mov", ".m4v", ".webm":
				mediaType = wm.MediaVideo
			case ".ogg", ".opus", ".mp3", ".m4a", ".wav":
				mediaType = wm.MediaAudio
			}

			mi := MediaInput{
				Bytes:     data,
				Caption:   req.Message,
				Mime:      mimeType,
				MediaType: mediaType,
				FileName:  filepath.Base(req.MediaPath),
			}
			id, err = e.SendMedia(r.Context(), to, mi)
		}

		type resp struct {
			Success bool
			Message string
		}
		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			_ = json.NewEncoder(w).Encode(resp{false, err.Error()})
			return
		}
		_ = json.NewEncoder(w).Encode(resp{true, "sent: " + id})
	})

	// /api/typing
	http.HandleFunc("/api/typing", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}

		var req TypingRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}

		rcpt := canonicalChatJID(req.Recipient)
		j, err := types.ParseJID(rcpt)
		if err != nil {
		    if strings.HasSuffix(rcpt, "@lid") {
		        parts := strings.SplitN(rcpt, "@", 2)
		        j = types.JID{User: parts[0], Server: "lid"}
		    } else {
		        http.Error(w, "bad jid", http.StatusBadRequest)
		        return
		    }
		}
		to := j

		media := types.ChatPresenceMediaText
		if strings.ToLower(req.Media) == "audio" {
			media = types.ChatPresenceMediaAudio
		}

		err = e.SetTyping(r.Context(), to, req.Typing, media)

		type resp struct {
			Success bool
			Message string
		}
		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			_ = json.NewEncoder(w).Encode(resp{false, err.Error()})
			return
		}
		_ = json.NewEncoder(w).Encode(resp{true, "typing updated"})
	})

	// /api/markread
	http.HandleFunc("/api/markread", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}

		var req MarkReadRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}

		// Parse recipient
		rcpt := canonicalChatJID(req.Recipient)
		j, err := types.ParseJID(rcpt)
		if err != nil {
		    if strings.HasSuffix(rcpt, "@lid") {
		        parts := strings.SplitN(rcpt, "@", 2)
		        j = types.JID{User: parts[0], Server: "lid"}
		    } else {
		        http.Error(w, "bad jid", http.StatusBadRequest)
		        return
		    }
		}


		var senderJ types.JID
		if strings.TrimSpace(req.Sender) != "" {
			src := strings.TrimSpace(req.Sender)
			if s, errS := types.ParseJID(src); errS == nil {
				senderJ = s
			} else if strings.HasSuffix(src, "@lid") {
				// parse manual para LID si el parser no lo reconoce en tu versión
				parts := strings.SplitN(src, "@", 2)
				if len(parts) == 2 {
					senderJ = types.JID{User: parts[0], Server: "lid"}
				}
			} else if !strings.Contains(src, "@") {
				senderJ = types.JID{User: src, Server: "s.whatsapp.net"}
			}
		}

		if len(req.MessageIDs) == 0 {
			http.Error(w, "message_ids required", http.StatusBadRequest)
			return
		}

		type resp struct {
			Success bool
			Message string
		}
		played := strings.EqualFold(strings.TrimSpace(req.ReceiptType), "played")

		if senderJ != (types.JID{}) {
			// Grupo
			var callErr error
			if played {
				callErr = e.MarkPlayedVoice(r.Context(), j, senderJ, req.MessageIDs)
			} else {
				callErr = e.MarkReadWithSender(r.Context(), j, senderJ, req.MessageIDs)
			}
			if callErr != nil {
				w.WriteHeader(http.StatusInternalServerError)
				_ = json.NewEncoder(w).Encode(resp{false, callErr.Error()})
				return
			}
		} else {
			// 1:1
			if played {
				// En 1:1 no hay sender → usa EmptyJID
				msgIDs := toMsgIDs(req.MessageIDs)
				if err := e.client.MarkRead(r.Context(), msgIDs, time.Now(), j, types.EmptyJID, types.ReceiptTypePlayed); err != nil {
					w.WriteHeader(http.StatusInternalServerError)
					_ = json.NewEncoder(w).Encode(resp{false, err.Error()})
					return
				}
			} else {
				if err := e.MarkRead(r.Context(), j, req.MessageIDs); err != nil {
					w.WriteHeader(http.StatusInternalServerError)
					_ = json.NewEncoder(w).Encode(resp{false, err.Error()})
					return
				}
			}
		}

		_ = json.NewEncoder(w).Encode(resp{true, "marked"})
	})

	addr := fmt.Sprintf(":%d", e.cfg.HTTPPort)
	go func() {
		if err := http.ListenAndServe(addr, nil); err != nil {
			if e.logger != nil {
				e.logger.Warnf("REST server error on %s: %v", addr, err)
			}
		}
	}()
}

//
// =======================
// 9) Wiring / lifecycle
// =======================
//

func NewEngine(cfg Config) (*Engine, error) {
	logger := waLog.Stdout("Engine", "INFO", term.IsTerminal(int(os.Stdout.Fd())))
	msgs, err := NewMessageStore(cfg.MsgDBPath)
	if err != nil {
		return nil, err
	}
	e := &Engine{
		cfg:          cfg,
		logger:       logger,
		msgStore:     msgs,
		limiterSend:  rate.NewLimiter(rate.Every(50*time.Millisecond), 5),
		limiterMedia: rate.NewLimiter(rate.Every(150*time.Millisecond), 2),
		limiterStat:  rate.NewLimiter(rate.Every(500*time.Millisecond), 1),
	}
	base := cfg.Forward.OutFolder
	if base == "" {
		base = "outbox"
	}
	e.fileSink = NewFlatSink(base, 0)
	return e, nil
}

func (e *Engine) Run(ctx context.Context, h Handlers) error {
	if err := e.CheckSession(ctx); err != nil {
		return err
	}
	e.RunEventLoop(ctx, h)
	if e.cfg.BackupEvery > 0 && e.store != nil {
		go func() {
			t := time.NewTicker(e.cfg.BackupEvery)
			for {
				select {
				case <-t.C:
					_ = e.store.RunBackup(context.Background())
				case <-ctx.Done():
					return
				}
			}
		}()
	}
	e.StartREST()
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
	<-sig
	e.client.Disconnect()
	_ = e.msgStore.Close()
	return nil
}

//
// =======================
// 10) Utils inspirados
// =======================
//

func (e *Engine) ResolveChatName(jid types.JID, chatJID string, conversation interface{}, sender string) string {
	var name string
	if jid.Server == "g.us" {
		if conversation != nil {
			var dn, cn *string
			v := reflect.ValueOf(conversation)
			if v.Kind() == reflect.Ptr && !v.IsNil() {
				v = v.Elem()
				if f := v.FieldByName("DisplayName"); f.IsValid() && f.Kind() == reflect.Ptr && !f.IsNil() {
					tmp := f.Elem().String()
					dn = &tmp
				}
				if f := v.FieldByName("Name"); f.IsValid() && f.Kind() == reflect.Ptr && !f.IsNil() {
					tmp := f.Elem().String()
					cn = &tmp
				}
			}
			if dn != nil && *dn != "" {
				name = *dn
			} else if cn != nil && *cn != "" {
				name = *cn
			}
		}
		if name == "" {
			if gi, err := e.client.GetGroupInfo(context.Background(), jid); err == nil && gi.Name != "" {
				name = gi.Name
			} else {
				name = "Group " + jid.User
			}
		}
	} else {
		if c, err := e.client.Store.Contacts.GetContact(context.Background(), jid); err == nil && c.FullName != "" {
			name = c.FullName
		} else if sender != "" {
			name = sender
		} else {
			name = jid.User
		}
	}
	return name
}

func placeholderWaveform(duration uint32) []byte {
	const N = 64
	w := make([]byte, N)
	rand.Seed(int64(duration))
	baseAmp := 35.0
	freq := float64(min(int(duration), 120)) / 30.0
	for i := range w {
		pos := float64(i) / float64(N)
		val := baseAmp*math.Sin(pos*math.Pi*freq*8) + (baseAmp/2)*math.Sin(pos*math.Pi*freq*16)
		val += (rand.Float64() - 0.5) * 15
		val = val*(0.7+0.3*math.Sin(pos*math.Pi)) + 50
		if val < 0 {
			val = 0
		} else if val > 100 {
			val = 100
		}
		w[i] = byte(val)
	}
	return w
}
func min(x, y int) int {
	if x < y {
		return x
	}
	return y
}

//lint:ignore U1000 keep for future (análisis simple de OGG/Opus)
func analyzeOggOpus(data []byte) (duration uint32, waveform []byte, err error) {
	if len(data) < 4 || string(data[0:4]) != "OggS" {
		return 0, nil, fmt.Errorf("not ogg")
	}
	var lastGranule uint64
	var sampleRate uint32 = 48000
	var preSkip uint16
	for i := 0; i < len(data); {
		if i+27 >= len(data) {
			break
		}
		if string(data[i:i+4]) != "OggS" {
			i++
			continue
		}
		gran := binary.LittleEndian.Uint64(data[i+6 : i+14])
		numSeg := int(data[i+26])
		if i+27+numSeg >= len(data) {
			break
		}
		pageSize := 27 + numSeg
		for _, seg := range data[i+27 : i+27+numSeg] {
			pageSize += int(seg)
		}
		page := data[i : i+pageSize]
		if bytes.Contains(page, []byte("OpusHead")) {
			// simplificado
		}
		if gran != 0 {
			lastGranule = gran
		}
		i += pageSize
	}
	if lastGranule > 0 {
		d := float64(lastGranule-uint64(preSkip)) / float64(sampleRate)
		if d < 1 {
			d = 1
		}
		if d > 300 {
			d = 300
		}
		duration = uint32(math.Ceil(d))
	} else {
		duration = 30
	}
	return duration, placeholderWaveform(duration), nil
}

func (e *Engine) handleHistorySync(h *events.HistorySync) { _ = h /* no-op */ }
