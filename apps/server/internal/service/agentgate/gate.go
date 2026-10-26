// Package agentgate is the shared write layer for Agent and admin writes:
// operation registration, idempotency and dry-run control, the write log,
// and server-captured evidence for article claims.
package agentgate

import (
	"context"
	"errors"
	"strings"

	"github.com/opennavo/opennavo/server/internal/domain"
	"gorm.io/gorm"
)

// RuleVersion is recorded with every write event.
const RuleVersion = "agent-v1"

// Rule groups an operation by its kind of change and the object it edits;
// the object selects how the write's revision and snapshots are read.
type Rule struct{ Action, Object string }

// Explicit registration fails closed when a new endpoint is added. HTTP
// allowlisting alone is not a business authorization boundary.
func RuleFor(operation string) (Rule, bool) {
	groups := []struct {
		r          Rule
		operations string
	}{
		{Rule{"translation", "translation"}, "FixTranslation"},
		{Rule{"maintenance", "package"}, "UpdatePackageText UpdatePackageMeta SetPackagePrice SetPackageMacosSupport SetPackageAlternatives UpdatePackageI18n SetPackageCategories SetPackageIcon DeletePackageIcon AddPackageScreenshot ReorderPackageScreenshots UpdatePackageScreenshot DeletePackageScreenshot UpdatePackageChangelogSettings"},
		{Rule{"maintenance", "release"}, "UpsertReleaseNotes UpdateRelease UpdateReleaseI18n"},
		{Rule{"fact_fill", "package"}, "SetPackageFactRecords"},
		{Rule{"fact_fill", "background"}, "SetBackgroundItem"},
		{Rule{"maintenance", "asset"}, "UploadAsset CreateAssetUploadTicket UploadAssetWithTicket"},
		{Rule{"content", "collection"}, "CreateCollection UpdateCollection DeleteCollection SetCollectionItems PublishCollection UnpublishCollection SetCollectionSetupCriteria"},
		{Rule{"content", "category"}, "CreateCategory UpdateCategory DeleteCategory ReorderCategories"},
		{Rule{"content", "feature"}, "CreateFeature UpdateFeature DeleteFeature"},
		{Rule{"content", "announcement"}, "UpdateAnnouncement"},
		{Rule{"maintenance", "desktop_release"}, "UpdateDesktopReleaseNotes"},
		{Rule{"content", "glossary"}, "UpsertGlossaryTerm DeleteGlossaryTerm"},
		{Rule{"listing", "package"}, "AddLinkPackage UpdateLinkPackage AddTapPackage"},
		{Rule{"withdraw", "package"}, "RemoveLinkPackage RemoveTapPackage"},
		{Rule{"article", "article"}, "CreateArticle UpdateArticle SetArticlePackages SubmitArticleReview PublishArticle RestoreArticle RevertArticle SetArticleSlug SetArticleLocale"},
		{Rule{"withdraw", "article"}, "WithdrawArticle"},
		{Rule{"article", "article_author"}, "CreateArticleAuthor UpdateArticleAuthor"},
		{Rule{"compensate", "history"}, "RestoreRevision RestoreTrash RevertRequest"},
		{Rule{"operations", "synonym"}, "CreateSynonym UpdateSynonym DeleteSynonym"},
		{Rule{"operations", "feedback"}, "UpdateFeedback"},
		{Rule{"operations", "update_feed"}, "UpsertUpdateFeed DisableUpdateFeed"},
		{Rule{"operations", "job"}, "ResyncPackage TriggerJob"},
		{Rule{"maintenance", "intent_pages"}, "SetPackageIntentPages"},
		{Rule{"maintenance", "package_seo"}, "SetPackageSeo"},
	}
	for _, g := range groups {
		for _, name := range strings.Fields(g.operations) {
			if operation == name {
				return g.r, true
			}
		}
	}
	return Rule{}, false
}

// Log records a write outcome without submitted bodies or credentials.
// Rejections are written after rollback by the caller, so failed validation
// remains observable.
func Log(ctx context.Context, db *gorm.DB, request, owner, operation, result, reason string) error {
	return db.WithContext(ctx).Exec(`INSERT INTO agent_write_events(request_id,owner,operation,rule_version,result,reason) VALUES(?,?,?,?,?,?)`, request, owner, operation, RuleVersion, result, reason).Error
}

type Violation struct{ Reason string }

func (e *Violation) Error() string { return "Agent gate: " + e.Reason }
func reject(reason string) error   { return &Violation{Reason: reason} }

func IsViolation(err error) bool { var v *Violation; return errors.As(err, &v) }

// Rejection names the broken rule so an agent can correct its request instead of
// retrying blind. Reasons are fixed rule codes and never carry fetched content.
func Rejection(err error, status int) (*domain.AppError, string, bool) {
	var v *Violation
	if !errors.As(err, &v) {
		return nil, "", false
	}
	return &domain.AppError{Code: domain.CodeValidation, HTTPStatus: status, Msg: "rejected: " + v.Reason}, v.Reason, true
}
