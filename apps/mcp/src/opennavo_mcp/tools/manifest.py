"""The single handwritten manifest of tools, permissions and internal operation mappings."""

from dataclasses import dataclass


@dataclass(frozen=True)
class ToolSpec:
    name: str
    permission: str
    operations: tuple[str, ...]
    write: bool = False
    delete: bool = False


def tool(
    name: str, permission: str, operations: str, *, write: bool = False, delete: bool = False
) -> ToolSpec:
    return ToolSpec(name, permission, tuple(operations.split()), write, delete)


TOOLS = (
    tool("whoami", "", "introspectAgentToken"),
    tool(
        "logs_search",
        "agent:log:view",
        "listAgentCalls getAgentCall listTranslationLogs getTranslationLog "
        "listContentRevisions getContentRevision listTrash getTrashItem",
    ),
    tool(
        "revision_restore",
        "content:revision:restore",
        "restoreRevision revertRequest restoreTrash",
        write=True,
    ),
    tool("packages_search", "catalog:package:view", "listAdminPackages"),
    tool(
        "package_get",
        "catalog:package:view",
        "getAdminPackage listAdminPackages listPackageScreenshots getTranslationStatus "
        "getPackageFacts",
    ),
    tool("package_versions", "catalog:package:view", "listPackageVersions listAdminPackages"),
    tool("listing_gaps", "catalog:package:view", "listAdminPackages"),
    tool(
        "package_update_meta",
        "catalog:package:edit",
        "updatePackageMeta listAdminPackages",
        write=True,
    ),
    tool(
        "package_set_text",
        "catalog:package:edit",
        "updatePackageText listAdminPackages",
        write=True,
    ),
    tool(
        "package_set_categories",
        "catalog:package:edit",
        "setPackageCategories listAdminPackages",
        write=True,
    ),
    tool(
        "package_set_price",
        "catalog:package:edit",
        "setPackagePrice listAdminPackages",
        write=True,
    ),
    tool(
        "package_set_macos_support",
        "catalog:package:edit",
        "setPackageMacosSupport listAdminPackages",
        write=True,
    ),
    tool(
        "package_set_alternatives",
        "catalog:package:edit",
        "setPackageAlternatives listAdminPackages",
        write=True,
    ),
    tool(
        "package_refresh", "catalog:package:resync", "resyncPackage listAdminPackages", write=True
    ),
    tool("tap_packages_list", "catalog:package:view", "listTapPackages"),
    tool("tap_package_add", "catalog:package:edit", "addTapPackage", write=True),
    tool(
        "tap_package_remove",
        "catalog:package:edit",
        "removeTapPackage listAdminPackages",
        write=True,
        delete=True,
    ),
    tool("asset_upload_from_url", "catalog:asset:upload", "uploadAsset", write=True),
    tool("asset_upload_ticket", "catalog:asset:upload", "createAssetUploadTicket", write=True),
    tool("assets_list", "catalog:asset:upload", "listAssets"),
    tool(
        "package_set_icon",
        "catalog:asset:upload",
        "setPackageIcon deletePackageIcon listAdminPackages",
        write=True,
    ),
    tool(
        "package_screenshot_add",
        "catalog:asset:upload",
        "addPackageScreenshot listAdminPackages",
        write=True,
    ),
    tool(
        "package_screenshot_update",
        "catalog:asset:upload",
        "updatePackageScreenshot listAdminPackages",
        write=True,
    ),
    tool(
        "package_screenshot_delete",
        "catalog:asset:upload",
        "deletePackageScreenshot listAdminPackages",
        write=True,
        delete=True,
    ),
    tool(
        "package_screenshots_reorder",
        "catalog:asset:upload",
        "reorderPackageScreenshots listAdminPackages",
        write=True,
    ),
    tool("categories_tree", "catalog:package:view", "getCategoryTree"),
    tool("category_create", "catalog:category:edit", "createCategory", write=True),
    tool("category_update", "catalog:category:edit", "updateCategory getCategoryTree", write=True),
    tool("categories_reorder", "catalog:category:edit", "reorderCategories", write=True),
    tool("category_delete", "catalog:category:edit", "deleteCategory", write=True, delete=True),
    tool("collections_list", "content:collection:edit", "listAdminCollections"),
    tool("collection_get", "content:collection:edit", "getAdminCollection"),
    tool("collection_create", "content:collection:edit", "createCollection", write=True),
    tool(
        "collection_update",
        "content:collection:edit",
        "updateCollection getAdminCollection",
        write=True,
    ),
    tool("collection_set_items", "content:collection:edit", "setCollectionItems", write=True),
    tool(
        "collection_publish",
        "content:collection:publish",
        "publishCollection unpublishCollection",
        write=True,
    ),
    tool(
        "collection_delete", "content:collection:edit", "deleteCollection", write=True, delete=True
    ),
    tool("articles_list", "content:article:edit", "listAdminArticles"),
    tool("article_get", "content:article:edit", "getAdminArticle"),
    tool("article_create", "content:article:edit", "createArticle", write=True),
    tool("article_update", "content:article:edit", "updateArticle", write=True),
    tool("article_set_packages", "content:article:edit", "setArticlePackages", write=True),
    tool("article_submit_review", "content:article:edit", "submitArticleReview", write=True),
    tool("article_publish", "content:article:publish", "publishArticle", write=True),
    tool("article_withdraw", "content:article:publish", "withdrawArticle", write=True),
    tool("article_restore", "content:article:publish", "restoreArticle", write=True),
    tool("article_revert", "content:article:edit", "revertArticle", write=True),
    tool("article_set_slug", "content:article:publish", "setArticleSlug", write=True),
    tool("article_set_locale", "content:article:publish", "setArticleLocale", write=True),
    tool("article_authors_list", "content:article:edit", "listArticleAuthors"),
    tool("article_author_create", "content:article:publish", "createArticleAuthor", write=True),
    tool("article_author_update", "content:article:publish", "updateArticleAuthor", write=True),
    tool("features_list", "content:feature:edit", "listFeatures getFeature"),
    tool(
        "feature_upsert",
        "content:feature:edit",
        "createFeature updateFeature getFeature",
        write=True,
    ),
    tool("feature_delete", "content:feature:edit", "deleteFeature", write=True, delete=True),
    tool("glossary_list", "content:glossary:edit", "listGlossary"),
    tool("glossary_upsert", "content:glossary:edit", "upsertGlossaryTerm", write=True),
    tool("glossary_delete", "content:glossary:edit", "deleteGlossaryTerm", write=True, delete=True),
    tool(
        "releases_list",
        "catalog:package:view",
        "listAdminVersions listAdminReleases getAdminRelease",
    ),
    tool("release_get", "catalog:package:view", "getAdminRelease"),
    tool(
        "release_write_notes",
        "changelog:release:edit",
        "upsertReleaseNotes listAdminPackages",
        write=True,
    ),
    tool("release_update", "changelog:release:edit", "updateRelease", write=True),
    tool("translation_list", "translation:review", "listTranslations"),
    tool("translation_status", "translation:review", "getTranslationStatus"),
    tool("translation_retranslate", "translation:review", "retranslateContent", write=True),
    tool("translation_fix", "translation:review", "fixTranslation", write=True),
    tool("jobs_runs", "ops:job:view", "listJobRuns getJobRun"),
    tool("queues_status", "ops:job:view", "listQueues"),
    tool("job_trigger", "ops:job:trigger", "triggerJob", write=True),
    tool("search_insights", "ops:search:view", "listTopQueries listZeroResultQueries"),
    tool("synonyms_list", "ops:search:view", "listSynonyms"),
    tool("synonym_upsert", "ops:search:edit", "createSynonym updateSynonym", write=True),
    tool("synonym_delete", "ops:search:edit", "deleteSynonym", write=True, delete=True),
    tool("feedback_list", "ops:feedback:handle", "listFeedback"),
    tool("feedback_get", "ops:feedback:handle", "getFeedback"),
    tool("feedback_update", "ops:feedback:handle", "updateFeedback", write=True),
    tool("catalog_changes", "catalog:package:view", "listAdminCatalogChanges"),
    tool("dashboard_overview", "dashboard:view", "getDashboardOverview getLlmUsage"),
    tool("announcement_get", "content:announcement:edit", "getAnnouncement"),
    tool("announcement_set", "content:announcement:edit", "updateAnnouncement", write=True),
    tool("desktop_releases_list", "release:desktop:notes", "listDesktopReleases getDesktopRelease"),
    tool(
        "desktop_release_write_notes",
        "release:desktop:notes",
        "updateDesktopReleaseNotes",
        write=True,
    ),
)
BY_NAME = {item.name: item for item in TOOLS}

RESOURCE_PERMISSIONS = {
    "opennavo://guide/daily-routine": "",
    "opennavo://guide/content-style": "",
    "opennavo://guide/app-intro": "",
    "opennavo://guide/release-notes": "",
    "opennavo://guide/article-writing": "",
    "opennavo://meta/locales": "",
    "opennavo://catalog/categories": "catalog:package:view",
    "opennavo://meta/policy": "",
}
