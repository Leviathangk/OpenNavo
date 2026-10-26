//go:build integration

package httpserver_test

import (
	"bytes"
	"context"
	"encoding/json"
	"image"
	"image/png"
	"io"
	"log/slog"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/opennavo/opennavo/server/gen/adminapi"
	"github.com/opennavo/opennavo/server/internal/config"
	httpserver "github.com/opennavo/opennavo/server/internal/http"
	"github.com/opennavo/opennavo/server/internal/http/admin"
	"github.com/opennavo/opennavo/server/internal/service/agent"
	"github.com/opennavo/opennavo/server/internal/service/assets"
	"github.com/opennavo/opennavo/server/internal/service/auth"
	"github.com/opennavo/opennavo/server/internal/service/desktop"
	"github.com/opennavo/opennavo/server/internal/service/management"
	"github.com/opennavo/opennavo/server/internal/service/seed"
	"github.com/opennavo/opennavo/server/internal/store"
	"github.com/opennavo/opennavo/server/internal/testutil"
	"github.com/opennavo/opennavo/server/seeds"
	"github.com/stretchr/testify/require"
)

func TestAgentAssetUploadTicketLifecycle(t *testing.T) {
	ctx := context.Background()
	st := testutil.NewFactsStore(t)
	data, err := seeds.Load()
	require.NoError(t, err)
	hash, err := seed.HashPassword("upload-ticket-test-only")
	require.NoError(t, err)
	require.NoError(t, st.Seed(ctx, data, "ticket-admin", hash))
	cfg := config.Config{AppEnv: "prod", AdminOrigin: "https://admin.opennavo.example", JWTSecret: strings.Repeat("u", 32), JWTAccessTTL: time.Hour, JWTRefreshTTL: 24 * time.Hour, AgentGatewaySecret: strings.Repeat("g", 32), WebBaseURL: "https://example.test"}
	authentication, err := auth.New(st, cfg)
	require.NoError(t, err)
	pair, err := authentication.Login(ctx, "ticket-admin", "upload-ticket-test-only", store.AuditActor{})
	require.NoError(t, err)
	manager := &management.Service{Store: st, Config: cfg, Desktop: &desktop.Service{Store: st}}
	server, err := httpserver.New(cfg, slog.New(slog.NewTextHandler(io.Discard, nil)), st, httpserver.Dependencies{Admin: &admin.Handler{Auth: authentication, Management: manager, Assets: &assets.Service{Store: st, Objects: memoryObjects{}}}})
	require.NoError(t, err)
	spec, err := adminapi.GetSpec()
	require.NoError(t, err)
	send := func(r *http.Request, base, expected string) json.RawMessage {
		t.Helper()
		w := httptest.NewRecorder()
		server.Engine.ServeHTTP(w, r)
		require.Equal(t, 200, w.Code)
		var out struct {
			Code string
			Data json.RawMessage
		}
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &out))
		require.Equal(t, expected, out.Code, r.URL.Path+" "+w.Body.String())
		if expected == "0000" {
			testutil.ValidateResponse(t, spec, base, r, w)
		}
		return out.Data
	}
	backend := func(method, path, body, expected string) json.RawMessage {
		t.Helper()
		r := httptest.NewRequest(method, "/admin-api"+path, strings.NewReader(body))
		r.Header.Set("Authorization", "Bearer "+pair.Token)
		if body != "" {
			r.Header.Set("Content-Type", "application/json")
		}
		return send(r, "/admin-api", expected)
	}
	var client struct{ ID int64 }
	require.NoError(t, json.Unmarshal(backend("POST", "/agent/clients", `{"name":"Ticket Hermes"}`, "0000"), &client))
	var credential struct {
		Plaintext string
		Token     struct{ ID int64 }
	}
	require.NoError(t, json.Unmarshal(backend("POST", "/agent/clients/"+strconv.FormatInt(client.ID, 10)+"/tokens", `{"name":"uploader","permissions":["catalog:asset:upload"],"allowDelete":false,"ipAllowlist":["127.0.0.1/32"]}`, "0000"), &credential))
	tokenPath := "/agent/tokens/" + strconv.FormatInt(credential.Token.ID, 10)
	fixture := httpExecutionFixture{t: t, st: st}
	issue := func(query, body, expected string) json.RawMessage {
		t.Helper()
		r := httptest.NewRequest("POST", "/agent-api/assets/upload-tickets"+query, strings.NewReader(body))
		r.Header.Set("Authorization", "Bearer "+credential.Plaintext)
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("X-Agent-Gateway-Key", cfg.AgentGatewaySecret)
		r.Header.Set("X-Agent-Client-IP", "127.0.0.1")
		r.Header.Set("X-Agent-Tool", "asset_upload_ticket")
		r.Header.Set("X-Request-ID", uuid.NewString())
		fixture.attach(r, "asset:new:http-ticket")
		return send(r, "/agent-api", expected)
	}
	type ticket struct {
		UploadURL string    `json:"uploadUrl"`
		Kind      string    `json:"kind"`
		MaxBytes  int64     `json:"maxBytes"`
		ExpiresAt time.Time `json:"expiresAt"`
	}
	newTicket := func(body string) (ticket, string) {
		t.Helper()
		var issued ticket
		require.NoError(t, json.Unmarshal(issue("", body, "0000"), &issued))
		path := strings.TrimPrefix(issued.UploadURL, "https://admin.opennavo.example")
		require.True(t, strings.HasPrefix(path, "/admin-api/assets/upload-tickets/onvu_"), issued.UploadURL)
		return issued, path
	}
	var icon bytes.Buffer
	require.NoError(t, png.Encode(&icon, image.NewNRGBA(image.Rect(0, 0, 256, 256))))
	upload := func(path, ip, expected string, fields ...string) json.RawMessage {
		t.Helper()
		var body bytes.Buffer
		w := multipart.NewWriter(&body)
		for _, name := range fields {
			require.NoError(t, w.WriteField(name, "icon"))
		}
		part, err := w.CreateFormFile("file", "icon.png")
		require.NoError(t, err)
		_, err = part.Write(icon.Bytes())
		require.NoError(t, err)
		require.NoError(t, w.Close())
		r := httptest.NewRequest("POST", path, &body)
		r.Header.Set("Content-Type", w.FormDataContentType())
		r.RemoteAddr = ip + ":40000"
		return send(r, "/admin-api", expected)
	}
	writes := func() int {
		t.Helper()
		value, err := st.Redis.Get(ctx, "agent:quota:"+strconv.FormatInt(credential.Token.ID, 10)+":write:"+time.Now().UTC().Format("20060102")).Int()
		require.NoError(t, err)
		return value
	}

	// A dry run validates the grant without storing a ticket or spending write quota.
	require.Contains(t, string(issue("?dryRun=true", `{"kind":"icon"}`, "0000")), `"dryRun":true`)
	keys, err := st.Redis.Keys(ctx, "agent:upload:*").Result()
	require.NoError(t, err)
	require.Empty(t, keys)
	issue("", `{"kind":"og"}`, "1001")

	issued, path := newTicket(`{"kind":"icon","sourceUrl":"https://code.visualstudio.com/"}`)
	require.Equal(t, "icon", issued.Kind)
	require.EqualValues(t, 5*1024*1024, issued.MaxBytes)
	require.WithinDuration(t, time.Now().Add(agent.UploadTicketTTL), issued.ExpiresAt, time.Minute)
	plaintext := strings.TrimPrefix(path, "/admin-api/assets/upload-tickets/")
	stored, err := st.Redis.Get(ctx, "agent:upload:"+agent.Hash(plaintext)).Result()
	require.NoError(t, err)
	require.NotContains(t, stored, plaintext)
	charged := writes()

	// Oversized bodies are rejected before the ticket is touched, so the same ticket still works afterwards.
	oversized := httptest.NewRequest("POST", path, strings.NewReader(""))
	oversized.ContentLength = 7 * 1024 * 1024
	send(oversized, "/admin-api", "1007")
	var asset struct {
		ID   int64
		Kind string
	}
	require.NoError(t, json.Unmarshal(upload(path, "127.0.0.1", "0000"), &asset))
	require.Equal(t, "icon", asset.Kind)
	require.Equal(t, charged, writes(), "redeeming a ticket must not charge the write quota again")
	upload(path, "127.0.0.1", "8888")
	var saved struct {
		SourceURL string
		CreatedBy int64
	}
	require.NoError(t, st.DB.Raw("SELECT source_url,created_by FROM assets WHERE id=?", asset.ID).Scan(&saved).Error)
	require.Equal(t, "https://code.visualstudio.com/", saved.SourceURL)
	var machine int64
	require.NoError(t, st.DB.Raw("SELECT user_id FROM agent_clients WHERE id=?", client.ID).Scan(&machine).Error)
	require.Equal(t, machine, saved.CreatedBy)
	var call struct {
		RequestID, Tool, Code string
		IsWrite               bool
		ParamsSummary         string
	}
	require.NoError(t, st.DB.Raw("SELECT request_id,tool,code,is_write,params_summary::text FROM agent_call_logs WHERE operation_id='uploadAssetWithTicket' ORDER BY id DESC LIMIT 1").Scan(&call).Error)
	require.Equal(t, "asset_upload_ticket", call.Tool)
	require.Equal(t, "0000", call.Code)
	require.True(t, call.IsWrite)
	require.Contains(t, call.ParamsSummary, "ticketRequestId")
	var audited int64
	require.NoError(t, st.DB.Raw("SELECT count(*) FROM audit_logs WHERE action='asset.upload' AND actor_id=? AND request_id=?", machine, call.RequestID).Scan(&audited).Error)
	require.EqualValues(t, 1, audited)

	// The kind is fixed at issue time; extra fields fail validation after the ticket is spent.
	_, path = newTicket(`{"kind":"screenshot"}`)
	upload(path, "127.0.0.1", "1001", "kind")
	upload(path, "127.0.0.1", "8888")
	_, path = newTicket(`{"kind":"screenshot"}`)
	require.NoError(t, json.Unmarshal(upload(path, "127.0.0.1", "0000"), &asset))
	require.Equal(t, "screenshot", asset.Kind)

	// Token checks are repeated for the uploading request: IP allowlist, expiry, permission and revocation.
	_, path = newTicket(`{"kind":"icon"}`)
	upload(path, "192.0.2.10", "1004")
	_, path = newTicket(`{"kind":"icon"}`)
	require.NoError(t, st.Redis.PExpire(ctx, "agent:upload:"+agent.Hash(strings.TrimPrefix(path, "/admin-api/assets/upload-tickets/")), time.Millisecond).Err())
	time.Sleep(20 * time.Millisecond)
	upload(path, "127.0.0.1", "8888")
	_, path = newTicket(`{"kind":"icon"}`)
	backend("PUT", tokenPath, `{"permissions":["catalog:package:view"]}`, "0000")
	upload(path, "127.0.0.1", "1004")
	backend("PUT", tokenPath, `{"permissions":["catalog:asset:upload"]}`, "0000")
	_, path = newTicket(`{"kind":"icon"}`)
	backend("POST", tokenPath+"/revoke", "", "0000")
	upload(path, "127.0.0.1", "7777")

	// Admin JWTs never issue tickets, and they cannot stand in for a ticket on upload.
	backend("POST", "/assets/upload-tickets", `{"kind":"icon"}`, "1004")
	forged := httptest.NewRequest("POST", "/admin-api/assets/upload-tickets/onvu_"+strings.Repeat("x", 43), strings.NewReader(""))
	forged.Header.Set("Authorization", "Bearer "+pair.Token)
	send(forged, "/admin-api", "8888")
}
