package seokeywords

import (
	"strings"
	"unicode"

	"golang.org/x/text/unicode/norm"
)

func Normalize(s string) string {
	s = strings.ToLower(norm.NFKC.String(s))
	s = strings.Map(func(r rune) rune {
		if r == '_' || unicode.Is(unicode.Dash, r) || unicode.IsSpace(r) {
			return ' '
		}
		return r
	}, s)
	return strings.Join(strings.Fields(s), " ")
}

// Each supported language contributes an explicit vocabulary; fuzzy entity matches remain candidates.
var intentTerms = map[string][]string{
	"uninstall":    {"uninstall", "remove", "卸载", "アンインストール", "desinstalar", "удалить", "deinstallieren", "désinstaller", "disinstallare", "解除安裝", "移除"},
	"alternative":  {"alternative", "alternatives", "替代", "代替", "代わり", "alternativa", "альтернатива", "альтернативы"},
	"price":        {"price", "pricing", "free", "价格", "免费", "料金", "無料", "precio", "gratis", "preço", "цена", "бесплатно", "preis", "kostenlos", "prix", "gratuit", "prezzo", "prezzi", "價格", "免費"},
	"requirements": {"requirements", "compatibility", "系统要求", "動作環境", "要件", "requisitos", "требования", "anforderungen", "configuration requise", "requisiti", "系統需求"},
	"version":      {"version", "versions", "版本", "バージョン", "versión", "versão", "версия", "versione"},
	"compare":      {"compare", " vs ", "比较", "比較", "comparar", "сравнение", "vergleich", "comparer", "confronto"},
	"brew":         {"homebrew", "brew", "cask"},
	"install":      {"install", "安装", "インストール", "instalar", "установить", "installieren", "installer", "installare", "安裝"},
	"download":     {"download", "下载", "ダウンロード", "descargar", "baixar", "скачать", "herunterladen", "télécharger", "scaricare", "下載"},
}
var piracyTerms = []string{"crack", "keygen", "warez", "pirated", "破解", "盗版", "盜版", "クラック", "海賊版", "взлом", "пиратский", "raubkopie", "piraté", "pirata"}

func containsTerm(text, term string) bool {
	text, term = Normalize(text), Normalize(term)
	for offset := 0; offset <= len(text); {
		i := strings.Index(text[offset:], term)
		if i < 0 {
			return false
		}
		i += offset
		// CJK words may adjoin an entity name; Latin substrings such as "support" never confirm "port".
		latin := true
		for _, r := range term {
			if r > 127 {
				latin = false
			}
		}
		if !latin || (i == 0 || !wordByte(text[i-1])) && (i+len(term) == len(text) || !wordByte(text[i+len(term)])) {
			return true
		}
		offset = i + len(term)
		if term == "" {
			return false
		}
	}
	return false
}
func wordByte(b byte) bool { return b >= 'a' && b <= 'z' || b >= '0' && b <= '9' }
func Intent(keyword string) (string, bool) {
	for _, term := range piracyTerms {
		if containsTerm(keyword, term) {
			return "", true
		}
	}
	for _, intent := range []string{"uninstall", "alternative", "requirements", "compare", "price", "version", "brew", "install", "download"} {
		for _, term := range intentTerms[intent] {
			if containsTerm(keyword, term) {
				return intent, false
			}
		}
	}
	return "topic", false
}
