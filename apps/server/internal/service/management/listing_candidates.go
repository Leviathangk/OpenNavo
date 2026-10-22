package management

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"path"
	"slices"
	"strings"

	"github.com/opennavo/opennavo/server/internal/content"
	"github.com/opennavo/opennavo/server/internal/domain"
	"github.com/opennavo/opennavo/server/internal/facts"
	"github.com/opennavo/opennavo/server/internal/seo"
	"github.com/opennavo/opennavo/server/internal/service/agentgate"
	"github.com/opennavo/opennavo/server/internal/service/seotasks"
	"github.com/opennavo/opennavo/server/internal/store"
)

func publicationEvidenceError(err error) error {
	var violation *agentgate.Violation
	if errors.As(err, &violation) {
		return &domain.AppError{Code: domain.CodeValidation, HTTPStatus: 422, Cause: violation}
	}
	return err
}

func evidenceIntact(e agentgate.Evidence) bool {
	h := sha256.Sum256([]byte(e.BodyText))
	return hex.EncodeToString(h[:]) == e.BodyHash && e.FetchStatus == 200 && strings.Contains(e.BodyText, e.Excerpt)
}
func officialHost(raw string) string {
	if !domain.LinkHTTPS(raw) {
		return ""
	}
	u, err := url.Parse(raw)
	if err != nil || u.Port() != "" {
		return ""
	}
	return strings.ToLower(u.Hostname())
}
func officialPage(raw, host string) bool {
	if host == "" || officialHost(raw) != host {
		return false
	}
	u, _ := url.Parse(raw)
	// Extensionless endpoints cannot be proven to be HTML from their URL alone.
	// Publication also binds the address to H2's captured HTML/text evidence.
	for _, v := range append([]string{u.Path}, queryValues(u)...) {
		ext := strings.ToLower(path.Ext(v))
		if slices.Contains([]string{".dmg", ".pkg", ".zip", ".gz", ".xz", ".bz2", ".exe", ".app", ".iso", ".tar", ".7z"}, ext) {
			return false
		}
	}
	return true
}
func queryValues(u *url.URL) []string {
	out := []string{}
	for _, vs := range u.Query() {
		out = append(out, vs...)
	}
	return out
}
func decodeStrictBody(body map[string]any, out any) error {
	raw, err := json.Marshal(body)
	if err != nil {
		return domain.Validation()
	}
	d := json.NewDecoder(strings.NewReader(string(raw)))
	d.DisallowUnknownFields()
	if d.Decode(out) != nil {
		return domain.Validation()
	}
	return nil
}

// ListingAgentRevision and ListingAgentSnapshot are extension points for H2.
// The generic gate still owns leases, policy, idempotency and atomic actions.
func ListingAgentRevision(ctx context.Context, st *store.Store, token string) (int64, error) {
	c, err := st.ListingCandidate(ctx, token)
	if err != nil || c == nil {
		return 0, err
	}
	return c.Revision, nil
}
func ListingAgentSnapshot(ctx context.Context, st *store.Store, token string) (any, error) {
	return st.ListingCandidate(ctx, token)
}

func (s *Service) lockListingBaseline(ctx context.Context, in Input) (any, error) {
	op := domain.CurrentOperation(ctx)
	if op == nil || op.AgentClientID != 0 || op.AgentTokenID != 0 || op.ActorID == nil || !op.Allowed("catalog:package:edit") {
		return nil, fail(domain.CodeForbidden)
	}
	var req struct {
		FactID     int64 `json:"factId"`
		EvidenceID int64 `json:"evidenceId"`
		Confirmed  bool  `json:"domainOwnershipConfirmed"`
		Suspicious bool  `json:"suspicious"`
	}
	if err := decodeStrictBody(in.Body, &req); err != nil {
		return nil, err
	}
	if !req.Confirmed || req.FactID < 1 || req.EvidenceID < 1 {
		return nil, domain.Validation()
	}
	return s.write(ctx, in, "LockSeoListingBaseline", "listing_baseline", func(st *store.Store) (any, error) {
		if err := store.LockCatalogWrites(ctx, st.DB); err != nil {
			return nil, err
		}
		var home string
		if err := st.DB.WithContext(ctx).Raw("SELECT homepage FROM packages WHERE id=? AND kind IN ('cask','link') AND removed_at IS NULL", in.ID).Scan(&home).Error; err != nil {
			return nil, err
		}
		host := officialHost(home)
		if host == "" {
			return nil, domain.Validation()
		}
		records, err := st.FactRecords(ctx, in.ID)
		if err != nil {
			return nil, err
		}
		var identity facts.SecurityObservation
		found := false
		for _, r := range records {
			if r.ID == req.FactID && r.Kind == "security" && r.Evidence.At(s.now()).Status == "verified" {
				if json.Unmarshal(r.Value, &identity) != nil {
					return nil, domain.Validation()
				}
				found = true
			}
		}
		if !found || identity.TeamID == "" || identity.Developer == "" || identity.Validate() != nil {
			return nil, domain.Validation()
		}
		evidence, err := agentgate.LoadEvidence(ctx, st.DB, []int64{req.EvidenceID}, fmt.Sprintf("pkg:%d", in.ID), true)
		if err != nil {
			return nil, publicationEvidenceError(err)
		}
		e := evidence[0]
		if !evidenceIntact(e) || officialHost(e.FinalURL) != host || !strings.Contains(e.Excerpt, identity.TeamID) || !strings.Contains(e.Excerpt, identity.Developer) {
			return nil, domain.Validation()
		}
		old, err := st.ListingBaseline(ctx, in.ID)
		if err != nil {
			return nil, err
		}
		if old != nil && (old.Domain != host || old.TeamID != identity.TeamID || old.Developer != identity.Developer) {
			return nil, fail(domain.CodeConflict)
		}
		err = st.DB.WithContext(ctx).Exec(`INSERT INTO seo_listing_baselines(package_id,domain,developer,team_id,fact_id,evidence_id,evidence_hash,verified_by,valid_until,suspicious)
   VALUES(?,?,?,?,?,?,?,?,?,?) ON CONFLICT(package_id) DO UPDATE SET evidence_id=EXCLUDED.evidence_id,evidence_hash=EXCLUDED.evidence_hash,verified_by=EXCLUDED.verified_by,verified_at=now(),valid_until=EXCLUDED.valid_until,suspicious=EXCLUDED.suspicious`, in.ID, host, identity.Developer, identity.TeamID, req.FactID, e.ID, e.BodyHash, *op.ActorID, e.ValidUntil, req.Suspicious).Error
		if err != nil {
			return nil, err
		}
		return st.ListingBaseline(ctx, in.ID)
	})
}

func (s *Service) listingView(ctx context.Context, st *store.Store, token string) (map[string]any, error) {
	c, err := st.ListingCandidate(ctx, token)
	if err != nil {
		return nil, err
	}
	if c == nil {
		return nil, fail(domain.CodeNotFound)
	}
	var indexing any
	if c.PackageID != nil {
		row, found, err := st.IndexRowByID(ctx, *c.PackageID)
		if err != nil {
			return nil, err
		}
		if found {
			policy, err := st.IndexPolicy(ctx)
			if err != nil {
				return nil, err
			}
			var p store.ListingInput
			if json.Unmarshal(c.Payload, &p) != nil {
				return nil, domain.Internal(nil)
			}
			indexing = seo.DecidePage(row.Facts(), seo.PageRef{Kind: seo.PageOverview, Locale: p.Link.SourceLocale}, policy)
		}
	}
	identityVerified := false
	if c.State == "published" && c.BaselinePackageID != nil {
		b, err := st.ListingBaseline(ctx, *c.BaselinePackageID)
		if err != nil {
			return nil, err
		}
		identityVerified = b != nil && !b.Suspicious && b.ValidUntil.After(s.now())
	}
	return map[string]any{"id": c.ID, "token": c.Token, "state": c.State, "revision": c.Revision, "packageId": c.PackageID, "identityVerified": identityVerified, "canDirectDownload": false, "indexing": indexing, "input": json.RawMessage(c.Payload)}, nil
}

func (s *Service) listingCandidate(ctx context.Context, name string, in Input) (any, error) {
	if name == "GetSeoListingCandidate" {
		return s.listingView(ctx, s.Store, in.Key)
	}
	var req store.ListingInput
	if err := decodeStrictBody(in.Body, &req); err != nil {
		return nil, err
	}
	if req.Link.Token != in.Key || req.ExpectedRevision < 0 || !slices.Contains([]string{"save", "publish", "reject"}, req.Action) || req.Link.Validate() != nil || len(req.Summary) > 400 || len(req.Description) > 100000 || req.BaselinePackageID < 0 || !slices.Contains([]string{"official_page", "not_applicable"}, req.DownloadMode) {
		return nil, domain.Validation()
	}
	if op := domain.CurrentOperation(ctx); op.AgentClientID > 0 && (op.Execution == nil || op.Execution.ExpectedRevision != req.ExpectedRevision) {
		return nil, fail(domain.CodeConflict)
	}
	return s.write(ctx, in, name, "listing", func(st *store.Store) (any, error) {
		if err := store.LockCatalogWrites(ctx, st.DB); err != nil {
			return nil, err
		}
		old, err := st.ListingCandidate(ctx, in.Key)
		if err != nil {
			return nil, err
		}
		revision := int64(0)
		if old != nil {
			revision = old.Revision
			if old.State != "candidate" && (old.State != "published" || req.Action != "publish") {
				return nil, fail(domain.CodeInvalidState)
			}
		}
		if revision != req.ExpectedRevision {
			return nil, fail(domain.CodeConflict)
		}
		state := "candidate"
		var packageID any
		if req.Action == "reject" {
			state = "rejected"
		}
		if req.Action == "publish" {
			existingID := int64(0)
			if old != nil && old.PackageID != nil {
				existingID = *old.PackageID
				app, err := st.LinkApp(ctx, existingID)
				if err != nil {
					return nil, err
				}
				if app == nil || app.Kind != "link" || app.Removed {
					return nil, fail(domain.CodeInvalidState)
				}
			}
			if err := s.validateListingExcept(ctx, st, req, existingID); err != nil {
				return nil, err
			}
			// Revalidate the replacement before advancing the trigger's identity snapshot.
			// Any subsequent failure rolls this and the package write back together.
			if existingID > 0 {
				payload, err := json.Marshal(req)
				if err != nil {
					return nil, err
				}
				if err := st.DB.WithContext(ctx).Exec("UPDATE seo_listing_candidates SET payload=?::jsonb WHERE id=?", string(payload), old.ID).Error; err != nil {
					return nil, err
				}
			}
			id, err := st.WriteLinkApp(ctx, existingID, req.Link)
			if err != nil {
				return nil, err
			}
			if err := st.DB.WithContext(ctx).Exec(`INSERT INTO package_meta(package_id,icon_asset_id,icon_source,developer) VALUES(?,?,'manual',(SELECT developer FROM seo_listing_baselines WHERE package_id=?)) ON CONFLICT(package_id) DO UPDATE SET icon_asset_id=EXCLUDED.icon_asset_id,icon_source=EXCLUDED.icon_source,developer=COALESCE(EXCLUDED.developer,package_meta.developer)`, id, req.IconAssetID, req.BaselinePackageID).Error; err != nil {
				return nil, err
			}
			if err := st.DB.WithContext(ctx).Exec("UPDATE package_categories SET is_primary=false WHERE package_id=? AND is_primary", id).Error; err != nil {
				return nil, err
			}
			if err := st.DB.WithContext(ctx).Exec("INSERT INTO package_categories(package_id,category_id,is_primary,source) VALUES(?,?,true,'human') ON CONFLICT(package_id,category_id) DO UPDATE SET is_primary=true", id, req.CategoryID).Error; err != nil {
				return nil, err
			}
			if err := st.WriteContentText(ctx, content.Ref{Entity: "package", ID: id}, req.Link.SourceLocale, map[string]any{"displayName": req.Link.Name, "summary": req.Summary, "description": req.Description}, true); err != nil {
				return nil, err
			}
			packageID = id
			state = "published"
		}
		payload, err := json.Marshal(req)
		if err != nil {
			return nil, err
		}
		var baseline any
		if req.BaselinePackageID > 0 {
			baseline = req.BaselinePackageID
		}
		err = st.DB.WithContext(ctx).Exec(`INSERT INTO seo_listing_candidates(token,state,payload,baseline_package_id,package_id) VALUES(?,?,?::jsonb,?,?)
   ON CONFLICT(token) DO UPDATE SET state=EXCLUDED.state,payload=EXCLUDED.payload,baseline_package_id=EXCLUDED.baseline_package_id,package_id=EXCLUDED.package_id,revision=seo_listing_candidates.revision+1,updated_at=now()`, in.Key, state, string(payload), baseline, packageID).Error
		if err != nil {
			return nil, err
		}
		return s.listingView(ctx, st, in.Key)
	})
}
func (s *Service) validateListing(ctx context.Context, st *store.Store, req store.ListingInput) error {
	return s.validateListingExcept(ctx, st, req, 0)
}
func (s *Service) validateListingExcept(ctx context.Context, st *store.Store, req store.ListingInput, exceptID int64) error {
	if strings.TrimSpace(req.Summary) == "" || req.CategoryID < 1 || req.IconAssetID < 1 || req.Link.Homepage == nil || req.Link.GithubRepo != nil || req.Link.SparkleFeed != nil || len(req.Link.AssetRules) > 0 || len(req.Link.Notes) > 0 {
		return domain.Validation()
	}
	host := officialHost(*req.Link.Homepage)
	if req.BaselinePackageID > 0 {
		b, err := st.ListingBaseline(ctx, req.BaselinePackageID)
		if err != nil {
			return err
		}
		if b == nil || b.Suspicious || !b.ValidUntil.After(s.now()) || b.Domain != host || req.Link.TeamID == nil || b.TeamID != *req.Link.TeamID {
			return domain.Validation()
		}
		baseline, err := agentgate.LoadEvidence(ctx, st.DB, []int64{b.EvidenceID}, fmt.Sprintf("pkg:%d", b.PackageID), true)
		if err != nil {
			return publicationEvidenceError(err)
		}
		if !evidenceIntact(baseline[0]) || baseline[0].BodyHash != b.EvidenceHash {
			return domain.Validation()
		}
	}
	if req.DownloadMode == "official_page" && (req.Link.DownloadURL == nil || !officialPage(*req.Link.DownloadURL, host)) || req.DownloadMode == "not_applicable" && req.Link.DownloadURL != nil {
		return domain.Validation()
	}
	evidence, err := agentgate.LoadEvidence(ctx, st.DB, []int64{req.EvidenceID}, "listing:"+req.Link.Token, false)
	if err != nil {
		return publicationEvidenceError(err)
	}
	e := evidence[0]
	if !evidenceIntact(e) || officialHost(e.FinalURL) != host {
		return domain.Validation()
	}
	if req.DownloadMode == "official_page" && e.FinalURL != *req.Link.DownloadURL {
		return domain.Validation()
	}
	var ready bool
	if err := st.DB.WithContext(ctx).Raw(`SELECT EXISTS(SELECT 1 FROM categories WHERE id=? AND applies_to IN ('both','cask')) AND EXISTS(SELECT 1 FROM assets WHERE id=? AND kind='icon')`, req.CategoryID, req.IconAssetID).Scan(&ready).Error; err != nil {
		return err
	}
	if !ready {
		return domain.Validation()
	}
	duplicate, err := st.ListingDuplicateExcept(ctx, req.Link, exceptID)
	if err != nil {
		return err
	}
	if duplicate {
		return fail(domain.CodeConflict)
	}
	return nil
}

// nominateListing turns an app a person asked for into a listing task, so an
// agent can research it and prepare its candidate. Keyword demand stays the
// other source of listing tasks; nominating publishes nothing by itself. A
// published automatic listing can be nominated again to correct it.
func (s *Service) nominateListing(ctx context.Context, in Input) (any, error) {
	if err := requireSEOAccess(ctx, "seo:task:handle"); err != nil {
		return nil, err
	}
	if err := requireSEOAccess(ctx, "catalog:package:edit"); err != nil {
		return nil, err
	}
	var req struct {
		IdempotencyKey string  `json:"idempotencyKey"`
		Name           string  `json:"name"`
		Homepage       *string `json:"homepage"`
		Reason         string  `json:"reason"`
	}
	if err := decodeStrictBody(in.Body, &req); err != nil {
		return nil, err
	}
	req.Name, req.Reason = strings.TrimSpace(req.Name), strings.TrimSpace(req.Reason)
	if !domain.ValidLinkToken(in.Key) || req.Name == "" || len(req.Name) > 256 || req.Reason == "" || len(req.Reason) > 1000 || req.Homepage != nil && (len(*req.Homepage) > 2048 || !domain.LinkHTTPS(*req.Homepage)) {
		return nil, domain.Validation()
	}
	owner := seoOwner(ctx)
	evidence := map[string]any{"source": "nomination", "token": in.Key, "name": req.Name, "homepage": req.Homepage, "reason": req.Reason, "nominatedBy": owner}
	raw, err := json.Marshal(evidence)
	if err != nil {
		return nil, err
	}
	tasks := &seotasks.Service{DB: s.Store.DB}
	result, err := tasks.Control(ctx, owner, "NominateSeoListing", req.IdempotencyKey, map[string]any{"token": in.Key, "body": in.Body}, domain.CurrentOperation(ctx).DryRun, func(t *seotasks.Service) (any, error) {
		// An automatic listing is corrected by publishing its candidate again, which
		// needs a listing task like the first publication; other packages are not listings.
		var state struct{ Listed, Automatic bool }
		if err := t.DB.WithContext(ctx).Raw("SELECT EXISTS(SELECT 1 FROM packages WHERE token=? AND removed_at IS NULL) AS listed,EXISTS(SELECT 1 FROM seo_listing_candidates WHERE token=? AND state='published') AS automatic", in.Key, in.Key).Scan(&state).Error; err != nil {
			return nil, err
		}
		if state.Listed && !state.Automatic {
			return nil, &seotasks.StateError{Reason: "already_listed"}
		}
		var rejected bool
		if err := t.DB.WithContext(ctx).Raw("SELECT EXISTS(SELECT 1 FROM seo_listing_candidates WHERE token=? AND state='rejected')", in.Key).Scan(&rejected).Error; err != nil {
			return nil, err
		}
		if rejected {
			return nil, &seotasks.StateError{Reason: "candidate_rejected"}
		}
		// A repeated nomination keeps the task that is queued or in progress; a finished
		// one is opened again with the latest request.
		var task seotasks.Task
		r := t.DB.WithContext(ctx).Raw(`INSERT INTO seo_tasks(kind,entity_key,locale,dedupe_key,priority,evidence,evidence_hash,rule_version) VALUES('listing',?,'en-US',?,1000,?::jsonb,?,'nomination-v1')
  ON CONFLICT(dedupe_key) DO UPDATE SET evidence=EXCLUDED.evidence,evidence_hash=EXCLUDED.evidence_hash,priority=greatest(seo_tasks.priority,EXCLUDED.priority),state='open',next_retry_at=NULL,lease_owner=NULL,lease_until=NULL,pending_evidence=NULL,pending_evidence_hash=NULL,updated_at=clock_timestamp()
  WHERE seo_tasks.state IN ('done','skipped','expired') RETURNING *`, "listing:"+in.Key, "nominate:"+in.Key, string(raw), seotasks.Hash(raw)).Scan(&task)
		if r.Error != nil {
			return nil, r.Error
		}
		if r.RowsAffected == 0 {
			if err := t.DB.WithContext(ctx).Raw("SELECT * FROM seo_tasks WHERE dedupe_key=?", "nominate:"+in.Key).Scan(&task).Error; err != nil {
				return nil, err
			}
		}
		return task, nil
	})
	return result, mapTaskError(err)
}
