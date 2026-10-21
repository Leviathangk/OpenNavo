package store

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"regexp"
	"strings"

	"github.com/opennavo/opennavo/server/internal/content"
	"github.com/opennavo/opennavo/server/internal/domain"
	"github.com/opennavo/opennavo/server/internal/llm"
	"gorm.io/gorm"
)

type translationModelKey struct{}

func WithTranslationModel(ctx context.Context, model string) context.Context {
	return context.WithValue(ctx, translationModelKey{}, model)
}

// WriteTranslationSnapshot applies a complete translation only to the source
// version it was prepared for. Management callers also enforce task fencing.
func (s *Store) WriteTranslationSnapshot(ctx context.Context, source ContentSource, locale string, translated map[string]string, model string) (bool, error) {
	applied := false
	err := s.WithTx(ctx, func(tx *gorm.DB) error {
		if err := LockCatalogWrites(ctx, tx); err != nil {
			return err
		}
		st := &Store{DB: tx, Redis: s.Redis}
		if err := tx.Exec("SELECT pg_advisory_xact_lock(hashtextextended(?,0))", "content:"+source.Ref.Entity+":"+source.Ref.Key()).Error; err != nil {
			return err
		}
		current, err := st.ReadContentSource(ctx, source.Ref)
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil
		}
		if err != nil {
			return err
		}
		if current.Hash != source.Hash || current.SourceLocale != source.SourceLocale {
			return nil
		}
		where, args := textWhere(source.Ref)
		var manual bool
		if err := tx.Raw("SELECT EXISTS(SELECT 1 FROM "+content.Registry[source.Ref.Entity].Table+" WHERE "+where+" AND locale=? AND status='manual')", append(args, locale)...).Scan(&manual).Error; err != nil {
			return err
		}
		if manual {
			return nil
		}
		fields := content.Replace(source.Fields, translated)
		if err := st.WriteContentText(WithTranslationModel(ctx, model), source.Ref, locale, fields, false); err != nil {
			return err
		}
		applied = true
		return nil
	})
	return applied, err
}

var translationModelName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:/-]{0,159}$`)

// Validate the full resulting snapshot before stamping its source hash. A partial
// correction may reuse current text, but never silently bless stale fields.
func (s *Store) validateTranslationWrite(ctx context.Context, source ContentSource, locale string, fields map[string]any) (string, error) {
	model := ""
	if op := domain.CurrentOperation(ctx); op != nil && op.AgentClientID > 0 {
		name, _ := ctx.Value(translationModelKey{}).(string)
		if !translationModelName.MatchString(name) {
			return "", domain.Validation()
		}
		model = "hermes:" + strings.TrimPrefix(name, "hermes:")
	}
	spec := content.Registry[source.Ref.Entity]
	where, args := textWhere(source.Ref)
	var raw json.RawMessage
	if err := s.contentJSON(ctx, &raw, "SELECT to_jsonb(i) FROM "+spec.Table+" i WHERE "+where+" AND locale=?", append(args, locale)...); err != nil {
		return "", err
	}
	current := map[string]any{}
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &current); err != nil {
			return "", err
		}
	}
	merged := map[string]any{}
	if current["source_hash"] == source.Hash {
		for key := range source.Fields {
			merged[key] = current[key]
		}
	}
	for key, value := range fields {
		merged[textColumn(key)] = value
	}
	glossary, protected, err := s.ContentGlossary(ctx)
	if err != nil {
		return "", err
	}
	limits := map[string]int{}
	for key := range content.Flatten(source.Fields) {
		limits[key] = spec.Fields[strings.Split(key, ".")[0]]
	}
	input := llm.ContentInput{SourceLocale: source.SourceLocale, Targets: []string{locale}, Fields: content.Flatten(source.Fields), Limits: limits, Protected: append(source.Protected, protected...), Glossary: glossary, Strict: true}
	if err := llm.ValidateContent(llm.ContentOutput{locale: content.Flatten(merged)}, input); err != nil {
		// An agent gets the rule (for example changed_structure) so it can correct the
		// translation. The failure stays attached as the cause: batch writes report its
		// violations (field and the spans that differ) next to the rule. People keep
		// the localized message.
		var failure *llm.Failure
		if model != "" && errors.As(err, &failure) {
			return "", &domain.AppError{Code: domain.CodeValidation, HTTPStatus: http.StatusBadRequest, Msg: failure.Reason, Cause: failure}
		}
		return "", domain.Validation()
	}
	return model, nil
}
