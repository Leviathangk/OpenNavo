package store

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"time"
	"unicode/utf8"

	"github.com/opennavo/opennavo/server/internal/content"
	"github.com/opennavo/opennavo/server/internal/domain"
	"github.com/opennavo/opennavo/server/internal/traditional"
	"gorm.io/gorm"
)

// These proof-backed texts have no translation status/source-hash columns.
// Derive only their prose; paths, item types and proof provenance remain exact.
var traditionalAuxiliary = map[string]struct{ table, key, field, columns string }{
	"background_item":  {"background_item_content", "label", "description", "label,locale,description,item_type,common_paths,evidence"},
	"collection_setup": {"collection_setup_criteria", "collection_id", "criteria", "collection_id,locale,criteria,evidence"},
}

func traditionalEntities() []string {
	return append(content.Names(), "background_item", "collection_setup")
}
func traditionalSpec(entity string) content.Spec {
	if spec, ok := traditionalAuxiliary[entity]; ok {
		return content.Spec{Table: spec.table, Key: spec.key}
	}
	return content.Registry[entity]
}

// deriveAuxiliaryTraditional derives a saved text inside the caller's transaction.
func (s *Store) deriveAuxiliaryTraditional(ctx context.Context, entity, key string) error {
	_, err := s.deriveAuxiliary(ctx, entity, key, false, false)
	return err
}

// deriveAuxiliary derives one proof-backed text; with hold it stops before writing. The backfill
// batches the cache clearing its writes need (see queueBackfillInvalidation).
func (s *Store) deriveAuxiliary(ctx context.Context, entity, key string, hold, backfill bool) (traditionalOutcome, error) {
	spec := traditionalAuxiliary[entity]
	if err := s.DB.WithContext(ctx).Exec("SELECT pg_advisory_xact_lock(hashtextextended(?,0))", "content:"+entity+":"+key).Error; err != nil {
		return traditionalUnchanged, err
	}
	read := func(locale string) (json.RawMessage, error) {
		var raw json.RawMessage
		err := s.contentJSON(ctx, &raw, "SELECT to_jsonb(i) FROM "+spec.table+" i WHERE "+spec.key+"=? AND locale=?", key, locale)
		return raw, err
	}
	cn, err := read(traditional.Source)
	if err != nil {
		return traditionalUnchanged, err
	}
	before, err := read(traditional.Locale)
	if err != nil {
		return traditionalUnchanged, err
	}
	after := json.RawMessage("null")
	if len(cn) > 0 {
		var row map[string]any
		decoder := json.NewDecoder(bytes.NewReader(cn))
		decoder.UseNumber()
		if err = decoder.Decode(&row); err != nil {
			return traditionalUnchanged, err
		}
		_, protected, err := s.ContentGlossary(ctx)
		if err != nil {
			return traditionalUnchanged, err
		}
		converted, err := traditional.Text(row[spec.field].(string), protected)
		if err != nil {
			return traditionalUnchanged, err
		}
		if utf8.RuneCountInString(converted) > 10000 {
			return traditionalUnchanged, fmt.Errorf("%w: %w", errTraditionalInvalid, domain.Rejected(spec.field+"_too_long"))
		}
		row[spec.field], row["locale"] = converted, traditional.Locale
		after, err = json.Marshal(row)
		if err != nil {
			return traditionalUnchanged, err
		}
	}
	if (len(before) == 0 && string(after) == "null") || (len(before) > 0 && content.JSONHash(before) == content.JSONHash(after)) {
		// The text comes out the same: a receipt from an older converter that still describes the row
		// is renewed, without touching the row (its writes bump the background item's revision).
		var prior struct {
			Converter  string
			RestoredAt *time.Time
			AfterData  json.RawMessage
		}
		if err = s.DB.WithContext(ctx).Table("derived_traditional").Where("entity=? AND object_key=?", entity, key).Scan(&prior).Error; err != nil {
			return traditionalUnchanged, err
		}
		if len(before) == 0 || prior.Converter == "" || prior.Converter == traditional.Version || prior.RestoredAt != nil || content.JSONHash(prior.AfterData) != content.JSONHash(before) {
			return traditionalUnchanged, nil
		}
		err = s.DB.WithContext(ctx).Exec("UPDATE derived_traditional SET converter=?,updated_at=clock_timestamp() WHERE entity=? AND object_key=?", traditional.Version, entity, key).Error
		return traditionalStamped, err
	}
	if hold {
		return traditionalHeld, nil
	}
	if string(after) == "null" {
		err = s.DB.WithContext(ctx).Exec("DELETE FROM "+spec.table+" WHERE "+spec.key+"=? AND locale='zh-Hant'", key).Error
	} else {
		// All identifiers are fixed above, and the source snapshot retains metadata.
		err = s.DB.WithContext(ctx).Exec("INSERT INTO "+spec.table+" ("+spec.columns+") SELECT "+spec.columns+" FROM jsonb_populate_record(NULL::"+spec.table+",?::jsonb) ON CONFLICT ("+spec.key+",locale) DO UPDATE SET ("+spec.columns+")=(SELECT "+spec.columns+" FROM jsonb_populate_record(NULL::"+spec.table+",?::jsonb))", string(after), string(after)).Error
	}
	if err != nil {
		return traditionalUnchanged, err
	}
	var backup any
	if len(before) > 0 {
		backup = string(before)
	}
	if err = s.DB.WithContext(ctx).Exec(`INSERT INTO derived_traditional(entity,object_key,simplified_hash,converter,before_data,after_data) VALUES(?,?,?,?,?::jsonb,?::jsonb) ON CONFLICT(entity,object_key) DO UPDATE SET before_data=CASE WHEN derived_traditional.restored_at IS NOT NULL THEN EXCLUDED.before_data ELSE derived_traditional.before_data END,simplified_hash=EXCLUDED.simplified_hash,converter=EXCLUDED.converter,after_data=EXCLUDED.after_data,updated_at=clock_timestamp(),restored_at=NULL`, entity, key, content.JSONHash(cn), traditional.Version, backup, string(after)).Error; err != nil {
		return traditionalUnchanged, err
	}
	if backfill {
		return traditionalWritten, queueBackfillInvalidation(ctx, s.DB)
	}
	return traditionalWritten, QueueCacheInvalidation(ctx, s.DB, "traditional:"+entity+":"+key, "c:*")
}

// queueBackfillInvalidation clears every cache (c:*, as live edits of these texts do) a minute after
// the first backfill write that needs it, so all writes until then share one clearing instead of
// each clearing every cache. Updating the pending row locks it against delivery until this write
// commits, as in addCacheInvalidation, and keeps its time.
func queueBackfillInvalidation(ctx context.Context, tx *gorm.DB) error {
	return tx.WithContext(ctx).Exec(`INSERT INTO job_outbox(unique_key,job_type,payload,available_at) VALUES('cache:invalidate:traditional:backfill','cache:invalidate','["c:*"]'::jsonb,now()+interval '1 minute') ON CONFLICT(unique_key) DO UPDATE SET payload=EXCLUDED.payload`).Error
}
