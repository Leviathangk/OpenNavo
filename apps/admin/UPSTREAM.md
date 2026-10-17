# Upstream provenance

| Item | Value |
|---|---|
| Repository | https://github.com/soybeanjs/soybean-admin |
| Tag | `v2.2.0` (annotated tag object `8c111df85c39ae691eaf8fc71c4a1d05f76dfa6f`) |
| Commit | `12f14672c4406f7b9d73be26b863d0dfed1edacc` (2026-05-13, chore(projects): release v2.2.0) |
| Unmodified verification | 2026-10-04, pnpm 10.13.1: `pnpm install && pnpm build && pnpm typecheck` all passed in a temporary directory |

Do not import main: it is a v3 draft based on ubean + Hono, incompatible with this Go backend. For soybean upgrades, import a new tag and replay the changes below.

## License

The upstream template stays under its MIT license: [LICENSE](LICENSE) (Copyright (c) 2021 Soybean) is kept verbatim and must ship with any copy of this app, including `packages/*`, which declare no license of their own. OpenNavo's changes and additions, listed below, are licensed under GPL-3.0-only like the rest of the repository (see the root [LICENSE](../../LICENSE)), so the app as a whole is distributed under GPL-3.0-only. MIT permits this; replaying a new upstream tag keeps both notices.

## Local changes

### Import (07 §1)

1. Removed `.git`, `.github`, `pnpm-lock.yaml`, `pnpm-workspace.yaml`, `.npmrc`; root workspace/lockfile manage dependencies using the default npm registry.
2. Removed upstream `CHANGELOG.md`, `CHANGELOG.zh_CN.md`, `README.md`, `README.en_US.md`, whose history/introduction does not describe this project. Preserved MIT `LICENSE` verbatim.
3. `package.json`: renamed to `@opennavo/admin`, added `private: true`; removed `simple-git-hooks` and `prepare` because root manages hooks. `lint` checks without fixing (`oxlint && eslint .`); retained autofix as `lint:fix`. Shared dependencies use `catalog:` for consistent Vue/tooling: `vue`, `vue-router`, `pinia`, `@vueuse/core`, `@iconify/vue`, `typescript`, `vue-tsc`, `vite`, `@vitejs/plugin-vue`, `unocss`, `eslint`, `oxlint`, `oxfmt`, `@soybeanjs/eslint-config-vue`, `@types/node`. Root `pnpm-workspace.yaml` specifies ranges, retaining upstream majors.
4. Root workspace includes `apps/admin` and `apps/admin/packages/*`, with upstream `shamefullyHoist: true` / `linkWorkspacePackages: true`; builds denied in upstream `allowBuilds` become pnpm 10 `ignoredBuiltDependencies`.

### Environment (07 §2)

5. `.env`: OpenNavo title/description, `VITE_ROUTE_HOME=dashboard`, `VITE_MENU_ICON=lucide:menu`, expired code only `9999` (not `9998`/`3333`), `VITE_STORAGE_PREFIX=ONV_ADMIN_`. Static routes, success/logout/modal-logout codes, and super role retain upstream defaults.
6. `.env.test` targets `http://localhost:8080/admin-api`; `.env.prod` uses same-origin `/admin-api` through Caddy, avoiding a build-time placeholder domain. Both use an empty `VITE_OTHER_SERVICE_BASE_URL` object.
7. Added `dev:mock` for Prism admin mock on 4011, invoked from root as `MOCK=1 make dev-admin`.

### Requests (07 §3)

8. Removed `apifoxToken` and `demoRequest` from `src/service/request/index.ts`; retained upstream success/logout/modal-logout/token-refresh behavior.
9. Set `OtherBaseURLKey` to `never` and removed `DemoResponse` in `src/typings/app.d.ts`; added an empty-service-map assertion in `src/utils/service.ts`, retaining upstream structure.

### Theme and brand (07 §4)

10. `src/theme/settings.ts`: dark by default; primary/status/dark-layout colors from `@opennavo/tokens/naive-theme` `adminColors`; `themeRadius: 8`, `isInfoFollowPrimary: false`, OpenNavo watermark.
11. Theme store switches unchanged brand coral to `adminColors.primaryLight` in light mode (white text contrast 4.6). Merge token `naiveDarkOverrides` / `naiveLightOverrides` first, then presets: dark primary-button text `#1B0A05`, dark status-button text, surfaces, text colors, fonts.
12. Assets import `@fontsource-variable/inter/opsz.css` and `@opennavo/tokens/tokens.css`; global CSS uses the token font stack and dark html surface to prevent loading flashes.
13. Added `presetOpenNavo()` token utilities to `uno.config.ts`.
14. Replaced logos in `system-logo.vue`, loading plugin, SVG asset, favicon with OpenNavo N + Polaris from shared `LOGO` geometry/gradients. Polaris is warm white on dark, amber on light. Loading honors persisted theme, default dark. `SystemLogo inline` centers N with titles in expanded sidebar/login.
15. Footer is `© {year} OpenNavo`; watermark placeholder is OpenNavo. Removed soybean-avatar.vue / soybean.jpg.

### Login (07 §5)

16. Retained only pwd-login; removed code login, registration, reset-password, WeChat binding; narrowed `UnionKey.LoginModule`, `loginModuleRecord`, and router plugin module lists.
17. Required-field validation only, without upstream 6–18-character password regex; backend owns password rules. Removed default credentials/quick-login buttons. Forgot password advises contacting superadmin. Applied dark brand styling with token glow, logo, subtitle.

### Routes and pages

18. Removed upstream home/copy; added dashboard placeholder (implemented in M1-13), regenerated elegant-router, and set dashboard icon `lucide:layout-dashboard`, order 1.
19. Added Chinese/English system title, dashboard route/placeholder, login subtitle/buttons, forgot-password copy; synchronized App.I18n.Schema.

### Route/menu skeleton (M1-12)

20. Added all 07 §6 views under `src/views/{catalog,content,changelog,ops,release,system,user-center}` using page-placeholder.vue; augmented generated routes with icon/order/roles/hideInMenu/activeMenu.
21. Added permission-gate.vue and Profile to the user-avatar menu.
22. Added Playwright configuration/e2e menu/route permission cases with intercepted APIs, plus e2e script and @playwright/test.

### Workspace dependencies

23. tsconfig paths pins vue-router to this app's node_modules. Peer differences create multiple same-version copies (web Vite/@types/node differ); the hoisted copy depends on the graph. @elegant-router/vue resolves from root and may miss this app's RouteMeta augmentation, making meta unknown in typecheck. Runtime is unaffected because Vite resolves locally.

### Pages (M1-13 onward)

24. Added @opennavo/ui for Markdown/public previews; regenerated component typings through unplugin-vue-components.
25. OpenNavo page messages live in `src/locales/langs/pages/`, spread into each main message file. App.I18n.Schema page derives from pages/zh-cn.ts, replacing dashboard placeholder typing. This explicitly localized schema source does not determine runtime or authoring defaults.
26. Added per-module API functions (07 §3), contract-derived QueryOf/BodyOf/DataOf/RecordOf, query/time/tristate helpers, category cache, and package-icon/Markdown/audit/diff components.
27. Playwright development server targets the E2E stack by default; added stack.ts and full-stack cases.
28. Translation-editor regressions use Vitest/Vue Test Utils/jsdom from the root catalog, vitest.config.ts and release-editor.test.ts. Unit tests do not start browsers.

## 2026-10-06 · Six-language program C1

Retained soybean-admin v2.2.0 and dependency majors. Locale initialization uses shared matchLocale with English fallback. Added Japanese, Spanish (Naive esAR), Portuguese, and Russian NaiveUI/date/dayjs mappings. Requests carry UI Accept-Language; HTML lang and route titles follow UI locale. Expanded LangType to the shared six-language type before C2 message/menu integration.

## 2026-10-06 · Six-language program C2

Four additional languages directly import complete i18n-sync-generated JSON, checked against App.I18n.Schema instead of aliasing enUS. Menus use native language names. Six-language screenshots covered login, USD dashboard, package list, locale tabs, collection editor, translation management, system/mirror settings, retaining upstream v2.2.0 and dependency majors.

Long-copy adaptations: GlobalLogo wraps at words while retaining its mark; expanded vertical menus wrap and grow, while collapsed menus retain existing behavior. Sync-card counts use locale grouping, task links wrap as a unit, and numbers do not split. This adaptation did not upgrade upstream/dependencies.

## 2026-10-08 · English defaults and security patches

Repository comments/documentation and default developer descriptions are English. UI fallback and newly authored content default to English; explicit record source locales and multilingual fixtures remain intact. Updated axios, nanoid, echarts, colord, and qs within their existing major versions for security fixes; root workspace owns lockfile/transitive remediation. Upstream license/copyright text remains unchanged.

## 2026-10-16 · GPLv3

The repository moved from Apache-2.0 to GPLv3: `package.json` `license` is now `GPL-3.0-only`, and the upstream MIT `LICENSE` stays unchanged (see License above).
