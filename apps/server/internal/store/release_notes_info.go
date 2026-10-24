package store

import (
	"regexp"
	"strings"
	"unicode/utf8"
)

// An editorial note has no information when it only reports that a version's changes are not known
// ("Official release notes are unavailable", "Changes - Not provided: … unverified", "未提供此版本的变更说明",
// "Die Versionshinweise sind nicht verfügbar"). On 2026-10-24 about one editorial note in ten read like this.
// Such a note needs both: a statement of that kind, and no statement of an actual change. A real note that
// mentions unavailability ("修复离线时发行说明不可用导致的崩溃", "Fixes a crash when release notes are unavailable
// offline") states a change and keeps counting. Evidence asides ("The catalog records …") match neither.
// Phrases cover the source languages editorial notes may be written in: the ten site languages. Gaps may
// cross a dot inside a word or number ("2.3.1"), not a sentence end.
var noInformationNotes = regexp.MustCompile(`(?im)` + strings.Join([]string{
	// English
	`^\s*(?:[-*•]\s*)?not provided\b`,
	`(?:release notes|release details|version details|change ?log|change details|changes)(?:[^.\n]|\.\S){0,60}\b(?:unavailable|not (?:available|provided|published|listed|described|verified|found|documented|itemized|itemised|specified|disclosed|included)|unverified)\b`,
	`\bno (?:official |specific |detailed )?(?:release notes|change ?log|change details|changes? (?:can|could) be (?:reported|confirmed))`,
	`\bno (?:[\w-]+ ){0,6}(?:changes?|fixes|features|updates)(?: [\w-]+){0,6} (?:are|were|can be|could be) (?:inferred|reported|confirmed|summari[sz]ed|described|documented|listed)`,
	`\b(?:changes?|details) (?:cannot|can't|could not) be (?:described|summari[sz]ed|reported|confirmed)`,
	`\b(?:points|directs) (?:readers )?to the (?:official|corresponding|upstream)\b`,
	// Chinese, Simplified and Traditional
	`(?:变更|變更|更改|更新|发行|發行|发布|發布|發佈)(?:说明|說明|日志|日誌|记录|記錄|描述|内容|內容|详情|詳情|清单|清單)[^。\n]{0,30}(?:未提供|未公布|未列出)`,
	`(?:未提供|未公布|未列出|没有|沒有|无|無)[^。\n]{0,20}(?:变更|變更|更改|发行|發行|发布|發布|發佈)(?:说明|說明|描述|日志|日誌|详情|詳情|清单|清單|内容|內容)`,
	`未提供[^。\n]{0,20}(?:变更|變更)|未能核[实實]|无法核[实實]|無法核[实實]|暂无官方|暫無官方|官方暂无|官方暫無`,
	// Japanese
	`(?:変更|更新|リリース)(?:内容|情報|ノート|履歴|の詳細)[^。\n]{0,30}(?:提供されていません|公開されていません|記載されていません|確認できません|ありません|不明です)`,
	// German
	`(?:versionshinweise|änderungen|release notes|changelog)(?:[^.\n]|\.\S){0,60}(?:nicht (?:verfügbar|angegeben|veröffentlicht|bekannt|bestätigt|verifiziert)|unbekannt)`,
	`(?:^|\s)keine (?:offiziellen |genauen |detaillierten )?(?:versionshinweise|änderungen|angaben)`,
	// French
	`(?:notes de version|modifications|changements)(?:[^.\n]|\.\S){0,60}(?:non (?:disponibles?|fournie?s?|publiée?s?|vérifiée?s?|précisée?s?)|indisponibles?|ne (?:sont|est) pas (?:disponibles?|fournie?s?|publiée?s?|précisée?s?)|n'ont pas été (?:publiée?s?|fournie?s?|précisée?s?))`,
	`(?:^|\s)aucune? (?:note|modification|information|précision|détail)s?(?:\s|$|[.,;:])`,
	// Italian
	`(?:note di rilascio|modifiche|novità)(?:[^.\n]|\.\S){0,60}non (?:sono |è )?(?:disponibil[ei]|fornit[aoie]|pubblicat[aoie]|verificat[aoie]|specificat[aoie])`,
	`(?:^|\s)nessuna? (?:nota|modifica|informazione|dettaglio)`,
	// Spanish
	`(?:notas de (?:la )?versión|cambios|novedades)(?:[^.\n]|\.\S){0,60}(?:no (?:está|están) disponibles?|no (?:se )?(?:proporcionan?|publicaron|han publicado|especifican?|verificaron|han verificado)|sin verificar)`,
	`(?:^|\s)(?:no hay|sin) (?:notas|cambios|detalles|información)`,
	// Portuguese
	`(?:notas de (?:versão|lançamento)|alterações|mudanças|novidades)(?:[^.\n]|\.\S){0,60}(?:não (?:está|estão) disponíve(?:l|is)|não (?:foram |foi )?(?:fornecid|publicad|divulgad|verificad|especificad)[oa]s?|indisponíve(?:l|is))`,
	`(?:^|\s)(?:não há|sem) (?:notas|alterações|mudanças|detalhes|informações)`,
	// Russian
	`(?:примечани[яй] к выпуску|список изменений|изменени[яй]|описани[ея] изменений)(?:[^.\n]|\.\S){0,60}(?:недоступн|не (?:предоставлен|опубликован|указан|подтвержд|проверен))`,
	`(?:^|\s)нет (?:официальных |подробных )?(?:примечаний|сведений|описания|изменений|информации)`,
}, "|"))

// changeStart reads a sentence or line that opens with a change ("Fixes …", "修复…", "Behebt …", "Corrige …").
// "New" in any language is left out: "New features, fixes and performance changes are not itemized" is no change.
var changeStart = regexp.MustCompile(`(?i)^(?:` + strings.Join([]string{
	`(?:adds?|added|fix(?:es|ed)?|improves?|improved|resolves?|resolved|removes?|removed|introduces?|introduced|supports?|enables?|allows?|now)(?:[^\p{L}]|$)`,
	`(?:新增|修复|修復|修正|改进|改進|优化|優化|支持|支援|增加|解决|解決|移除|删除|刪除|加入|引入)`,
	`(?:追加|修正|改善|対応)`,
	`(?:behebt|behoben|fügt|hinzugefügt|verbessert|unterstützt|entfernt|ermöglicht|korrigiert)(?:[^\p{L}]|$)`,
	`(?:ajoute|ajout|corrige|correction|améliore|amélioration|prend en charge|prise en charge|supprime|résout|permet)(?:[^\p{L}]|$)`,
	`(?:aggiunge|aggiunt[aoie]|corregge|corrett[aoie]|correzione|migliora|miglioramento|risolve|risolt[aoie]|rimuove|supporta|consente)(?:[^\p{L}]|$)`,
	`(?:añade|agrega|corrige|mejora|soluciona|resuelve|elimina|admite|permite|ahora)(?:[^\p{L}]|$)`,
	`(?:adiciona|corrige|melhora|resolve|remove|suporta|permite|agora)(?:[^\p{L}]|$)`,
	`(?:добавлен[аоы]?|добавляет|исправлен[аоы]?|исправляет|улучшен[аоы]?|улучшает|поддержка|удален[аоы]?|теперь)(?:[^\p{L}]|$)`,
}, "|") + `)`)

// changeAnywhere reads changes stated with the verb last (Japanese, German passive, Chinese 已…) and
// upgrades to a stated version ("将捆绑的 CLIProxyAPI 更新至 8.0.28", "Updates the bundled engine to 8.0.28").
var changeAnywhere = regexp.MustCompile(`(?i)(?:追加|修正|改善|対応|強化|削除|変更)(?:しました|した|しています|されました|されています|します)` +
	`|(?:更新|升级|升級)(?:至|到)\s*v?\d|\bupdate[sd]? (?:[^.\n]{0,40} )?to (?:version |v)?\d` +
	`|(?:已|已经|已經)(?:修复|修復|解决|解決|改进|改進|优化|優化|支持|支援)` +
	`|\b(?:wurde|wurden)\b(?:[^.\n]|\.\S){0,80}(?:behoben|hinzugefügt|verbessert|entfernt|korrigiert)`)

// listItem finds the items of a Markdown list.
var listItem = regexp.MustCompile(`(?m)^\s*(?:[-*•]|\d+[.)])\s+(.+)$`)

// asideWords marks an item as a remark about sources, downloads or missing details rather than a change
// ("The official release history does not list changes", "Separate macOS archives are available",
// "url: App-2.8.2-arm64.zip", "未列出更细的修复项目").
var asideWords = regexp.MustCompile(`(?i)(?:^|[^\p{L}])(?:not|no|none|never|unverified|unavailable|official|source|evidence|changelog|catalog|homebrew|archives?|installers?|downloads?|updater|builds?|appcast|checksum|sha256|url|404|nicht|kein\p{L}*|offiziell\p{L}*|quelle|pas|aucun\p{L}*|officiel\p{L}*|non|nessun\p{L}*|ufficial\p{L}*|fonte|sin|oficial\p{L}*|fuente|não|sem|не|нет|официальн\p{L}*|источник\p{L}*)(?:[^\p{L}]|$)` +
	`|\.(?:zip|dmg|pkg|tar|gz|exe|msi|appimage|deb|rpm|yml|xml|json)\b` +
	`|未|没有|沒有|无|無|官方|来源|來源|核实|核實|下载|下載|安装包|安裝包|ません|ない|公式|不明`)

// cjkText finds Chinese or Japanese script, which has no spaces between words.
var cjkText = regexp.MustCompile(`[\p{Han}\p{Hiragana}\p{Katakana}]`)

// listsChanges reports whether at least two list or section items describe something rather than remark on
// sources ("- Drag notes into a note to insert note-links." is a change even without a change verb). A
// description takes a sentence outside links: at least four words, or eight characters in Chinese or Japanese.
func listsChanges(items []string) bool {
	count := 0
	for _, item := range items {
		// A link names a source whatever its label ("[View the release history](…)"): count only the words
		// outside links.
		item = strings.TrimSpace(notesURLs.ReplaceAllString(notesLinks.ReplaceAllString(item, ""), ""))
		long := len(strings.Fields(item)) >= 4
		if cjkText.MatchString(item) {
			long = utf8.RuneCountInString(item) >= 8
		}
		if long && !asideWords.MatchString(item) && !noInformationNotes.MatchString(item) {
			count++
		}
	}
	return count >= 2
}

// changeInProse finds a change stated mid-sentence: a subject such as "This release", "Vienna 3.10.8" or
// "Cette version", then a change verb ("This release adds vertical tabs and fixes a crash"), or a Chinese or
// Russian completed form ("此版本新增了标签页", "добавлены вкладки"). Negation before the change
// or immediately after its English verb cancels it; noun followers do not state changes either.
var changeInProse = regexp.MustCompile(`(?i)` + strings.Join([]string{
	`(?:\b(?:this|the|it|that)(?:\s+[\w.-]+){0,2}|\d+(?:\.\d+)+[\w.,+-]*)\s+(?:adds|added|fixes|fixed|improves|improved|introduces|introduced|removes|removed|resolves|resolved|brings|brought|enables|enabled|supports|supported|allows|allowed)\s+(?P<next>["'“‘]?[\p{L}\d][\p{L}\d'-]*)`,
	`(?:^|[^\p{L}])(?:version|update|release|ausgabe|\d+(?:\.\d+)+\S*)\s+(?:behebt|verbessert|fügt|ergänzt|bringt|entfernt|unterstützt|führt)(?:[^\p{L}]|$)`,
	`(?:^|[^\p{L}])(?:version|mise à jour|\d+(?:\.\d+)+\S*)\s+(?:ajoute|corrige|améliore|apporte|supprime|introduit|prend en charge)(?:[^\p{L}]|$)`,
	`(?:^|[^\p{L}])(?:versione|aggiornamento|\d+(?:\.\d+)+\S*)\s+(?:aggiunge|corregge|migliora|introduce|rimuove|risolve|supporta)(?:[^\p{L}]|$)`,
	`(?:^|[^\p{L}])(?:versión|actualización|\d+(?:\.\d+)+\S*)\s+(?:añade|agrega|corrige|mejora|introduce|elimina|soluciona|admite)(?:[^\p{L}]|$)`,
	`(?:^|[^\p{L}])(?:versão|atualização|\d+(?:\.\d+)+\S*)\s+(?:adiciona|corrige|melhora|introduz|remove|resolve|suporta)(?:[^\p{L}]|$)`,
	`(?:^|[^\p{L}])(?:добавлен[аоы]?|исправлен[аоы]?|улучшен[аоы]?|удален[аоы]?)(?:[^\p{L}]|$)`,
	`(?:新增|修复|修復|改进|改進|优化|優化|增加|加入|引入|移除)了|(?:版本|更新|此版|该版|該版)(?:新增|修复|修復|改进|改進|优化|優化|增加|加入|引入|移除)`,
}, "|"))

// nounFollower marks English "fixes"/"supports" used as nouns: "the listed bug fixes are not specified".
var nounFollower = regexp.MustCompile(`(?i)^(?:are|were|is|was|and|or|nor|for|in|of|to|from|with|by|have|has|that|which)$`)

// negation marks a word that cancels a change named right after it.
var negation = regexp.MustCompile(`(?i)^(?:no|not|never|without|nor|nicht|kein\p{L}*|ohne|pas|aucun\p{L}*|sans|ne|non|nessun\p{L}*|senza|sin|ningún|ninguna|não|sem|nenhum\p{L}*|не|нет|без)$|n't$`)

// negatedBefore reports whether one of the two words before a change, within its clause, negates it.
func negatedBefore(before string) bool {
	if i := strings.LastIndexAny(before, ",;:，；：、"); i >= 0 {
		before = before[i+1:]
	}
	if runes := []rune(strings.TrimSpace(before)); len(runes) > 0 && cjkText.MatchString(string(runes[len(runes)-1])) {
		tail := string(runes[max(0, len(runes)-2):])
		return strings.ContainsAny(tail, "未没沒无無不非")
	}
	words := strings.Fields(before)
	for _, word := range words[max(0, len(words)-2):] {
		if negation.MatchString(strings.Trim(word, "\"'()[]")) {
			return true
		}
	}
	return false
}

// resolvedFailure recognises a failure that has stopped, rather than missing changes
// or unavailable source material ("Now no longer available: release notes").
var resolvedFailure = regexp.MustCompile(`(?i)^now\s+no\s+longer\s+(?:crashes?|hangs?|freezes?|fails?)\b`)

// negatedAfter checks an unquoted immediate object. A quoted error name such as
// "No release notes available" is the object of a fix, not a negated change.
func negatedAfter(after string) bool {
	words := strings.Fields(after)
	return len(words) > 0 && negation.MatchString(words[0])
}

// Resolve reference links before evaluating their labels as statements or removing
// them from descriptive list items. Definitions are metadata, not statements.
var notesReferenceDefinition = regexp.MustCompile(`(?m)^ {0,3}\[([^]\n]+)\]:[^\n]*(?:\n|$)`)
var notesReferenceLink = regexp.MustCompile(`!?\[([^]\n]+)\](?:\[([^]\n]*)\]|(\([^\n)]*\)))?`)

func normalizeNotesReferences(value, document string) string {
	key := func(label string) string { return strings.ToLower(strings.Join(strings.Fields(label), " ")) }
	definitions := make(map[string]bool)
	for _, match := range notesReferenceDefinition.FindAllStringSubmatch(document, -1) {
		definitions[key(match[1])] = true
	}
	value = notesReferenceDefinition.ReplaceAllString(value, "")
	return notesReferenceLink.ReplaceAllStringFunc(value, func(link string) string {
		match := notesReferenceLink.FindStringSubmatch(link)
		if match[3] != "" {
			return link
		}
		label := match[2]
		if label == "" {
			label = match[1]
		}
		if !definitions[key(label)] {
			return link
		}
		return "[" + match[1] + "]()"
	})
}

// statementBreaks ends a sentence at a line break or punctuation; a dot ends one only before a space or the
// end, so "Vienna 3.10.8 fixes a crash" stays one sentence.
var statementBreaks = regexp.MustCompile(`[\n!?;。！？；]+|\.(?:\s+|$)`)

// statementLead strips list markers, headings, numbering and bold markers before a statement.
var statementLead = regexp.MustCompile(`^[\s\-*•#>|]*(?:\d+[.)]\s*)?(?:\*\*|__)?\s*`)

// statesChange reports whether any sentence, line or section item states an actual change. A bare
// heading ("Fixes", "New") is too short to count.
func statesChange(text string) bool {
	if changeAnywhere.MatchString(text) {
		return true
	}
	for _, statement := range statementBreaks.Split(text, -1) {
		statement = strings.TrimSpace(statementLead.ReplaceAllString(statement, ""))
		if start := changeStart.FindStringIndex(statement); utf8.RuneCountInString(statement) >= 6 && start != nil && (!negatedAfter(statement[start[1]:]) || resolvedFailure.MatchString(statement)) {
			return true
		}
		next := changeInProse.SubexpIndex("next")
		for _, match := range changeInProse.FindAllStringSubmatchIndex(statement, -1) {
			if match[2*next] >= 0 && (nounFollower.MatchString(statement[match[2*next]:match[2*next+1]]) || negation.MatchString(statement[match[2*next]:match[2*next+1]])) {
				continue
			}
			if !negatedBefore(statement[:match[0]]) {
				return true
			}
		}
	}
	return false
}

// noInformation reads an editorial note's source-language fields. Translations inherit the verdict: a
// translated "details are unavailable" says no more than its source.
func noInformation(fields map[string]any) bool {
	var text, statements strings.Builder
	var items []string
	for _, key := range []string{"summary", "body_markdown"} {
		if value, ok := fields[key].(string); ok {
			text.WriteString(value + "\n")
			statements.WriteString(value + "\n")
		}
	}
	if body, ok := fields["body_markdown"].(string); ok {
		for _, match := range listItem.FindAllStringSubmatch(body, -1) {
			items = append(items, match[1])
		}
	}
	// Section items are read on their own lines; the area is a heading ("Changes" over "Not provided." is caught
	// by the item, while "Changes" over "… when HLS video is unavailable" is no statement about the notes).
	if sections, ok := fields["sections"].([]any); ok {
		for _, section := range sections {
			entry, _ := section.(map[string]any)
			list, _ := entry["items"].([]any)
			for _, item := range list {
				if value, ok := item.(string); ok {
					text.WriteString(value + "\n")
					statements.WriteString(value + "\n")
					items = append(items, value)
				}
			}
		}
	}
	// Link labels still read as statements ("[Fixes a crash on launch](…)").
	document := text.String()
	for i := range items {
		items[i] = normalizeNotesReferences(items[i], document)
	}
	return noInformationNotes.MatchString(document) && !statesChange(visibleNotes(normalizeNotesReferences(statements.String(), document))) && !listsChanges(items)
}
