package admin

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"io"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/getkin/kin-openapi/openapi3"
	"github.com/gin-gonic/gin"
	"github.com/opennavo/opennavo/server/internal/domain"
	"github.com/opennavo/opennavo/server/internal/http/respond"
	"github.com/opennavo/opennavo/server/internal/service/agent"
	"github.com/opennavo/opennavo/server/internal/service/auth"
	"github.com/opennavo/opennavo/server/internal/store"
)

type AgentPolicy struct {
	Permissions   []string `json:"permissions"`
	DeleteAction  string   `json:"deleteAction"`
	Introspection bool     `json:"introspection"`
	Operation     string
}

func AgentPolicies(spec *openapi3.T) map[string]AgentPolicy {
	out := map[string]AgentPolicy{}
	for path, item := range spec.Paths.Map() {
		segments := strings.Split(path, "/")
		for i, v := range segments {
			if strings.HasPrefix(v, "{") {
				segments[i] = ":" + strings.Trim(v, "{}")
			}
		}
		for method, op := range item.Operations() {
			raw, ok := op.Extensions["x-agent-access"]
			if !ok || raw == false {
				continue
			}
			b, err := json.Marshal(raw)
			if err != nil {
				continue
			}
			var p AgentPolicy
			if json.Unmarshal(b, &p) != nil {
				continue
			}
			p.Operation = strings.ToLower(op.OperationID[:1]) + op.OperationID[1:]
			out[method+" "+strings.Join(segments, "/")] = p
		}
	}
	return out
}
func (h *Handler) agentService() *agent.Service {
	if h.Management == nil {
		return nil
	}
	return &agent.Service{Store: h.Management.Store, GatewaySecret: h.Management.Config.AgentGatewaySecret}
}

type agentWriter struct {
	gin.ResponseWriter
	prefix []byte
}

func (w *agentWriter) Write(b []byte) (int, error) {
	if len(w.prefix) < 512 {
		w.prefix = append(w.prefix, b[:min(len(b), 512-len(w.prefix))]...)
	}
	return w.ResponseWriter.Write(b)
}

var responseCode = regexp.MustCompile(`"code"\s*:\s*"([0-9]{4})"`)
var agentToolPattern = regexp.MustCompile(`^[a-z][a-z0-9_]{0,127}$`)

func (h *Handler) AgentMiddleware(policies map[string]AgentPolicy) gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Header("Cache-Control", "no-store")
		svc := h.agentService()
		if svc == nil {
			respond.Error(c, agent.Error(domain.CodeForbidden), true)
			return
		}
		id, err := svc.Authenticate(c.Request.Context(), c.GetHeader("X-Agent-Gateway-Key"), auth.Bearer(c.GetHeader("Authorization")), c.GetHeader("X-Agent-Client-IP"))
		if err != nil {
			respond.Error(c, err, true)
			return
		}
		p, ok := policies[c.Request.Method+" "+strings.TrimPrefix(c.FullPath(), "/agent-api")]
		if !ok {
			respond.Error(c, agent.Error(domain.CodeForbidden), true)
			return
		}
		op := &domain.Operation{RequestID: c.GetString("requestId"), Name: p.Operation, Tool: c.GetHeader("X-Agent-Tool"), ActorName: id.ClientName, ActorID: &id.UserID, AgentClientID: id.ClientID, AgentTokenID: id.ID, Permissions: id.Permissions, RequiredPermissions: p.Permissions, AllowDelete: id.AllowDelete, DryRun: c.Query("dryRun") == "true"}
		op.WebBaseURL = h.Management.Config.WebBaseURL
		if c.Request.Method != http.MethodGet && !p.Introspection && !agentControlOperation(p.Operation) {
			var err error
			op.Execution, err = optionalCatalogExecution(p.Operation, c.Request.Header, op.DryRun)
			if err != nil {
				respond.Error(c, err, true)
				return
			}
		}
		if c.GetHeader("X-Request-ID") == "" || len(c.GetHeader("X-Request-ID")) > 128 || (!p.Introspection && !agentToolPattern.MatchString(op.Tool)) {
			respond.Error(c, domain.Validation(), true)
			return
		}
		callID, finish, ok := beginAgentCall(c, svc, id, op, c.GetHeader("X-Agent-Client-IP"), c.Request.Method != "GET" && !p.Introspection)
		if !ok {
			return
		}
		defer finish()
		for _, permission := range p.Permissions {
			if !op.Allowed(permission) {
				respond.Error(c, agent.Error(domain.CodeForbidden), true)
				return
			}
		}
		var body map[string]any
		if strings.HasPrefix(c.GetHeader("Content-Type"), "application/json") {
			raw, err := io.ReadAll(io.LimitReader(c.Request.Body, 1024*1024+1))
			if err != nil || len(raw) > 1024*1024 || json.Unmarshal(raw, &body) != nil {
				respond.Error(c, domain.Validation(), true)
				return
			}
			c.Request.Body = io.NopCloser(bytes.NewReader(raw))
			c.Set("adminBody", raw)
		}
		if !id.AllowDelete && (p.DeleteAction == "always" || (p.DeleteAction == "clear" && body["clear"] == true)) {
			respond.Error(c, agent.Error(domain.CodeForbidden), true)
			return
		}
		for _, v := range body {
			if list, ok := v.([]any); ok && len(list) > id.Limits.BatchLimit {
				respond.Error(c, agent.Error(domain.CodeRateLimited), true)
				return
			}
		}
		write := 0
		if c.Request.Method != "GET" && !p.Introspection && !op.DryRun {
			write = 1
		}
		if err := svc.Reserve(c.Request.Context(), id, write, 0); err != nil {
			respond.Error(c, err, true)
			return
		}
		fields := []string{}
		for k := range body {
			fields = append(fields, k)
		}
		summary, _ := json.Marshal(map[string]any{"fields": fields})
		if err := svc.Store.DB.WithContext(c.Request.Context()).Exec(`UPDATE agent_call_logs SET params_summary=?::jsonb,llm_operation=? WHERE id=?`, string(summary), false, callID).Error; err != nil {
			respond.Error(c, err, true)
			return
		}
		if !touchAgentToken(c, svc, id, c.GetHeader("X-Agent-Client-IP")) {
			return
		}
		c.Next()
	}
}

// agentUpload admits POST /admin-api/assets/upload-tickets/:ticket. The single-use ticket replaces the bearer
// credential and is consumed before the body is read, so unauthenticated uploads never reach multipart decoding.
func (h *Handler) agentUpload(c *gin.Context) {
	svc := h.agentService()
	if svc == nil {
		respond.Error(c, agent.Error(domain.CodeForbidden), true)
		return
	}
	ip := c.ClientIP()
	grant, id, err := svc.RedeemUploadTicket(c.Request.Context(), c.Param("ticket"), ip)
	if err != nil {
		respond.Error(c, err, true)
		return
	}
	// Choose the request ID server-side so a direct upload cannot collide with or join another call's audit trail.
	requestID := rand.Text()
	c.Set("requestId", requestID)
	c.Header("X-Request-ID", requestID)
	op := &domain.Operation{RequestID: requestID, Name: "uploadAssetWithTicket", Tool: grant.Tool, ActorName: id.ClientName, ActorID: &id.UserID, AgentClientID: id.ClientID, AgentTokenID: id.ID, Permissions: id.Permissions, RequiredPermissions: []string{"catalog:asset:upload"}, AllowDelete: id.AllowDelete, Execution: grant.Execution}
	op.WebBaseURL = h.Management.Config.WebBaseURL
	callID, finish, ok := beginAgentCall(c, svc, id, op, ip, true)
	if !ok {
		return
	}
	defer finish()
	if !op.Allowed("catalog:asset:upload") {
		respond.Error(c, agent.Error(domain.CodeForbidden), true)
		return
	}
	// Issuing the ticket already reserved the write quota; link this upload to that call instead of charging again.
	summary, _ := json.Marshal(map[string]any{"fields": []string{"file"}, "kind": grant.Kind, "ticketRequestId": grant.RequestID})
	if err := svc.Store.DB.WithContext(c.Request.Context()).Exec(`UPDATE agent_call_logs SET params_summary=?::jsonb WHERE id=?`, string(summary), callID).Error; err != nil {
		respond.Error(c, err, true)
		return
	}
	if !touchAgentToken(c, svc, id, ip) {
		return
	}
	c.Set("agentUpload", grant)
	c.Next()
}

// beginAgentCall binds the identity to the request and reserves its request ID first, before business commits can
// hide conflicts. finish records the outcome; summaries include field names only, never bodies or credentials.
func beginAgentCall(c *gin.Context, svc *agent.Service, id agent.Identity, op *domain.Operation, ip string, write bool) (int64, func(), bool) {
	c.Request = c.Request.WithContext(domain.WithOperation(c.Request.Context(), op))
	c.Set("agentIdentity", id)
	c.Set("adminActor", store.AuditActor{ID: &id.UserID, IP: ip, UserAgent: c.GetHeader("User-Agent")})
	c.Set("userId", id.UserID)
	var callID int64
	if err := svc.Store.DB.WithContext(c.Request.Context()).Raw(`INSERT INTO agent_call_logs(request_id,client_id,client_name,token_id,token_prefix,tool,operation_id,code,dry_run,is_write) VALUES(?,?,?,?,?,?,?,'5000',?,?) ON CONFLICT(request_id) DO NOTHING RETURNING id`, op.RequestID, id.ClientID, id.ClientName, id.ID, id.Prefix, op.Tool, op.Name, op.DryRun, write).Scan(&callID).Error; err != nil {
		respond.Error(c, err, true)
		return 0, nil, false
	}
	if callID == 0 {
		respond.Error(c, agent.Error(domain.CodeConflict), true)
		return 0, nil, false
	}
	w := &agentWriter{ResponseWriter: c.Writer}
	c.Writer = w
	start := time.Now()
	return callID, func() {
		code := domain.CodeInternal
		if m := responseCode.FindSubmatch(w.prefix); len(m) > 1 {
			code = string(m[1])
		}
		ctx, cancel := context.WithTimeout(context.WithoutCancel(c.Request.Context()), 5*time.Second)
		defer cancel()
		_ = svc.Store.DB.WithContext(ctx).Exec(`UPDATE agent_call_logs SET code=?,duration_ms=?,objects=COALESCE((SELECT jsonb_agg(DISTINCT jsonb_build_object('entity',entity,'objectKey',object_key)) FROM content_revisions WHERE request_id=?),'[]'::jsonb),result_summary=jsonb_build_object('revisionIds',COALESCE((SELECT jsonb_agg(id ORDER BY id) FROM content_revisions WHERE request_id=?),'[]'::jsonb)) WHERE id=?`, code, time.Since(start).Milliseconds(), op.RequestID, op.RequestID, callID).Error
	}, true
}

func touchAgentToken(c *gin.Context, svc *agent.Service, id agent.Identity, ip string) bool {
	if err := svc.Store.DB.WithContext(c.Request.Context()).Exec("UPDATE agent_tokens SET last_used_at=now(),last_used_ip=?::inet WHERE id=?", ip, id.ID).Error; err != nil {
		respond.Error(c, err, true)
		return false
	}
	return true
}

// Execution metadata is optional for all authorized business writes. When
// supplied, a revision enables optimistic concurrency and a key enables replay.
func optionalCatalogExecution(_ string, headers http.Header, _ bool) (*domain.AgentExecution, error) {
	out := &domain.AgentExecution{IdempotencyKey: headers.Get("Idempotency-Key")}
	if out.IdempotencyKey != "" && (len(out.IdempotencyKey) < 8 || len(out.IdempotencyKey) > 200) {
		return nil, domain.Validation()
	}
	if raw := headers.Get("X-SEO-Expected-Revision"); raw != "" {
		value, err := strconv.ParseInt(raw, 10, 64)
		if err != nil || value < 0 {
			return nil, domain.Validation()
		}
		out.ExpectedRevision, out.ExpectedRevisionSet = value, true
	}
	return out, nil
}

// Pass only allowlisted operations to the generated router; system and similar paths are never registered.
type AgentRouter struct {
	gin.IRouter
	Policies map[string]AgentPolicy
}

func (r AgentRouter) register(method, path string, h ...gin.HandlerFunc) gin.IRoutes {
	if _, ok := r.Policies[method+" "+path]; ok {
		return r.IRouter.Handle(method, path, h...)
	}
	return r.IRouter
}
func (r AgentRouter) GET(p string, h ...gin.HandlerFunc) gin.IRoutes {
	return r.register(http.MethodGet, p, h...)
}
func (r AgentRouter) POST(p string, h ...gin.HandlerFunc) gin.IRoutes {
	return r.register(http.MethodPost, p, h...)
}
func (r AgentRouter) PUT(p string, h ...gin.HandlerFunc) gin.IRoutes {
	return r.register(http.MethodPut, p, h...)
}
func (r AgentRouter) DELETE(p string, h ...gin.HandlerFunc) gin.IRoutes {
	return r.register(http.MethodDelete, p, h...)
}

// Control operations and batch translations parse their own optional execution
// metadata. Task claim/complete/defer still enforce the queue lease contract.
func agentControlOperation(operation string) bool {
	switch operation {
	case "claimSeoTasks", "completeSeoTask", "deferSeoTask", "reopenSeoTask", "nominateSeoListing", "nominateSeoTopic", "createSeoEvidence", "updateSeoAutomation", "importSeoTasks", "writeTranslations", "recordSeoGeo":
		return true
	default:
		return false
	}
}
