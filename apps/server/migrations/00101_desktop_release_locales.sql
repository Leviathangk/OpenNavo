-- The Mac client ships in all ten site languages, so its release notes do too (the desktop locale surface).
-- Widening only admits more values, so existing rows validate immediately. Down re-adds the six-language checks and
-- therefore refuses to run while notes in the other four languages exist, rather than deleting them.
-- +goose Up
ALTER TABLE desktop_releases DROP CONSTRAINT desktop_releases_source_locale_check;
ALTER TABLE desktop_releases ADD CONSTRAINT desktop_releases_source_locale_check CHECK (source_locale IN ('en-US','zh-CN','ja-JP','es-ES','pt-BR','ru-RU','de-DE','fr-FR','it-IT','zh-Hant'));
ALTER TABLE desktop_release_i18n DROP CONSTRAINT desktop_release_i18n_locale_check;
ALTER TABLE desktop_release_i18n ADD CONSTRAINT desktop_release_i18n_locale_check CHECK (locale IN ('en-US','zh-CN','ja-JP','es-ES','pt-BR','ru-RU','de-DE','fr-FR','it-IT','zh-Hant'));
ALTER TABLE desktop_release_i18n DROP CONSTRAINT desktop_release_i18n_source_locale_check;
ALTER TABLE desktop_release_i18n ADD CONSTRAINT desktop_release_i18n_source_locale_check CHECK (source_locale IN ('en-US','zh-CN','ja-JP','es-ES','pt-BR','ru-RU','de-DE','fr-FR','it-IT','zh-Hant'));

-- +goose Down
ALTER TABLE desktop_releases DROP CONSTRAINT desktop_releases_source_locale_check;
ALTER TABLE desktop_releases ADD CONSTRAINT desktop_releases_source_locale_check CHECK (source_locale IN ('en-US','zh-CN','ja-JP','es-ES','pt-BR','ru-RU'));
ALTER TABLE desktop_release_i18n DROP CONSTRAINT desktop_release_i18n_locale_check;
ALTER TABLE desktop_release_i18n ADD CONSTRAINT desktop_release_i18n_locale_check CHECK (locale IN ('en-US','zh-CN','ja-JP','es-ES','pt-BR','ru-RU'));
ALTER TABLE desktop_release_i18n DROP CONSTRAINT desktop_release_i18n_source_locale_check;
ALTER TABLE desktop_release_i18n ADD CONSTRAINT desktop_release_i18n_source_locale_check CHECK (source_locale IN ('en-US','zh-CN','ja-JP','es-ES','pt-BR','ru-RU'));
