package handlers

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/bytedance/sonic"
	"github.com/fasthttp/router"
	"github.com/google/uuid"
	"github.com/maximhq/bifrost/core/schemas"
	"github.com/maximhq/bifrost/framework/configstore"
	"github.com/maximhq/bifrost/framework/configstore/tables"
	"github.com/maximhq/bifrost/transports/v2/bifrost-http/lib"
	"github.com/valyala/fasthttp"
)

const auditEventTypeURI = "https://getbifrost.ai/schemas/auditlog.json"

// AuditLogsHandler exposes the dashboard's filter and export surface. Events
// are immutable and never include request bodies, authorization credentials or
// other secret-bearing payloads; this keeps exports safe to send to a SIEM.
// A configured HMAC key is accepted only when it has at least 32 bytes.
type AuditLogsHandler struct {
	store   configstore.AuditLogStore
	omitIPs bool
	hmacKey []byte
}

func NewAuditLogsHandler(store configstore.AuditLogStore) *AuditLogsHandler {
	key := []byte(os.Getenv("BIFROST_AUDIT_HMAC_KEY"))
	if len(key) < 32 {
		key = nil
	}
	return &AuditLogsHandler{store: store, omitIPs: os.Getenv("BIFROST_AUDIT_OMIT_IP_ADDRESSES") == "true", hmacKey: key}
}

func (h *AuditLogsHandler) RegisterRoutes(r *router.Router, middlewares ...schemas.BifrostHTTPMiddleware) {
	r.GET("/api/audit-logs", lib.ChainMiddlewares(h.list, middlewares...))
	r.GET("/api/audit-logs/export", lib.ChainMiddlewares(h.export, middlewares...))
}

type auditLogResponse struct {
	ID         string    `json:"id"`
	OccurredAt time.Time `json:"occurred_at"`
	Action     string    `json:"action"`
	Outcome    string    `json:"outcome"`
	Initiator  string    `json:"initiator"`
	Target     string    `json:"target"`
	Method     string    `json:"method"`
	Path       string    `json:"path"`
	RequestIP  string    `json:"request_ip,omitempty"`
	UserAgent  string    `json:"user_agent"`
	DurationMS int64     `json:"duration_ms"`
	TypeURI    string    `json:"type_uri"`
	Signed     bool      `json:"signed"`
}

func (h *AuditLogsHandler) list(ctx *fasthttp.RequestCtx) {
	filter, err := auditLogFilter(ctx, 100)
	if err != nil {
		SendError(ctx, fasthttp.StatusBadRequest, err.Error())
		return
	}
	events, count, err := h.listEvents(ctx, filter)
	if err != nil {
		SendError(ctx, fasthttp.StatusInternalServerError, "failed to list audit logs")
		return
	}
	SendJSON(ctx, map[string]any{"audit_logs": h.responses(events), "count": count})
}

func (h *AuditLogsHandler) export(ctx *fasthttp.RequestCtx) {
	filter, err := auditLogFilter(ctx, 10000)
	if err != nil {
		SendError(ctx, fasthttp.StatusBadRequest, err.Error())
		return
	}
	events, _, err := h.listEvents(ctx, filter)
	if err != nil {
		SendError(ctx, fasthttp.StatusInternalServerError, "failed to export audit logs")
		return
	}
	format := strings.ToLower(string(ctx.QueryArgs().Peek("format")))
	if format == "" {
		format = "json"
	}
	rows := h.responses(events)
	ctx.Response.Header.Set("Content-Disposition", "attachment; filename=audit-logs."+auditExportExtension(format))
	switch format {
	case "json":
		ctx.SetContentType("application/json")
		body, err := sonic.Marshal(rows)
		if err != nil {
			SendError(ctx, fasthttp.StatusInternalServerError, "failed to export audit logs")
			return
		}
		ctx.SetBody(body)
	case "jsonl":
		ctx.SetContentType("application/x-ndjson")
		var body strings.Builder
		for _, row := range rows {
			encoded, err := sonic.Marshal(row)
			if err != nil {
				SendError(ctx, fasthttp.StatusInternalServerError, "failed to export audit logs")
				return
			}
			body.Write(encoded)
			body.WriteByte('\n')
		}
		ctx.SetBodyString(body.String())
	case "syslog":
		ctx.SetContentType("text/plain; charset=utf-8")
		var body strings.Builder
		for _, row := range rows {
			fmt.Fprintf(&body, "<134>1 %s bifrost - - audit [bifrost@32473 action=\"%s\" outcome=\"%s\" initiator=\"%s\" target=\"%s\" path=\"%s\"] audit event\n", row.OccurredAt.UTC().Format(time.RFC3339), syslogEscape(row.Action), syslogEscape(row.Outcome), syslogEscape(row.Initiator), syslogEscape(row.Target), syslogEscape(row.Path))
		}
		ctx.SetBodyString(body.String())
	default:
		SendError(ctx, fasthttp.StatusBadRequest, "format must be json, jsonl or syslog")
		return
	}
	h.write(ctx, "export")
}

func (h *AuditLogsHandler) write(ctx *fasthttp.RequestCtx, action string) {
	if h.store == nil {
		return
	}
	event := newAuditEvent(ctx, action, h.omitIPs, h.hmacKey, 0)
	go func(event tables.TableAuditLog) {
		writeCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		if err := h.store.CreateAuditLog(writeCtx, &event); err != nil {
			logger.Warn("failed to persist audit event: %v", err)
		}
	}(event)
}

func (h *AuditLogsHandler) listEvents(ctx context.Context, filter configstore.AuditLogFilter) ([]tables.TableAuditLog, int64, error) {
	if h.store == nil {
		return nil, 0, fmt.Errorf("audit log storage is unavailable")
	}
	return h.store.ListAuditLogs(ctx, filter)
}

func (h *AuditLogsHandler) responses(events []tables.TableAuditLog) []auditLogResponse {
	rows := make([]auditLogResponse, 0, len(events))
	for _, event := range events {
		ip := event.RequestIP
		if h.omitIPs {
			ip = ""
		}
		rows = append(rows, auditLogResponse{ID: event.ID, OccurredAt: event.OccurredAt, Action: event.Action, Outcome: event.Outcome, Initiator: event.Initiator, Target: event.Target, Method: event.Method, Path: event.Path, RequestIP: ip, UserAgent: event.UserAgent, DurationMS: event.DurationMS, TypeURI: event.TypeURI, Signed: event.Signature != ""})
	}
	return rows
}

func auditLogFilter(ctx *fasthttp.RequestCtx, defaultLimit int) (configstore.AuditLogFilter, error) {
	args := ctx.QueryArgs()
	limit := defaultLimit
	if raw := string(args.Peek("limit")); raw != "" {
		value, err := strconv.Atoi(raw)
		if err != nil || value < 1 || value > 10000 {
			return configstore.AuditLogFilter{}, fmt.Errorf("limit must be between 1 and 10000")
		}
		limit = value
	}
	offset := 0
	if raw := string(args.Peek("offset")); raw != "" {
		value, err := strconv.Atoi(raw)
		if err != nil || value < 0 {
			return configstore.AuditLogFilter{}, fmt.Errorf("offset must be non-negative")
		}
		offset = value
	}
	filter := configstore.AuditLogFilter{Search: string(args.Peek("search")), Action: string(args.Peek("action")), Outcome: string(args.Peek("outcome")), Limit: limit, Offset: offset}
	for key, destination := range map[string]**time.Time{"start": &filter.Start, "end": &filter.End} {
		if raw := string(args.Peek(key)); raw != "" {
			parsed, err := time.Parse(time.RFC3339, raw)
			if err != nil {
				return configstore.AuditLogFilter{}, fmt.Errorf("%s must be RFC3339", key)
			}
			*destination = &parsed
		}
	}
	return filter, nil
}

func auditExportExtension(format string) string {
	if format == "jsonl" {
		return "jsonl"
	}
	if format == "syslog" {
		return "log"
	}
	return "json"
}
func syslogEscape(value string) string {
	return strings.NewReplacer("\\", "\\\\", "\"", "\\\"", "]", "\\]").Replace(value)
}

// AuditMiddleware records completed administrative mutations outside the
// inference request path. It copies every value before the asynchronous DB
// write because fasthttp recycles RequestCtx immediately after a handler exits.
func AuditMiddleware(store configstore.AuditLogStore) schemas.BifrostHTTPMiddleware {
	if store == nil {
		return func(next fasthttp.RequestHandler) fasthttp.RequestHandler { return next }
	}
	key := []byte(os.Getenv("BIFROST_AUDIT_HMAC_KEY"))
	omitIPs := os.Getenv("BIFROST_AUDIT_OMIT_IP_ADDRESSES") == "true"
	return func(next fasthttp.RequestHandler) fasthttp.RequestHandler {
		return func(ctx *fasthttp.RequestCtx) {
			path := string(ctx.Path())
			method := string(ctx.Method())
			if !shouldAuditRequest(method, path) {
				next(ctx)
				return
			}
			started := time.Now()
			next(ctx)
			event := newAuditEvent(ctx, auditAction(method, path), omitIPs, key, time.Since(started).Milliseconds())
			go func(event tables.TableAuditLog) {
				writeCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
				defer cancel()
				if err := store.CreateAuditLog(writeCtx, &event); err != nil {
					logger.Warn("failed to persist audit event: %v", err)
				}
			}(event)
		}
	}
}

func newAuditEvent(ctx *fasthttp.RequestCtx, action string, omitIPs bool, key []byte, durationMS int64) tables.TableAuditLog {
	path := string(ctx.Path())
	event := tables.TableAuditLog{ID: uuid.NewString(), OccurredAt: time.Now().UTC(), Action: action, Outcome: auditOutcome(ctx.Response.StatusCode()), Initiator: auditInitiator(ctx), Target: path, Method: string(ctx.Method()), Path: path, UserAgent: string(ctx.UserAgent()), DurationMS: durationMS, TypeURI: auditEventTypeURI}
	if !omitIPs {
		event.RequestIP = auditRequestIP(ctx)
	}
	if len(key) >= 32 {
		event.Signature = auditSignature(key, event)
	}
	return event
}

func shouldAuditRequest(method, path string) bool {
	return strings.HasPrefix(path, "/api/") && method != fasthttp.MethodGet && method != fasthttp.MethodHead && !strings.HasPrefix(path, "/api/session/ws-ticket")
}
func auditAction(method, path string) string {
	if strings.Contains(path, "/session/") {
		return "authenticate"
	}
	switch method {
	case fasthttp.MethodPost:
		return "create"
	case fasthttp.MethodPut, fasthttp.MethodPatch:
		return "update"
	case fasthttp.MethodDelete:
		return "delete"
	default:
		return "authorize"
	}
}
func auditOutcome(status int) string {
	if status >= 200 && status < 300 {
		return "success"
	}
	if status == fasthttp.StatusAccepted {
		return "pending"
	}
	return "failure"
}
func auditInitiator(ctx *fasthttp.RequestCtx) string {
	if _, ok := ctx.UserValue(schemas.BifrostContextKeySessionToken).(string); ok {
		return "dashboard-session"
	}
	if _, ok := ctx.UserValue(schemas.IsLocalAdminContextKey).(bool); ok {
		return "local-admin"
	}
	return "api-client"
}
func auditRequestIP(ctx *fasthttp.RequestCtx) string {
	if forwarded := clientForwardedIP(ctx); forwarded != "" {
		return forwarded
	}
	if addr := ctx.RemoteAddr(); addr != nil {
		return addr.String()
	}
	return ""
}
func auditSignature(key []byte, event tables.TableAuditLog) string {
	value := strings.Join([]string{event.ID, event.OccurredAt.UTC().Format(time.RFC3339Nano), event.Action, event.Outcome, event.Initiator, event.Target, event.Method, event.Path, strconv.FormatInt(event.DurationMS, 10), event.TypeURI}, "\n")
	mac := hmac.New(sha256.New, key)
	_, _ = mac.Write([]byte(value))
	return hex.EncodeToString(mac.Sum(nil))
}
