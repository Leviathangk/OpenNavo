package i18n

import (
	"embed"
	"encoding/json"
)

//go:embed messages/*.json
var messageFiles embed.FS
var messages = loadMessages()

func loadMessages() map[string]map[string]string {
	result := make(map[string]map[string]string, len(Locales))
	for _, locale := range Locales {
		data, err := messageFiles.ReadFile("messages/" + string(locale) + ".json")
		if err != nil {
			panic(err)
		}
		var values map[string]string
		if err := json.Unmarshal(data, &values); err != nil {
			panic(err)
		}
		result[string(locale)] = values
	}
	return result
}

func Message(code, locale string) string {
	if !ValidApp(locale) {
		locale = string(DefaultLocale)
	}
	if value := messages[locale][code]; value != "" {
		return value
	}
	return messages[locale]["5000"]
}
