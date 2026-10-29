package management

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/opennavo/opennavo/server/internal/domain"
	"github.com/opennavo/opennavo/server/internal/homebrew"
	"github.com/opennavo/opennavo/server/internal/service/taps"
	"github.com/opennavo/opennavo/server/internal/store"
)

func tapView(cask store.TapCask) map[string]any {
	view := map[string]any{
		"id": cask.PackageID, "token": cask.Token, "fullToken": cask.FullToken, "tap": cask.Tap,
		"name": cask.Name, "version": cask.Version, "sourcePath": cask.SourcePath,
		"sourceUrl": "https://github.com/" + homebrew.TapRepository(cask.Tap) + "/blob/HEAD/" + cask.SourcePath,
		"headSha":   nil, "checkedAt": cask.CheckedAt, "lastError": nil, "removed": cask.RemovedAt != nil, "createdAt": cask.CreatedAt,
	}
	if cask.HeadSHA != "" {
		view["headSha"] = cask.HeadSHA
	}
	if cask.LastError != "" {
		view["lastError"] = cask.LastError
	}
	return view
}

func (s *Service) listTapPackages(ctx context.Context) (any, error) {
	casks, err := s.Store.TapCasks(ctx)
	if err != nil {
		return nil, err
	}
	records := make([]map[string]any, 0, len(casks))
	for _, cask := range casks {
		records = append(records, tapView(cask))
	}
	return map[string]any{"records": records}, nil
}

func (s *Service) readTapCandidate(ctx context.Context, in Input) (*taps.Candidate, error) {
	siteToken, custom := in.Body["siteToken"]
	if custom {
		value, ok := siteToken.(string)
		if !ok || !homebrew.ValidCaskToken(value) {
			return nil, domain.Rejected("tap_site_token_invalid")
		}
	}
	if s.Taps == nil {
		return nil, domain.Internal(errors.New("tap reader is not configured"))
	}
	candidate, err := s.Taps.Read(ctx, strings.TrimSpace(text(in.Body, "cask")))
	if err == nil && custom {
		candidate.Package.Token = siteToken.(string)
	}
	return candidate, err
}

func tapOwner(ctx context.Context, st *store.Store, candidate *taps.Candidate, custom bool) (*store.CaskOwner, error) {
	if custom {
		// Site routes are shared by casks and link apps, including removed entries.
		if err := st.LinkTokenAvailable(ctx, candidate.Package.Token, 0); err != nil {
			var app *domain.AppError
			if errors.As(err, &app) && app.Code == domain.CodeConflict {
				return nil, &domain.AppError{Code: domain.CodeConflict, HTTPStatus: http.StatusConflict, Msg: "rejected: tap_site_token_taken:" + candidate.Package.Token}
			}
			return nil, err
		}
	}
	owner, err := st.CaskOwner(ctx, candidate.Package.Token)
	if err != nil {
		return nil, err
	}
	if err := taps.Conflict(owner, candidate.Tap, candidate.Package.Token); err != nil {
		return nil, err
	}
	upstream, err := st.TrackedCaskOwner(ctx, candidate.Package.FullToken)
	if err != nil {
		return nil, err
	}
	if upstream != nil {
		return nil, &domain.AppError{Code: domain.CodeConflict, HTTPStatus: http.StatusConflict, Msg: "rejected: tap_cask_already_added:" + candidate.Package.FullToken}
	}
	return owner, nil
}

func (s *Service) addTapPackage(ctx context.Context, op string, in Input) (any, error) {
	candidate := in.tapCandidate
	if candidate == nil {
		// The admin path fetches here; the Agent path prepares the candidate
		// before entering its enclosing catalog transaction.
		var err error
		candidate, err = s.readTapCandidate(ctx, in)
		if err != nil {
			return nil, err
		}
	}
	_, custom := in.Body["siteToken"]
	owner, err := tapOwner(ctx, s.Store, candidate, custom)
	if err != nil {
		return nil, err
	}
	if domain.CurrentOperation(ctx).DryRun {
		return tapPreview(ctx, candidate, owner), nil
	}
	return s.write(ctx, in, op, "tap_cask", func(st *store.Store) (any, error) {
		if err := store.LockCatalogWrites(ctx, st.DB); err != nil {
			return nil, err
		}
		if _, err := tapOwner(ctx, st, candidate, custom); err != nil {
			return nil, err
		}
		now := s.now()
		id, err := taps.Write(ctx, st, candidate, now)
		if err != nil {
			return nil, err
		}
		if err := st.RegisterTapCask(ctx, id, candidate.Tap, candidate.SourcePath, candidate.ETag, candidate.HeadSHA, candidate.HistorySHA, now); err != nil {
			return nil, err
		}
		// Versions from the file's history accept release notes as soon as the cask is added.
		if _, err := st.RecordTapVersions(ctx, id, candidate.History, now); err != nil {
			return nil, err
		}
		cask, err := st.TapCask(ctx, id)
		if err != nil || cask == nil {
			return nil, errors.Join(err, errors.New("tap cask was not registered"))
		}
		return tapView(*cask), nil
	})
}

// tapPreview describes what adding the cask would write, in the AgentDryRun shape.
func tapPreview(ctx context.Context, candidate *taps.Candidate, owner *store.CaskOwner) map[string]any {
	item := candidate.Package
	var artifacts homebrew.CaskArtifacts
	_ = json.Unmarshal(item.Artifacts, &artifacts)
	after := map[string]any{
		"token": item.Token, "fullToken": item.FullToken, "tap": item.Tap, "name": item.Name, "version": item.Version,
		"desc": item.Desc, "homepage": item.Homepage, "downloadUrl": item.DownloadURL, "autoUpdates": item.AutoUpdates,
		"apps": artifacts.Apps, "binaries": artifacts.Binaries, "pkgs": artifacts.Pkgs, "supportsArm64": item.SupportsArm64,
		"supportsX86_64": item.SupportsX8664, "minMacos": item.MinMacos, "sourcePath": candidate.SourcePath, "headSha": candidate.HeadSHA,
	}
	// The versions the file's history declares, newest first; package_versions lists them after the add.
	if candidate.HistorySHA != "" {
		versions := make([]string, 0, len(candidate.History))
		for i := len(candidate.History) - 1; i >= 0; i-- {
			versions = append(versions, candidate.History[i].Version)
		}
		after["versions"] = versions
	}
	action, key := "create", item.FullToken
	var before any
	if owner != nil {
		action, key = "update", fmt.Sprint(owner.ID)
		before = map[string]any{"tap": owner.Tap, "removed": owner.RemovedAt != nil}
	}
	return map[string]any{"dryRun": true, "requestId": domain.CurrentOperation(ctx).RequestID, "affectedUrls": []string{},
		"changes": []any{map[string]any{"object": map[string]any{"entity": "package", "objectKey": key}, "action": action, "before": before, "after": after}}}
}

func (s *Service) removeTapPackage(ctx context.Context, op string, in Input) (any, error) {
	cask, err := s.Store.TapCask(ctx, in.ID)
	if err != nil {
		return nil, err
	}
	if cask == nil {
		return nil, &domain.AppError{Code: domain.CodeNotFound, HTTPStatus: http.StatusNotFound}
	}
	if domain.CurrentOperation(ctx).DryRun {
		return map[string]any{"dryRun": true, "requestId": domain.CurrentOperation(ctx).RequestID, "affectedUrls": []string{},
			"changes": []any{map[string]any{"object": map[string]any{"entity": "package", "objectKey": fmt.Sprint(in.ID)}, "action": "delete", "before": tapView(*cask), "after": nil}}}, nil
	}
	return s.write(ctx, in, op, "tap_cask", func(st *store.Store) (any, error) {
		return nil, st.UnregisterTapCask(ctx, in.ID)
	})
}
