//go:build integration

package httpserver_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/opennavo/opennavo/server/gen/adminapi"
	"github.com/opennavo/opennavo/server/gen/publicapi"
	"github.com/opennavo/opennavo/server/internal/config"
	httpserver "github.com/opennavo/opennavo/server/internal/http"
	"github.com/opennavo/opennavo/server/internal/http/admin"
	"github.com/opennavo/opennavo/server/internal/http/public"
	"github.com/opennavo/opennavo/server/internal/service/auth"
	"github.com/opennavo/opennavo/server/internal/service/desktop"
	"github.com/opennavo/opennavo/server/internal/service/management"
	"github.com/opennavo/opennavo/server/internal/service/seed"
	"github.com/opennavo/opennavo/server/internal/store"
	"github.com/opennavo/opennavo/server/internal/testutil"
	"github.com/opennavo/opennavo/server/seeds"
	"github.com/stretchr/testify/require"
)

type manifestObjects struct {
	data map[string][]byte
	fail bool
}

func (o *manifestObjects) Put(_ context.Context, key string, b []byte, _ string) error {
	if o.fail {
		o.fail = false
		return errors.New("simulated object error")
	}
	o.data[key] = append([]byte{}, b...)
	return nil
}
func (o *manifestObjects) Read(_ context.Context, key string) ([]byte, error) {
	return o.data[key], nil
}
func (o *manifestObjects) Delete(_ context.Context, key string) error {
	delete(o.data, key)
	return nil
}
func TestDesktopReleaseContractsCIManifestRollbackAndCompensation(t *testing.T) {
	ctx := context.Background()
	st := testutil.NewStore(t)
	hash, err := seed.HashPassword("test-password-strong")
	require.NoError(t, err)
	data, err := seeds.Load()
	require.NoError(t, err)
	require.NoError(t, st.Seed(ctx, data, "tester", hash))
	cfg := config.Config{JWTSecret: strings.Repeat("a", 32), JWTAccessTTL: time.Hour, JWTRefreshTTL: time.Hour}
	authentication, err := auth.New(st, cfg)
	require.NoError(t, err)
	pair, err := authentication.Login(ctx, "tester", "test-password-strong", store.AuditActor{})
	require.NoError(t, err)
	objects := &manifestObjects{data: map[string][]byte{}}
	svc := &desktop.Service{Store: st, Objects: objects}
	svc.Now = func() time.Time { return time.Date(2026, 10, 5, 0, 0, 0, 123456789, time.UTC) }
	mgr := &management.Service{Store: st, Desktop: svc}
	server, err := httpserver.New(cfg, slog.New(slog.NewTextHandler(io.Discard, nil)), st, httpserver.Dependencies{Admin: &admin.Handler{Auth: authentication, Management: mgr, CIToken: "local-ci-only"}, Public: &public.Handler{Desktop: svc}})
	require.NoError(t, err)
	spec, err := adminapi.GetSpec()
	require.NoError(t, err)
	pspec, err := publicapi.GetSpec()
	require.NoError(t, err)
	call := func(method, path, body, code, token string) *httptest.ResponseRecorder {
		t.Helper()
		r := httptest.NewRequest(method, "/admin-api"+path, strings.NewReader(body))
		r.Header.Set("Authorization", "Bearer "+token)
		if body != "" {
			r.Header.Set("Content-Type", "application/json")
		}
		w := httptest.NewRecorder()
		server.Engine.ServeHTTP(w, r)
		testutil.ValidateResponse(t, spec, "/admin-api", r, w)
		var out struct{ Code string }
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &out))
		require.Equal(t, code, out.Code, w.Body.String())
		require.Equal(t, 200, w.Code)
		return w
	}
	artifacts := `[{"target":"darwin-aarch64","url":"https://cdn.example.test/a.tar.gz","signature":"public-test-signature","bytes":42,"sha256":"` + strings.Repeat("a", 64) + `"},{"target":"darwin-x86_64","url":"https://cdn.example.test/b.tar.gz","signature":"public-test-signature","bytes":43,"sha256":"` + strings.Repeat("b", 64) + `"},{"target":"dmg-universal","url":"https://cdn.example.test/app.dmg","signature":null,"bytes":44,"sha256":"` + strings.Repeat("c", 64) + `"}]`
	create := func(version string) int64 {
		w := call("POST", "/desktop-releases", `{"version":"`+version+`","channel":"stable","sourceLocale":"zh-CN","i18n":{"zh-CN":{"notes":"中文更新"},"en-US":{"notes":"English notes"}},"artifacts":`+artifacts+`}`, "0000", "local-ci-only")
		var r struct{ Data struct{ ID int64 } }
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &r))
		return r.Data.ID
	}
	id1, id2 := create("0.0.1"), create("0.0.2")
	require.Equal(t, id1, create("0.0.1"), "Duplicate registration returns the existing ID")
	call("POST", "/desktop-releases", `{"version":"0.0.1","channel":"stable","minMacos":"14.0","artifacts":`+artifacts+`}`, "1006", "local-ci-only")
	changed := strings.Replace(artifacts, "https://cdn.example.test/a.tar.gz", "https://github.com/example/opennavo/releases/download/desktop-v0.0.1/a.tar.gz", 1)
	call("POST", "/desktop-releases", `{"version":"0.0.1","channel":"stable","artifacts":`+changed+`}`, "1006", "local-ci-only")
	one, two := strconv.FormatInt(id1, 10), strconv.FormatInt(id2, 10)
	call("GET", "/desktop-releases", "", "0000", pair.Token)
	call("GET", "/desktop-releases/"+one, "", "0000", pair.Token)
	call("POST", "/desktop-releases/"+one+"/publish", "", "1004", "local-ci-only")
	call("POST", "/desktop-releases/"+one+"/publish", "", "0000", pair.Token)
	require.Contains(t, string(objects.data["desktop/stable/latest.json"]), `"version":"0.0.1"`)
	call("POST", "/desktop-releases/"+one+"/rollback", "", "1006", pair.Token)
	before := string(objects.data["desktop/stable/latest.json"])
	objects.fail = true
	call("POST", "/desktop-releases/"+two+"/publish", "", "5000", pair.Token)
	require.Equal(t, before, string(objects.data["desktop/stable/latest.json"]))
	call("POST", "/desktop-releases/"+two+"/publish", "", "0000", pair.Token)
	call("POST", "/desktop-releases/"+one+"/publish", "", "1006", pair.Token)
	call("PUT", "/desktop-releases/"+two, `{"sourceLocale":"zh-CN","i18n":{"zh-CN":{"notes":"新版说明"}}}`, "0000", pair.Token)
	require.Contains(t, string(objects.data["desktop/stable/latest.json"]), "新版说明")
	require.Equal(t, id2, create("0.0.2"))
	retained, readErr := st.DesktopRelease(ctx, id2)
	require.NoError(t, readErr)
	require.Equal(t, "published", retained.Status)
	notes, _ := retained.LocalizedNotes("zh-CN")
	require.Equal(t, "新版说明", notes)
	require.Contains(t, string(objects.data["desktop/stable/latest.json"]), "新版说明")
	previous := string(objects.data["desktop/stable/latest.json"])
	badActor := int64(999)
	_, err = svc.Mutate(ctx, "UpdateDesktopRelease", id2, map[string]any{"sourceLocale": "zh-CN", "i18n": map[string]any{"zh-CN": map[string]any{"notes": "不能提交"}}}, store.AuditActor{ID: &badActor})
	require.Error(t, err)
	require.Equal(t, previous, string(objects.data["desktop/stable/latest.json"]))
	call("POST", "/desktop-releases/"+two+"/rollback", "", "0000", pair.Token)
	require.Equal(t, before, string(objects.data["desktop/stable/latest.json"]))
	var manifest desktop.Manifest
	require.NoError(t, json.Unmarshal(objects.data["desktop/stable/latest.json"], &manifest))
	require.Equal(t, "0.0.1", manifest.Version)
	require.Len(t, manifest.Platforms, 2)
	require.NotContains(t, manifest.Platforms, "dmg-universal")
	r := httptest.NewRequest("GET", "/api/v1/desktop/releases/latest?locale=en-US", nil)
	w := httptest.NewRecorder()
	server.Engine.ServeHTTP(w, r)
	testutil.ValidateResponse(t, pspec, "/api/v1", r, w)
	require.Contains(t, w.Body.String(), "English notes")
	require.Contains(t, w.Body.String(), `"version":"0.0.1"`)
	r = httptest.NewRequest("GET", "/api/v1/desktop/releases/latest?channel=beta", nil)
	w = httptest.NewRecorder()
	server.Engine.ServeHTTP(w, r)
	testutil.ValidateResponse(t, pspec, "/api/v1", r, w)
	require.Equal(t, 404, w.Code)
	// Execute the real CI registration script to prevent drift between the ten-language contract and JavaScript request body.
	apiServer := httptest.NewServer(server.Engine)
	t.Cleanup(apiServer.Close)
	dir := t.TempDir()
	var collected []map[string]any
	require.NoError(t, json.Unmarshal([]byte(artifacts), &collected))
	for _, artifact := range collected {
		encoded, encodeErr := json.Marshal(artifact)
		require.NoError(t, encodeErr)
		require.NoError(t, os.WriteFile(filepath.Join(dir, artifact["target"].(string)+".json"), encoded, 0o600))
	}
	root, rootErr := filepath.Abs("../../../..")
	require.NoError(t, rootErr)
	for range 2 {
		command := exec.CommandContext(ctx, "node", filepath.Join(root, "apps/desktop/scripts/register-release.mjs"), "--dir", dir, "--version", "0.0.3", "--notes-en", "Script notes") //nolint:gosec // Fixed controlled script and temporary test directory; no user input.
		command.Dir = root
		command.Env = []string{"PATH=" + os.Getenv("PATH"), "ADMIN_API_BASE=" + apiServer.URL + "/admin-api", "CI_RELEASE_TOKEN=local-ci-only"}
		output, runErr := command.CombinedOutput()
		require.NoError(t, runErr, "%s", output)
	}
	var count int64
	require.NoError(t, st.DB.Table("desktop_releases").Where("version=? AND channel=?", "0.0.3", "stable").Count(&count).Error)
	require.EqualValues(t, 1, count)
	var text string
	require.NoError(t, st.DB.Raw("SELECT notes FROM desktop_release_i18n WHERE desktop_release_id=(SELECT id FROM desktop_releases WHERE version='0.0.3' AND channel='stable') AND locale='en-US'").Scan(&text).Error)
	require.Equal(t, "Script notes", text)
	var registered store.DesktopRelease
	require.NoError(t, st.DB.Where("version=? AND channel=?", "0.0.3", "stable").First(&registered).Error)
	require.Equal(t, "en-US", registered.SourceLocale)
	call("GET", "/system/audit-logs?entityType=desktop_release", "", "0000", pair.Token)
}
