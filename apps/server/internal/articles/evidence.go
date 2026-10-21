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

// A line that is only a link, optionally numbered, is a reference entry, and
// [1] after a sentence is a citation marker: attribution, not statements.
var referenceLine = regexp.MustCompile(`(?m)^[ \t]*(?:[-+*][ \t]+|[0-9]+\.[ \t]+)?(?:\[\^?[0-9]{1,3}\]:?[ \t]*)?\[[^\]]*\]\([^\s)]+\)[ \t]*$`)
var citationMarker = regexp.MustCompile(`\[\^?[0-9]{1,3}\]:?`)

// Only statements a reader could check need evidence: numbers (versions, prices,
// dates, sizes, counts), money, code, test or performance results, and pricing,
// licensing, platform-compatibility or safety terms. Explanatory prose that
// states none of these needs no claim; headings and table headers need one only
// for numbers, money, code or test results. Each table cell is its own unit.
var assertionSentence = regexp.MustCompile(`(?:[.!?][ \t]+|[。！？])`)
var markdownPrefix = regexp.MustCompile(`^(?:#{1,6}[ \t]+|[-+*][ \t]+|[0-9]+\.[ \t]+|>[ \t]*)`)
var headingLine = regexp.MustCompile(`^#{1,6}[ \t]`)
var tableSeparator = regexp.MustCompile(`^\|?[ \t]*:?-{3,}:?[ \t]*(?:\|[ \t]*:?-{3,}:?[ \t]*)*\|?$`)
var codeFence = regexp.MustCompile("^(?:```|~~~)")
var codeSpan = regexp.MustCompile("`([^`\n]+)`")
var numberToken = regexp.MustCompile(`\p{Nd}+(?:\.\p{Nd}+)*`)
var currencySign = regexp.MustCompile(`\p{Sc}`)

// Numbers compare without trailing zero groups: macOS 14 and 14.0, or $100 and
// $100.00, state the same number; 14 and 14.1 do not.
var trailingZeros = regexp.MustCompile(`(?:\.0+)+$`)

func numbers(text string) []string {
	out := numberToken.FindAllString(text, -1)
	for i, n := range out {
		out[i] = trailingZeros.ReplaceAllString(n, "")
	}
	return out
}

// Code compares without whitespace: pages render keys and options as separate
// elements, so a page's "Ctrl - Shift - C" is the article's `Ctrl-Shift-C`.
func compact(text string) string { return strings.Join(strings.Fields(text), "") }

// "Free up space" is disk cleanup, not a price.
var diskSpace = regexp.MustCompile(`(?i)\bfree(?:s|d|ing)?[ \t]+up\b|\bfree[ \t]+(?:disk[ \t]+)?space\b`)

// Each category is compared on its own: a claim stating a price, licence,
// platform or safety property needs an excerpt that names the same kind of
// property, in any of the site languages. states finds the fact in a sentence;
// names confirms that an excerpt is about the same kind of fact.
type factCategory struct{ states, names *regexp.Regexp }

const platformChips = `\b(?:apple silicon|intel|rosetta|arm64|x86_64|universal binary|tahoe|sequoia|sonoma|ventura|monterey|big sur|catalina|mojave|high sierra)\b|apple ?(?:芯片|晶片|シリコン)|苹果芯片|蘋果晶片`
const platformNames = `\b(?:mac|macs|macos|os x|windows|linux|ios|ipados|iphone|ipad|android)\b|苹果电脑|蘋果電腦`
const compatibilityVerbs = `\b(?:supports?|supported|unsupported|requires?|required|requirements?|compatib\w*|incompatib\w*|runs? on|available (?:for|on)|works? on|unterstützt|erfordert|kompatib\w*|läuft auf|prend en charge|nécessite|requiert|fonctionne sur|supporta|richiede|funziona su|admite|requiere|funciona en|suporta|requer|exige|compatív\w*|funciona em)\b|поддерж|требует|совместим|работает на|支持|兼容|适配|支援|相容|対応|互換|适用于|適用於|运行于|運行於`

func sameCategory(pattern string) factCategory {
	re := regexp.MustCompile(pattern)
	return factCategory{re, re}
}

var factCategories = []factCategory{
	// pricing
	sameCategory(`(?i)\b(?:free|freeware|paid|prices?|priced|pricing|costs?|subscriptions?|subscribe|trials?|purchases?|in-app|one-time)\b|免费|收费|付费|价格|售价|订阅|试用|买断|内购|免費|收費|付費|價格|售價|訂閱|試用|買斷|內購|無料|有料|価格|料金|サブスク|買い切り|課金|\b(?:kostenlos|kostenpflichtig|preis\w*|abo|abonnement\w*|testversion|kauf\w*|gratuit\w*|payant\w*|prix|tarif\w*|essai|gratis|prezz\w*|abbonament\w*|a pagamento|precio\w*|de pago|suscripci\w*|grátis|gratuit\w*|preço\w*|pago|assinatura\w*)\b|бесплатн|платн|стоимост|подписк|пробн`),
	// licensing
	sameCategory(`(?i)\b(?:licen[cs]es?|licen[cs]ed|licensing|open[- ]source|proprietary|closed[- ]source|lizenz\w*|quelloffen|licenza|licencia|licença)\b|开源|闭源|许可证|開源|閉源|許可證|授權條款|オープンソース|ライセンス|code source ouvert|codice aperto|código abierto|código aberto|лицензи|открыт\S* исходн`),
	// platform compatibility: chips, macOS releases, or a compatibility verb
	// naming a platform; "supports PDF" describes a feature and is not one
	{regexp.MustCompile(`(?i)` + platformChips + `|(?:` + compatibilityVerbs + `)[^.!?。！？\n]{0,40}(?:` + platformNames + `)`), regexp.MustCompile(`(?i)` + platformChips + `|` + platformNames + `|` + compatibilityVerbs)},
	// safety and security
	sameCategory(`(?i)\b(?:safe|safety|unsafe|secure|security|insecure|malware|malicious|virus|viruses|spyware|adware|privacy|encrypt\w*|notari[sz]\w*|code[- ]signed|sandbox\w*|telemetry|sicher\w*|schadsoftware|datenschutz|verschlüssel\w*|sécurité|sécurisé\w*|malveillant\w*|confidentialité|chiffr\w*|sicur\w*|crittograf\w*|segur\w*|privacidad|privacidade|cifrad\w*|criptograf\w*)\b|安全|恶意|病毒|隐私|加密|公证|代码签名|沙盒|遥测|木马|惡意|隱私|公證|代碼簽名|沙箱|遙測|木馬|マルウェア|ウイルス|プライバシー|暗号化|サンドボックス|безопас|вредонос|вирус|конфиденц|шифр`),
}

// Negation must agree between a claim and its excerpt, so a reworded claim can
// never turn "does not support" into "supports".
var negation = regexp.MustCompile(`(?i)\b(?:not|no|never|none|without|cannot|can't|doesn't|don't|isn't|aren't|wasn't|won't|nicht|kein\w*|nie|ohne|pas|jamais|sans|aucun\w*|non|senza|mai|nessun\w*|nunca|sin|ningún|ninguna|não|nem|sem|nenhum\w*)\b|不支持|不兼容|不提供|不包含|不再|不能|不会|不可|没有|无法|未提供|不是|并非|不支援|不相容|不會|沒有|無法|並非|ない|ません|非対応|できない|(?:^|\s)(?:не|нет|без|никогда)(?:\s|$)`)

// NeedsEvidence reports whether a sentence states something checkable.
func NeedsEvidence(text string, heading bool) bool {
	if numberToken.MatchString(text) || currencySign.MatchString(text) || codeSpan.MatchString(text) || TestingClaim(text) {
		return true
	}
	if heading {
		return false
	}
	plain := diskSpace.ReplaceAllString(text, "")
	for _, category := range factCategories {
		if category.states.MatchString(plain) {
			return true
		}
	}
	return false
}

// Supports reports whether an excerpt backs a claim written in the article's own
// words or language. A claim quoted verbatim always qualifies; otherwise every
// number, currency sign and code literal in the claim must appear in the
// excerpt, every fact category it names must appear there too, and both must
// agree on negation.
func Supports(claim, excerpt string) bool {
	text := strings.ReplaceAll(claim, "`", "")
	if CompleteAssertion(text, excerpt) {
		return true
	}
	source := NormalizeAssertion(excerpt)
	stated := numbers(source)
	for _, n := range numbers(claim) {
		if !slices.Contains(stated, n) {
			return false
		}
	}
	for _, sign := range currencySign.FindAllString(claim, -1) {
		if !strings.Contains(source, sign) {
			return false
		}
	}
	for _, code := range codeSpan.FindAllStringSubmatch(claim, -1) {
		if !strings.Contains(compact(source), compact(code[1])) {
			return false
		}
	}
	plainClaim, plainSource := diskSpace.ReplaceAllString(text, ""), diskSpace.ReplaceAllString(source, "")
	for _, category := range factCategories {
		if category.states.MatchString(plainClaim) && !category.names.MatchString(plainSource) {
			return false
		}
	}
	return negation.MatchString(text) == negation.MatchString(source)
}

// ClaimsCover requires a claim for every sentence that states something
// checkable; a claim for one sentence never covers a neighboring one. Code in
// a fence must appear in some claim's excerpt, and inline code in a covered
// sentence must appear in that claim's excerpt (whitespace aside).
func ClaimsCover(text string, claims []Claim) bool {
	text = appCardLine.ReplaceAllString(text, "")
	text = referenceLine.ReplaceAllString(text, "")
	text = markdownLinks.ReplaceAllString(text, "$1")
	text = citationMarker.ReplaceAllString(text, "")
	text = strings.NewReplacer("**", "", "__", "", "~~", "").Replace(text)
	lines := strings.Split(text, "\n")
	fenced := false
	for i, raw := range lines {
		line := strings.TrimSpace(raw)
		if codeFence.MatchString(line) {
			fenced = !fenced
			continue
		}
		if fenced {
			if line != "" && !excerptsContain(claims, line) {
				return false
			}
			continue
		}
		heading := headingLine.MatchString(line) || i+1 < len(lines) && tableSeparator.MatchString(strings.TrimSpace(lines[i+1]))
		line = markdownPrefix.ReplaceAllString(line, "")
		segments := []string{line}
		if strings.HasPrefix(line, "|") {
			segments = strings.Split(strings.Trim(line, "|"), "|")
		}
		for _, segment := range segments {
			for _, unit := range assertionSentence.Split(segment, -1) {
				unit = strings.TrimSpace(unit)
				substantive := false
				for _, r := range unit {
					if unicode.IsLetter(r) || unicode.IsNumber(r) {
						substantive = true
						break
					}
				}
				if !substantive || !NeedsEvidence(unit, heading) {
					continue
				}
				if !claimCovers(unit, claims) {
					return false
				}
			}
		}
	}
	return true
}

func claimCovers(unit string, claims []Claim) bool {
	plain := strings.ReplaceAll(unit, "`", "")
	codes := codeSpan.FindAllStringSubmatch(unit, -1)
	for _, c := range claims {
		if !CompleteAssertion(plain, strings.ReplaceAll(c.Text, "`", "")) {
			continue
		}
		excerpt := compact(c.Excerpt)
		quoted := true
		for _, code := range codes {
			quoted = quoted && strings.Contains(excerpt, compact(code[1]))
		}
		if quoted {
			return true
		}
	}
	return false
}

func excerptsContain(claims []Claim, line string) bool {
	line = compact(line)
	for _, c := range claims {
		if strings.Contains(compact(c.Excerpt), line) {
			return true
		}
	}
	return false
}
