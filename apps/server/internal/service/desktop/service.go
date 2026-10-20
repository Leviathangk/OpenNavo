package desktop

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"reflect"
	"regexp"
	"strings"
	"time"

	"github.com/opennavo/opennavo/server/gen/publicapi"
	"github.com/opennavo/opennavo/server/internal/cache"
	"github.com/opennavo/opennavo/server/internal/content"
	"github.com/opennavo/opennavo/server/internal/domain"
	"github.com/opennavo/opennavo/server/internal/store"
	"gorm.io/gorm"
)

type Objects interface {
	Put(context.Context, string, []byte, string) error
	Read(context.Context, string) ([]byte, error)
	Delete(context.Context, string) error
}
type Service struct {
	Store   *store.Store
	Objects Objects
	Now     func() time.Time
}
type Artifact struct {
	Target, URL string
	Signature   *string
	Bytes       int64
	SHA256      string
}
type Platform struct {
	Signature string `json:"signature"`
	URL       string `json:"url"`
}
type Manifest struct {
	Version   string              `json:"version"`
	Notes     string              `json:"notes"`
	PubDate   time.Time           `json:"pub_date"`
	Platforms map[string]Platform `json:"platforms"`
}

var hashPattern = regexp.MustCompile(`^[0-9a-fA-F]{64}$`)
var macosPattern = regexp.MustCompile(`^\d+\.\d+(?:\.\d+)?$`)

func decodeArtifacts(raw []byte) ([]Artifact, error) {
	var rows []struct {
		Target    string  `json:"target"`
		URL       string  `json:"url"`
		Signature *string `json:"signature"`
		Bytes     int64   `json:"bytes"`
		SHA256    string  `json:"sha256"`
	}
	if err := json.Unmarshal(raw, &rows); err != nil {
		return nil, domain.Validation()
	}
	seen := map[string]bool{}
	out := []Artifact{}
	for _, r := range rows {
		u, err := url.Parse(r.URL)
		if err != nil || u.Hostname() == "" || u.User != nil || (u.Scheme != "https" && u.Scheme != "http") || !hashPattern.MatchString(r.SHA256) || r.Bytes <= 0 || seen[r.Target] || (r.Target != "darwin-aarch64" && r.Target != "darwin-x86_64" && r.Target != "dmg-universal") {
			return nil, domain.Validation()
		}
		seen[r.Target] = true
		out = append(out, Artifact{Target: r.Target, URL: r.URL, Signature: r.Signature, Bytes: r.Bytes, SHA256: r.SHA256})
	}
	if len(out) == 0 {
		return nil, domain.Validation()
	}
	return out, nil
}
func Generate(row store.DesktopRelease) (Manifest, error) {
	notes, _ := row.LocalizedNotes(row.SourceLocale)
	out := Manifest{Version: row.Version, Notes: notes, Platforms: map[string]Platform{}}
	if out.Notes == "" {
		out.Notes = row.NotesEN
	}
	if row.PubDate == nil {
		return out, domain.Validation()
	}
	out.PubDate = row.PubDate.UTC()
	artifacts, err := decodeArtifacts(row.Artifacts)
	if err != nil {
		return out, err
	}
	for _, a := range artifacts {
		if a.Target == "dmg-universal" {
			continue
		}
		if a.Signature == nil || strings.TrimSpace(*a.Signature) == "" {
			return out, &domain.AppError{Code: domain.CodeInvalidState}
		}
		out.Platforms[a.Target] = Platform{Signature: *a.Signature, URL: a.URL}
	}
	if len(out.Platforms) != 2 {
		return out, &domain.AppError{Code: domain.CodeInvalidState}
	}
	return out, nil
}
func (s *Service) now() time.Time {
	if s.Now != nil {
		return s.Now().UTC()
	}
	return time.Now().UTC()
}
func (s *Service) List(ctx context.Context, channel string, current, size int) (store.AdminPage, error) {
	where, args := "TRUE", []any{}
	if channel != "" {
		where = "d.channel=?"
		args = append(args, channel)
	}
	return s.Store.AdminPage(ctx, store.DesktopJSON, store.DesktopFrom, where, "d.created_at DESC,d.id DESC", args, current, size)
}
func (s *Service) Mutate(ctx context.Context, op string, id int64, body map[string]any, actor store.AuditActor) (any, error) {
	channel, _ := body["channel"].(string)
	if id > 0 {
		row, err := s.Store.DesktopRelease(ctx, id)
		if err != nil {
			return nil, store.MapAdminError(err)
		}
		channel = row.Channel
	}
	var result any
	err := s.Store.WithAdvisoryLock(ctx, "desktop:"+channel, func(ctx context.Context) error {
		key := "desktop/" + channel + "/latest.json"
		var previous []byte
		manifestChanged := false
		txErr := s.Store.WithTx(ctx, func(tx *gorm.DB) error {
			st := &store.Store{DB: tx, Redis: s.Store.Redis}
			var before any
			if id > 0 {
				raw, err := st.AdminDesktop(ctx, id)
				if err != nil {
					return err
				}
				if err := json.Unmarshal(raw, &before); err != nil {
					return err
				}
			}
			historyRef := content.Ref{Entity: "desktop_release", ID: id}
			historyBefore, err := st.HistorySnapshot(ctx, historyRef)
			if err != nil {
				return err
			}
			row, refresh, err := s.modify(ctx, st, op, &id, body, actor)
			if err != nil {
				return err
			}
			result = map[string]any{"id": id}
			if op == "CreateDesktopRelease" && row.ID > 0 {
				return nil // Idempotent retries do not duplicate note revisions or audit records.
			}
			if refresh {
				if s.Objects == nil {
					return domain.Internal(nil)
				}
				manifest, err := Generate(row)
				if err != nil {
					return err
				}
				data, err := json.Marshal(manifest)
				if err != nil {
					return err
				}
				previous, err = s.Objects.Read(ctx, key)
				if err != nil {
					return err
				}
				// Upload first; if the database commit fails, restore the previous manifest while still holding the channel lock.
				manifestChanged = true
				if err := s.Objects.Put(ctx, key, data, "application/json"); err != nil {
					return err
				}
			}
			raw, err := st.AdminDesktop(ctx, id)
			if err != nil {
				return err
			}
			var after any
			if err := json.Unmarshal(raw, &after); err != nil {
				return err
			}
			historyRef.ID = id
			historyAfter, err := st.HistorySnapshot(ctx, historyRef)
			if err != nil {
				return err
			}
			if historyBefore == nil {
				// This ledger covers release notes only; restore/undo must not change desktop release objects.
				historyBefore = store.HistoryState{"desktop_releases": historyAfter["desktop_releases"], "desktop_release_i18n": {}}
			}
			if store.HistoryHash(historyBefore) != store.HistoryHash(historyAfter) {
				operation := domain.CurrentOperation(ctx)
				if operation == nil {
					operation = &domain.Operation{Name: op, ActorID: actor.ID, RequiredPermissions: []string{"release:desktop:edit"}}
				}
				if _, err := st.RecordRevision(ctx, historyRef, historyBefore, historyAfter, "", operation); err != nil {
					return err
				}
			}
			return store.Audit(ctx, tx, actor, op, "desktop_release", fmt.Sprint(id), before, after)
		})
		if txErr != nil && manifestChanged {
			cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
			defer cancel()
			if previous == nil {
				txErr = errors.Join(txErr, s.Objects.Delete(cleanup, key))
			} else {
				txErr = errors.Join(txErr, s.Objects.Put(cleanup, key, previous, "application/json"))
			}
		}
		return txErr
	})
	if err != nil {
		return nil, store.MapAdminError(err)
	}
	if s.Store.Redis != nil {
		if err := cache.PublishInvalidation(ctx, s.Store.Redis, "c:*"); err != nil {
			return nil, err
		}
	}
	return result, nil
}
func (s *Service) modify(ctx context.Context, st *store.Store, op string, id *int64, body map[string]any, actor store.AuditActor) (store.DesktopRelease, bool, error) {
	if op == "CreateDesktopRelease" {
		raw, err := json.Marshal(body["artifacts"])
		if err != nil {
			return store.DesktopRelease{}, false, err
		}
		if _, err := decodeArtifacts(raw); err != nil {
			return store.DesktopRelease{}, false, err
		}
		macos := "13.0"
		if value, ok := body["minMacos"].(string); ok {
			macos = value
		}
		if !macosPattern.MatchString(macos) {
			return store.DesktopRelease{}, false, domain.Validation()
		}
		// Registration retries under the same channel lock return the original ID without overwriting administrator notes or publication state.
		var existing store.DesktopRelease
		result := st.DB.WithContext(ctx).Raw("SELECT * FROM desktop_releases WHERE version=? AND channel=?", body["version"], body["channel"]).Scan(&existing)
		if result.Error != nil {
			return existing, false, result.Error
		}
		if result.RowsAffected > 0 {
			previous, decodeErr := decodeArtifacts(existing.Artifacts)
			if decodeErr != nil {
				return existing, false, decodeErr
			}
			incoming, decodeErr := decodeArtifacts(raw)
			if decodeErr != nil {
				return existing, false, decodeErr
			}
			byTarget := func(rows []Artifact) map[string]Artifact {
				out := make(map[string]Artifact, len(rows))
				for _, item := range rows {
					out[item.Target] = item
				}
				return out
			}
			if existing.MinMacos != macos || !reflect.DeepEqual(byTarget(previous), byTarget(incoming)) {
				return existing, false, &domain.AppError{Code: domain.CodeInvalidState}
			}
			*id = existing.ID
			return existing, false, nil
		}
		err = st.DB.WithContext(ctx).Raw("INSERT INTO desktop_releases(version,channel,min_macos,artifacts) VALUES(?,?,?,?::jsonb) RETURNING id", body["version"], body["channel"], macos, string(raw)).Scan(id).Error
		if err == nil {
			err = st.WriteLocalized(ctx, "desktop_release", *id, 0, body, false)
		}
		return store.DesktopRelease{}, false, err
	}
	row, err := st.DesktopRelease(ctx, *id)
	if err != nil {
		return row, false, err
	}
	switch op {
	case "UpdateDesktopRelease":
		if err := st.WriteLocalized(ctx, "desktop_release", *id, 0, body, false); err != nil {
			return row, false, err
		}
		fields := map[string]any{"updated_at": s.now()}
		for k, col := range map[string]string{"minMacos": "min_macos"} {
			if v, ok := body[k]; ok {
				fields[col] = v
			}
		}
		if v, ok := body["minMacos"].(string); ok && !macosPattern.MatchString(v) {
			return row, false, domain.Validation()
		}
		if err := st.DB.WithContext(ctx).Table("desktop_releases").Where("id=?", *id).Updates(fields).Error; err != nil {
			return row, false, err
		}
		row, err = st.DesktopRelease(ctx, *id)
		if err != nil {
			return row, false, err
		}
		latest, err := st.LatestDesktop(ctx, row.Channel, 0)
		if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
			return row, false, err
		}
		return row, err == nil && latest.ID == row.ID, nil
	case "PublishDesktopRelease":
		if row.Status == "rolled_back" {
			return row, false, &domain.AppError{Code: domain.CodeInvalidState}
		}
		if row.Status == "published" {
			latest, err := st.LatestDesktop(ctx, row.Channel, 0)
			if err != nil {
				return row, false, err
			}
			if latest.ID != row.ID {
				return row, false, &domain.AppError{Code: domain.CodeInvalidState}
			}
		}
		if row.Status == "draft" {
			date := s.now()
			row.PubDate = &date
		}
		if _, err := Generate(row); err != nil {
			return row, false, err
		}
		if err := st.DB.WithContext(ctx).Exec("UPDATE desktop_releases SET status='published',pub_date=?,published_by=?,updated_at=? WHERE id=?", row.PubDate, actor.ID, s.now(), *id).Error; err != nil {
			return row, false, err
		}
		// Read publication timestamps at database precision so rollback can recreate the exact same manifest.
		row, err = st.DesktopRelease(ctx, *id)
		return row, true, err
	case "RollbackDesktopRelease":
		latest, err := st.LatestDesktop(ctx, row.Channel, 0)
		if err != nil {
			return row, false, err
		}
		if row.Status != "published" || latest.ID != row.ID {
			return row, false, &domain.AppError{Code: domain.CodeInvalidState}
		}
		previous, err := st.LatestDesktop(ctx, row.Channel, row.ID)
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return row, false, &domain.AppError{Code: domain.CodeInvalidState}
		}
		if err != nil {
			return row, false, err
		}
		if err := st.DB.WithContext(ctx).Exec("UPDATE desktop_releases SET status='rolled_back',updated_at=? WHERE id=?", s.now(), row.ID).Error; err != nil {
			return row, false, err
		}
		return previous, true, nil
	}
	return row, false, domain.Validation()
}
func (s *Service) Latest(ctx context.Context, channel, locale string) (publicapi.DesktopReleaseInfo, error) {
	row, err := s.Store.LatestDesktop(ctx, channel, 0)
	if err != nil {
		return publicapi.DesktopReleaseInfo{}, store.MapAdminError(err)
	}
	notes, machine := row.LocalizedNotes(locale)
	source := publicapi.AppLocale(row.SourceLocale)
	out := publicapi.DesktopReleaseInfo{SourceLocale: &source, MachineTranslated: &machine, Version: row.Version, Channel: publicapi.DesktopReleaseInfoChannel(row.Channel), MinMacos: row.MinMacos, Notes: notes, Downloads: []publicapi.DesktopDownload{}}
	if row.PubDate != nil {
		out.PubDate = row.PubDate.UTC()
	}
	artifacts, err := decodeArtifacts(row.Artifacts)
	if err != nil {
		return out, err
	}
	for _, a := range artifacts {
		out.Downloads = append(out.Downloads, publicapi.DesktopDownload{Target: publicapi.DesktopDownloadTarget(a.Target), Url: a.URL, Bytes: a.Bytes, Sha256: a.SHA256})
	}
	rows, err := s.Store.RecentDesktopReleases(ctx, channel)
	if err != nil {
		return out, err
	}
	recent := make([]publicapi.DesktopReleaseNotes, 0, len(rows))
	for _, release := range rows {
		text, machine := release.LocalizedNotes(locale)
		source := publicapi.AppLocale(release.SourceLocale)
		if release.PubDate != nil {
			recent = append(recent, publicapi.DesktopReleaseNotes{SourceLocale: &source, MachineTranslated: &machine, Version: release.Version, PubDate: release.PubDate.UTC(), Notes: text})
		}
	}
	out.RecentReleases = &recent
	return out, nil
}
