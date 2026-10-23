package caddytest

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"testing"
	"time"
)

const botHeader = "X-OpenNavo-Verified-Bot"

// What reached the upstream: the bot verdict and the edge country, both of which only Cloudflare may set.
type forwarded struct {
	Bot     []string `json:"bot"`
	Country []string `json:"country"`
}

// Run via verify-bot-ingress.py: real Caddy and upstream sockets live entirely in a
// --network none container. Only upstream destinations and (for trusted-peer cases)
// the Cloudflare ranges are replaced. All production routes/header handlers remain.
func TestVerifiedBotIngress(t *testing.T) {
	source, err := os.ReadFile("/fixture/Caddyfile")
	if err != nil {
		t.Fatal(err)
	}
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Query().Get("status") == "429" {
			w.WriteHeader(http.StatusTooManyRequests)
		}
		_ = json.NewEncoder(w).Encode(forwarded{r.Header.Values(botHeader), r.Header.Values("CF-IPCountry")})
	}))
	defer upstream.Close()
	for _, scenario := range []struct {
		name, flag string
		simulateCF bool
	}{
		{"unset", "", true}, {"disabled", "false", true},
		{"invalid-case", "TRUE", true}, {"invalid-number", "1", true},
		{"enabled-real-ranges", "true", false}, {"enabled-simulated-CF", "true", true},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			config := string(source)
			if scenario.simulateCF {
				// Linux loopback supports distinct actual peers without a privileged network.
				pattern := regexp.MustCompile(`@botNotCloudflare not remote_ip [^\n]+`)
				if len(pattern.FindAllString(config, -1)) != 1 {
					t.Fatal("expected exactly one production Cloudflare matcher")
				}
				config = pattern.ReplaceAllString(config, "@botNotCloudflare not remote_ip 127.0.0.2/32")
			}
			address := startCaddy(t, config, scenario.flag, strings.TrimPrefix(upstream.URL, "http://"))
			for _, peer := range []string{"127.0.0.1", "127.0.0.2"} {
				transport := &http.Transport{DialContext: (&net.Dialer{
					LocalAddr: &net.TCPAddr{IP: net.ParseIP(peer)}, Timeout: time.Second,
				}).DialContext}
				defer transport.CloseIdleConnections()
				client := &http.Client{Transport: transport, Timeout: 2 * time.Second}
				for _, route := range []struct{ host, path string }{
					{"web.example.test", "/"}, {"web.example.test", "/apps/fixture?status=429"},
					{"api.example.test", "/api/v1/packages/fixture"}, {"api.example.test", "/healthz"},
					{"admin.example.test", "/admin-api/fixture"}, {"admin.example.test", "/mcp"},
				} {
					for _, values := range [][]string{nil, {"true"}, {"false"}, {"TRUE"}, {"true,false"}, {"true", "true"}, {"true", "false"}, {""}} {
						request, err := http.NewRequest(http.MethodGet, "http://"+address+route.path, nil)
						if err != nil {
							t.Fatal(err)
						}
						request.Host = route.host
						// Neither forwarding header is allowed to turn a direct peer into Cloudflare.
						request.Header.Set("X-Forwarded-For", "173.245.48.1")
						request.Header.Set("CF-Connecting-IP", "173.245.48.1")
						request.Header.Set("CF-IPCountry", "US")
						for i, value := range values {
							name := botHeader
							if i%2 == 1 {
								name = strings.ToLower(name)
							}
							request.Header[name] = append(request.Header[name], value)
						}
						response, err := client.Do(request)
						if err != nil {
							t.Fatal(err)
						}
						var actual forwarded
						err = json.NewDecoder(response.Body).Decode(&actual)
						response.Body.Close()
						if err != nil {
							t.Fatalf("%s: %v", route.path, err)
						}
						status := http.StatusOK
						if strings.Contains(route.path, "status=429") {
							status = http.StatusTooManyRequests
						}
						if response.StatusCode != status {
							t.Fatalf("final status = %d, want %d", response.StatusCode, status)
						}
						var expected forwarded
						if scenario.flag == "true" && scenario.simulateCF && peer == "127.0.0.2" {
							expected.Bot = values
						}
						// The country passes from Cloudflare whatever the bot flag says, and never from anyone else.
						if scenario.simulateCF && peer == "127.0.0.2" {
							expected.Country = []string{"US"}
						}
						if !reflect.DeepEqual(actual, expected) {
							t.Fatalf("peer=%s %s%s input=%q got=%+v want=%+v", peer, route.host, route.path, values, actual, expected)
						}
					}
				}
			}
		})
	}
}

func startCaddy(t *testing.T, source, flag, upstream string) string {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	listener.Close()
	dir := t.TempDir()
	file := filepath.Join(dir, "Caddyfile")
	if err := os.WriteFile(file, []byte(source), 0600); err != nil {
		t.Fatal(err)
	}
	env := []string{
		"PATH=/usr/bin:/bin", "XDG_DATA_HOME=" + dir, "XDG_CONFIG_HOME=" + dir,
		fmt.Sprintf("WEB_DOMAIN=http://web.example.test:%d", port),
		fmt.Sprintf("API_DOMAIN=http://api.example.test:%d", port),
		fmt.Sprintf("ADMIN_DOMAIN=http://admin.example.test:%d", port),
		"PUBLIC_BASE_URL=https://api.example.test", "CADDY_GLOBAL_OPTIONS=admin off",
	}
	if flag != "" {
		env = append(env, "OPENNAVO_CF_VERIFIED_BOT_ENABLED="+flag)
	}
	adapt := exec.Command("caddy", "adapt", "--config", file, "--adapter", "caddyfile")
	adapt.Env = env
	var adaptErrors bytes.Buffer
	adapt.Stderr = &adaptErrors
	data, err := adapt.Output()
	if err != nil {
		t.Fatalf("adapt: %v: %s", err, adaptErrors.String())
	}
	var config any
	if err := json.Unmarshal(data, &config); err != nil {
		t.Fatal(err)
	}
	var rewrite func(any)
	proxyCount := 0
	rewrite = func(value any) {
		switch node := value.(type) {
		case map[string]any:
			if node["handler"] == "reverse_proxy" {
				delete(node, "dynamic_upstreams")
				node["upstreams"] = []any{map[string]any{"dial": upstream}}
				proxyCount++
			}
			for _, child := range node {
				rewrite(child)
			}
		case []any:
			for _, child := range node {
				rewrite(child)
			}
		}
	}
	rewrite(config)
	if proxyCount != 4 {
		t.Fatalf("expected four production upstreams, got %d", proxyCount)
	}
	data, err = json.Marshal(config)
	if err != nil {
		t.Fatal(err)
	}
	file = filepath.Join(dir, "caddy.json")
	if err := os.WriteFile(file, data, 0600); err != nil {
		t.Fatal(err)
	}
	log, err := os.Create(filepath.Join(dir, "caddy.log"))
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("caddy", "run", "--config", file)
	cmd.Env, cmd.Stdout, cmd.Stderr = env, log, log
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = cmd.Process.Signal(os.Interrupt)
		_ = cmd.Wait()
		log.Close()
		if t.Failed() {
			output, _ := os.ReadFile(log.Name())
			t.Logf("Caddy: %s", output)
		}
	})
	address := fmt.Sprintf("127.0.0.1:%d", port)
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		conn, err := net.DialTimeout("tcp", address, 100*time.Millisecond)
		if err == nil {
			conn.Close()
			return address
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("Caddy did not start")
	return ""
}
