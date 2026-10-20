package articles

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"regexp"
	"slices"
	"strings"
	"time"
	"unicode"
)

// Claims are editor-supplied references, not publication approvals. Publication
// rebinds them to server snapshots and immutable, administrator-attested reports.
type Claim struct {
	Text           string `json:"text"`
	Kind           string `json:"kind"`
	EvidenceID     int64  `json:"evidenceId"`
	Excerpt        string `json:"excerpt"`
	PackageID      *int64 `json:"packageId"`
	AppVersion     string `json:"appVersion"`
	Region         string `json:"region"`
	TestArtifactID *int64 `json:"testArtifactId"`
}
type Target struct {
	EntityKey string `json:"entityKey"`
	Intent    string `json:"intent"`
}
type ClaimEvidence struct {
	EvidenceID       int64     `json:"evidenceId"`
	SourceURL        string    `json:"sourceUrl"`
	BodyHash         string    `json:"bodyHash"`
	CheckedAt        time.Time `json:"checkedAt"`
	ValidUntil       time.Time `json:"validUntil"`
	TestArtifactID   *int64    `json:"testArtifactId"`
	TestArtifactHash *string   `json:"testArtifactHash"`
}
type TestArtifactInput struct {
	PackageID    int64             `json:"packageId"`
	AppVersion   string            `json:"appVersion"`
	MacOS        string            `json:"macOS"`
	Architecture string            `json:"architecture"`
	Region       string            `json:"region"`
	PerformedAt  time.Time         `json:"performedAt"`
	Runner       string            `json:"runner"`
	Command      string            `json:"command"`
	Environment  map[string]string `json:"environment"`
	Output       string            `json:"output"`
	Conclusions  []string          `json:"conclusions"`
	Attested     bool              `json:"attested"`
}

func (a TestArtifactInput) Hash() string {
	raw, _ := json.Marshal(a)
	h := sha256.Sum256(raw)
	return hex.EncodeToString(h[:])
}

var claimRegion = regexp.MustCompile(`^[A-Z]{2}$`)
var targetEntity = regexp.MustCompile(`^(pkg:[1-9][0-9]*|topic:[a-z0-9][a-z0-9-]*)$`)

func (t Target) Valid() bool {
	return len(t.EntityKey) <= 300 && targetEntity.MatchString(t.EntityKey) && slices.Contains([]string{"download", "install", "brew", "uninstall", "alternative", "price", "version", "requirements", "compare", "topic"}, t.Intent)
}
func ValidClaims(claims []Claim) bool {
	if len(claims) > 200 {
		return false
	}
	for _, c := range claims {
		if strings.TrimSpace(c.Text) == "" || len(c.Text) > 20000 || !slices.Contains([]string{"factual", "test"}, c.Kind) || c.EvidenceID < 1 || strings.TrimSpace(c.Excerpt) == "" || len(c.Excerpt) > 20000 || len(c.AppVersion) > 100 || !claimRegion.MatchString(c.Region) || c.PackageID != nil && *c.PackageID < 1 || c.TestArtifactID != nil && *c.TestArtifactID < 1 {
			return false
		}
		if c.Kind == "test" && (c.TestArtifactID == nil || c.PackageID == nil || c.AppVersion == "") {
			return false
		}
	}
	return true
}
func (a TestArtifactInput) Valid(now time.Time) bool {
	if !a.Attested || a.PackageID < 1 || strings.TrimSpace(a.AppVersion) == "" || len(a.AppVersion) > 100 || strings.TrimSpace(a.MacOS) == "" || len(a.MacOS) > 100 || !slices.Contains([]string{"arm64", "x86_64", "universal"}, a.Architecture) || !claimRegion.MatchString(a.Region) || a.PerformedAt.IsZero() || a.PerformedAt.After(now.Add(5*time.Minute)) || strings.TrimSpace(a.Runner) == "" || len(a.Runner) > 200 || strings.TrimSpace(a.Command) == "" || len(a.Command) > 4096 || strings.TrimSpace(a.Output) == "" || len(a.Output) > 262144 || len(a.Conclusions) == 0 || len(a.Conclusions) > 100 || len(a.Environment) == 0 || len(a.Environment) > 40 {
		return false
	}
	for k, v := range a.Environment {
		if len(k) > 100 || len(v) > 2000 {
			return false
		}
	}
	for _, c := range a.Conclusions {
		if strings.TrimSpace(c) == "" || len(c) > 20000 {
			return false
		}
	}
	raw, err := json.Marshal(a)
	return err == nil && len(raw) <= 400000
}

var testingLanguage = regexp.MustCompile(`(?i)\b(?:we (?:tested|benchmarked)|our (?:tests|benchmarks)|faster|safer|benchmark|tested|performance|latency|throughput|fps|schneller|sicherer|getestet|testé|testato|testamos|быстрее|безопаснее|протестировали)\b|我们(?:测试|实测)|我們(?:測試|實測)|更快|更安全|耗时|耗時|性能|帧率|幀率|plus rapide|plus sûr|più veloce|più sicuro|más rápido|más seguro|mais rápido|mais seguro|実測|テスト結果|实测|實測|测试结果|測試結果`)

func TestingClaim(text string) bool         { return testingLanguage.MatchString(text) }
func NormalizeAssertion(text string) string { return strings.Join(strings.Fields(text), " ") }

// CompleteAssertion only accepts complete sentence ranges, never a favorable
// fragment extracted from a negative or conditional source statement.
func CompleteAssertion(assertion, body string) bool {
	wanted := strings.TrimRight(NormalizeAssertion(assertion), ".!?。！？")
	source := NormalizeAssertion(body)
	if wanted == "" {
		return false
	}
	boundary := func(value string) bool { return strings.ContainsAny(value, ".!?。！？") }
	for offset := 0; offset <= len(source)-len(wanted); {
		found := strings.Index(source[offset:], wanted)
		if found < 0 {
			return false
		}
		start := offset + found
		end := start + len(wanted)
		before := strings.TrimSpace(source[:start])
		after := strings.TrimSpace(source[end:])
		left := before == ""
		if !left {
			r := []rune(before)
			left = boundary(string(r[len(r)-1])) && start > 0 && (unicode.IsSpace(rune(source[start-1])) || r[len(r)-1] > 127)
		}
		right := after == ""
		if !right {
			r := []rune(after)
			right = boundary(string(r[0])) && (len(r) == 1 || unicode.IsSpace(r[1]) || r[0] > 127)
		}
		if left && right {
			return true
		}
		offset = start + 1
	}
	return false
}

var markdownLinks = regexp.MustCompile(`!?\[([^\]]*)\]\([^\s)]+\)`)
var appCardLine = regexp.MustCompile(`(?m)^\s*::app\[[a-z0-9+@._-]+\]\s*$`)

// Every visible textual span must be supported. Supplying a valid claim for one
// sentence never gives permission to publish unsupported neighboring prose.
var assertionSentence = regexp.MustCompile(`(?:[.!?][ \t]+|[。！？])`)
var markdownPrefix = regexp.MustCompile(`^(?:#{1,6}[ \t]+|[-+*][ \t]+|[0-9]+\.[ \t]+|>[ \t]*)`)

func ClaimsCover(text string, claims []Claim) bool {
	text = appCardLine.ReplaceAllString(text, "")
	text = markdownLinks.ReplaceAllString(text, "$1")
	text = strings.NewReplacer("**", "", "__", "", "~~", "", "`", "").Replace(text)
	for _, line := range strings.Split(text, "\n") {
		line = markdownPrefix.ReplaceAllString(strings.TrimSpace(line), "")
		for _, unit := range assertionSentence.Split(line, -1) {
			unit = strings.TrimSpace(unit)
			substantive := false
			for _, r := range unit {
				if unicode.IsLetter(r) || unicode.IsNumber(r) {
					substantive = true
					break
				}
			}
			if !substantive {
				continue
			}
			covered := false
			for _, c := range claims {
				if CompleteAssertion(unit, c.Text) {
					covered = true
					break
				}
			}
			if !covered {
				return false
			}
		}
	}
	return true
}
