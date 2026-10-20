package management

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/opennavo/opennavo/server/internal/content"
	"github.com/opennavo/opennavo/server/internal/domain"
	"github.com/opennavo/opennavo/server/internal/jobs"
	"github.com/opennavo/opennavo/server/internal/searchtext"
	"github.com/opennavo/opennavo/server/internal/store"
)

func (s *Service) releaseRead(ctx context.Context, op string, in Input) (any, error) {
	if op == "GetAdminRelease" {
		return s.Store.AdminRelease(ctx, in.ID)
	}
	current, size, err := page(in)
	if err != nil {
		return nil, err
	}
	if op == "ListTranslationQueue" {
		return s.translationQueue(ctx, in, current, size)
	}
	where, args := []string{"TRUE"}, []any{}
	if has, ok := in.Params["haseditorial"].(bool); ok {
		where = append(where, "EXISTS(SELECT 1 FROM releases e WHERE e.package_id=r.package_id AND e.version=r.version AND e.source='editorial' AND NOT e.hidden)=?")
		args = append(args, has)
	}
	for key, col := range map[string]string{"source": "r.source", "translationstatus": "coalesce(i.status,CASE WHEN r.body_markdown IS NULL THEN 'skipped' ELSE 'pending' END)"} {
		if v := text(in.Params, key); v != "" {
			where = append(where, col+"=?")
			args = append(args, v)
		}
	}
	if id := number(in.Params, strings.ToLower("packageId"), 0); id > 0 {
		where = append(where, "r.package_id=?")
		args = append(args, id)
	}
	if v, ok := in.Params["hidden"].(bool); ok {
		where = append(where, "r.hidden=?")
		args = append(args, v)
	}
	if q := text(in.Params, "q"); q != "" {
		where = append(where, `(p.token ILIKE ? ESCAPE '\' OR r.version ILIKE ? ESCAPE '\')`)
		v := "%" + searchtext.EscapeLike(q) + "%"
		args = append(args, v, v)
	}
	return s.Store.AdminPage(ctx, store.AdminReleaseJSON, store.AdminReleaseFrom, strings.Join(where, " AND "), "r.published_at DESC NULLS LAST,r.id DESC", args, current, size)
}
func (s *Service) translationQueue(ctx context.Context, in Input, current, size int) (any, error) {
	status := text(in.Params, "status")
	args := []any{}
	typ := text(in.Params, "type")
	expr, from, where := "", "", "TRUE"
	if status != "" {
		where += " AND i.status=?"
		args = append(args, status)
	}
	if locale := text(in.Params, "locale"); locale != "" {
		where += " AND i.locale=?"
		args = append(args, locale)
	}
	switch typ {
	case "package":
		expr = `jsonb_build_object('type','package','id',p.id,'kind',p.kind,'token',p.token,'packageName',p.name,'rank30d',p.rank_30d,'status',i.status,'locale',i.locale,'sourceLocale',i.source_locale,'original',p.desc_en,'translated',i.summary,'confidence',(SELECT confidence FROM package_categories WHERE package_id=p.id AND is_primary),'updatedAt',i.updated_at)`
		from = "FROM package_i18n i JOIN packages p ON p.id=i.package_id"
	case "release":
		expr = `jsonb_build_object('type','release','id',r.id,'kind',p.kind,'token',p.token,'packageName',p.name,'version',r.version,'rank30d',p.rank_30d,'status',i.status,'locale',i.locale,'sourceLocale',i.source_locale,'original',left(r.body_markdown,400),'translated',i.summary,'updatedAt',i.updated_at)`
		from = "FROM release_i18n i JOIN releases r ON r.id=i.release_id JOIN packages p ON p.id=r.package_id"
		where += " AND NOT r.hidden"
	default:
		return nil, domain.Validation()
	}
	order := "p.popularity DESC,i.updated_at,p.id"
	if typ == "release" {
		order += ",r.id"
	}
	return s.Store.AdminPage(ctx, expr, from, where+" AND p.removed_at IS NULL", order, args, current, size)
}
func (s *Service) releaseWrite(ctx context.Context, op string, in Input) (any, error) {
	result, err := s.write(ctx, in, op, "release", func(st *store.Store) (any, error) {
		var row struct {
			PackageID int64
			BodyHash  *string
		}
		if err := st.DB.WithContext(ctx).Raw("SELECT package_id,body_hash FROM releases WHERE id=? FOR UPDATE", in.ID).Scan(&row).Error; err != nil {
			return nil, err
		}
		if row.PackageID == 0 {
			return nil, fail(domain.CodeNotFound)
		}
		switch op {
		case "UpdateRelease":
			fields := mapped(in.Body, map[string]string{"hidden": "hidden", "title": "title"})
			fields["updated_at"] = s.now()
			if err := st.DB.WithContext(ctx).Table("releases").Where("id=?", in.ID).Updates(fields).Error; err != nil {
				return nil, err
			}
		case "UpdateReleaseI18n":
			source, err := st.TextSourceLocale(ctx, "release", in.ID, in.Locale, in.Body)
			if err != nil {
				return nil, err
			}
			status := "manual"
			if source == in.Locale {
				status = "source"
			}
			data, err := json.Marshal(in.Body["sections"])
			if err != nil {
				return nil, err
			}
			if err := st.DB.WithContext(ctx).Exec(`INSERT INTO release_i18n(release_id,locale,summary,sections,body_markdown,status,source_hash,reviewed_by,reviewed_at) VALUES(?,?,?,?::jsonb,?,'manual',?,?,?) ON CONFLICT(release_id,locale) DO UPDATE SET summary=EXCLUDED.summary,sections=EXCLUDED.sections,body_markdown=EXCLUDED.body_markdown,status='manual',source_hash=EXCLUDED.source_hash,fail_reason=NULL,reviewed_by=EXCLUDED.reviewed_by,reviewed_at=EXCLUDED.reviewed_at,updated_at=now()`, in.ID, in.Locale, in.Body["summary"], string(data), in.Body["bodyMarkdown"], row.BodyHash, in.Actor.ID, s.now()).Error; err != nil {
				return nil, err
			}
			fields := map[string]any{"source_locale": source, "status": status}
			if title, present := in.Body["title"]; present {
				fields["title"] = title
			}
			if err := st.DB.WithContext(ctx).Table("release_i18n").Where("release_id=? AND locale=?", in.ID, in.Locale).Updates(fields).Error; err != nil {
				return nil, err
			}

			if err := st.StampContentHash(ctx, content.Ref{Entity: "release", ID: in.ID}, in.Locale); err != nil {
				return nil, err
			}
			if in.Locale == source {
				if err := st.ScheduleContentTranslation(ctx, content.Ref{Entity: "release", ID: in.ID}); err != nil {
					return nil, err
				}
			}
		}
		return nil, st.AppendContentChange(ctx, row.PackageID)
	})
	if err != nil {
		return nil, err
	}
	if boolean(in.Params, "dryrun") {
		return result, nil
	}

	return nil, nil
}
func (s *Service) approve(ctx context.Context, in Input) (any, error) {
	list, err := ids(in.Body["ids"])
	if err != nil {
		return nil, err
	}
	if len(list) == 0 {
		return nil, domain.Validation()
	}
	typ := text(in.Body, "type")
	return s.write(ctx, in, "ApproveTranslations", "translation", func(st *store.Store) (any, error) {
		for _, id := range list {
			var pid int64
			switch typ {
			case "package":
				r := st.DB.WithContext(ctx).Raw(`UPDATE package_i18n i SET status='manual',reviewed_by=?,reviewed_at=?,updated_at=? FROM packages p WHERE i.package_id=p.id AND i.package_id=? AND i.locale='zh-CN' AND i.status='machine' AND i.source_hash=encode(sha256(convert_to(coalesce(p.desc_en,''),'UTF8')),'hex') RETURNING p.id`, in.Actor.ID, s.now(), s.now(), id).Scan(&pid)
				if r.Error != nil {
					return nil, r.Error
				}
				if r.RowsAffected != 1 {
					return nil, fail(domain.CodeInvalidState)
				}
			case "release":
				r := st.DB.WithContext(ctx).Raw(`UPDATE release_i18n i SET status='manual',reviewed_by=?,reviewed_at=?,updated_at=? FROM releases r WHERE i.release_id=r.id AND i.release_id=? AND i.locale='zh-CN' AND i.status='machine' AND i.source_hash=r.body_hash RETURNING r.package_id`, in.Actor.ID, s.now(), s.now(), id).Scan(&pid)
				if r.Error != nil {
					return nil, r.Error
				}
				if r.RowsAffected != 1 {
					return nil, fail(domain.CodeInvalidState)
				}
			default:
				return nil, domain.Validation()
			}
			if err := st.AppendContentChange(ctx, pid); err != nil {
				return nil, err
			}
		}
		return nil, nil
	})
}
func (s *Service) dispatch(ctx context.Context) error {
	_, err := s.Store.DispatchOutbox(ctx, func(ctx context.Context, typ string, raw json.RawMessage) error {
		p, m, err := jobs.DecodeOutbox(raw)
		if err != nil {
			return err
		}
		_, err = s.Queue.Enqueue(ctx, typ, p, m)
		return jobs.OutboxDeliveryError(typ, err)
	})
	return err
}
func releaseSnapshot(ctx context.Context, st *store.Store, in Input) (any, error) {
	raw, err := st.AdminRelease(ctx, in.ID)
	if err != nil {
		return nil, err
	}
	var out any
	err = json.Unmarshal(raw, &out)
	return out, err
}
func translationSnapshot(ctx context.Context, st *store.Store, in Input) (any, error) {
	list, err := ids(in.Body["ids"])
	if err != nil {
		return nil, err
	}
	out := []any{}
	for _, id := range list {
		var raw json.RawMessage
		if text(in.Body, "type") == "release" {
			raw, err = st.AdminRelease(ctx, id)
		} else {
			raw, err = st.AdminPackage(ctx, id)
		}
		if err != nil {
			return nil, err
		}
		var v any
		if err := json.Unmarshal(raw, &v); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return map[string]any{"items": out}, nil
}
