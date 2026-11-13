-- Run only by seed-demo in opennavo-dev; preserve existing content and admin edits.
BEGIN;
SELECT pg_advisory_xact_lock(hashtext('opennavo:demo-content'));

CREATE TEMP TABLE demo_collections (
  slug TEXT PRIMARY KEY, sort INTEGER,
  title_zh TEXT, title_en TEXT, subtitle_zh TEXT, subtitle_en TEXT,
  body_zh TEXT, body_en TEXT
) ON COMMIT DROP;
INSERT INTO demo_collections VALUES
  ('demo-new-mac', 10, '新 Mac 必备', 'New Mac essentials',
   '从浏览、影音到窗口管理，装好日常所需。', 'Everyday apps for browsing, media and window management.',
   '刚换新 Mac？先从这六款常用应用开始。\n\n浏览网页、播放视频、管理窗口、解压文件、保存密码和记录想法，各选一款。可以逐个查看，也可以导出 Brewfile 后在客户端安装。',
   'Start your new Mac with six everyday apps.\n\nBrowse the web, play videos, arrange windows, extract archives, keep passwords and take notes. Explore each app or export a Brewfile for the desktop client.'),
  ('demo-developer-toolkit', 20, '开发者工具箱', 'Developer toolkit',
   '编辑器、终端与命令行，让开发流程更顺手。', 'An editor, a terminal and CLI tools for your daily workflow.',
   '从编写代码到查找文件，搭建一套轻巧的开发环境。\n\nVisual Studio Code 与 Ghostty 负责编辑和终端；Git 管理版本；ripgrep、fd、fzf、jq 与 bat 补齐搜索、筛选和文本处理。合集同时包含 App 和命令行工具。',
   'Build a practical workflow for writing code and finding files.\n\nUse Visual Studio Code and Ghostty for editing and terminal sessions, Git for version control, and ripgrep, fd, fzf, jq and bat for search and text processing. Includes both apps and CLI tools.'),
  ('demo-focus-productivity', 30, '效率与专注', 'Focus and productivity',
   '减少切换，把时间留给重要的事。', 'Spend less time switching and more time doing.',
   '为日常工作选一组容易上手的工具。\n\n用 Raycast 快速启动，用 Rectangle 整理窗口，用 Obsidian 和 Notion 组织知识与项目，再用 Bitwarden 管理账号。按自己的习惯挑选即可。',
   'Choose tools that fit your working habits.\n\nLaunch with Raycast, arrange windows with Rectangle, organize knowledge and projects with Obsidian and Notion, and keep credentials in Bitwarden. Pick only what you need.');

CREATE TEMP TABLE demo_items (
  slug TEXT, kind TEXT, token TEXT, sort INTEGER, note_zh TEXT, note_en TEXT
) ON COMMIT DROP;
INSERT INTO demo_items VALUES
  ('demo-new-mac', 'cask', 'firefox', 10, '日常浏览，兼顾隐私与扩展。', 'An everyday browser with privacy controls and extensions.'),
  ('demo-new-mac', 'cask', 'iina', 20, '适合 macOS 的本地视频播放器。', 'A video player designed for macOS.'),
  ('demo-new-mac', 'cask', 'rectangle', 30, '用快捷键快速摆放窗口。', 'Arrange windows with keyboard shortcuts.'),
  ('demo-new-mac', 'cask', 'the-unarchiver', 40, '轻松打开常见压缩文件。', 'Extract common archive formats.'),
  ('demo-new-mac', 'cask', 'bitwarden', 50, '集中管理密码与账号。', 'Keep passwords and accounts organized.'),
  ('demo-new-mac', 'cask', 'obsidian', 60, '用本地 Markdown 建立个人笔记库。', 'Build a personal library of local Markdown notes.'),
  ('demo-developer-toolkit', 'cask', 'visual-studio-code', 10, '从编辑代码到调试，一处完成。', 'Edit and debug code in one place.'),
  ('demo-developer-toolkit', 'cask', 'ghostty', 20, '原生终端，适合日常命令行工作。', 'A native terminal for everyday command-line work.'),
  ('demo-developer-toolkit', 'formula', 'git', 30, '记录代码变化，协作管理版本。', 'Track changes and collaborate with version control.'),
  ('demo-developer-toolkit', 'formula', 'ripgrep', 40, '快速搜索项目中的文本。', 'Search text across your projects.'),
  ('demo-developer-toolkit', 'formula', 'fd', 50, '用简洁的命令查找文件。', 'Find files with a concise command.'),
  ('demo-developer-toolkit', 'formula', 'fzf', 60, '交互式筛选文件与命令历史。', 'Interactively filter files and command history.'),
  ('demo-developer-toolkit', 'formula', 'jq', 70, '在终端里筛选和转换 JSON。', 'Filter and transform JSON in the terminal.'),
  ('demo-developer-toolkit', 'formula', 'bat', 80, '带语法高亮的文件预览。', 'Preview files with syntax highlighting.'),
  ('demo-focus-productivity', 'cask', 'raycast', 10, '快速启动应用与常用操作。', 'Quickly launch apps and common actions.'),
  ('demo-focus-productivity', 'cask', 'rectangle', 20, '为多窗口工作整理桌面。', 'Arrange your desktop for multi-window work.'),
  ('demo-focus-productivity', 'cask', 'obsidian', 30, '把零散想法连成知识网络。', 'Connect ideas in a personal knowledge base.'),
  ('demo-focus-productivity', 'cask', 'notion', 40, '一起整理文档、任务和项目。', 'Organize documents, tasks and projects together.'),
  ('demo-focus-productivity', 'cask', 'bitwarden', 50, '减少找密码的时间。', 'Spend less time looking for passwords.');

-- Roll back the entire batch if packages are missing or invisible, avoiding incomplete collections.
DO $$
BEGIN
  IF EXISTS (
    SELECT 1 FROM demo_items d
    LEFT JOIN packages p ON p.kind=d.kind AND p.token=d.token
    LEFT JOIN package_meta m ON m.package_id=p.id
    WHERE p.id IS NULL OR p.removed_at IS NOT NULL OR p.disabled OR COALESCE(m.hidden,false)
  ) THEN
    RAISE EXCEPTION 'Demo packages missing or unavailable; sync the Homebrew catalog first';
  END IF;
END $$;

CREATE TEMP TABLE demo_new_collections ON COMMIT DROP AS
WITH inserted AS (
  INSERT INTO collections (slug, status, sort, source_locale)
  SELECT slug, 'published', sort, 'zh-CN' FROM demo_collections
  ON CONFLICT (slug) DO NOTHING
  RETURNING id, slug
) SELECT * FROM inserted;

INSERT INTO collection_i18n (collection_id, locale, source_locale, title, subtitle, body)
SELECT c.id, l.locale, 'zh-CN',
  CASE WHEN l.locale='zh-CN' THEN d.title_zh ELSE d.title_en END,
  CASE WHEN l.locale='zh-CN' THEN d.subtitle_zh ELSE d.subtitle_en END,
  replace(CASE WHEN l.locale='zh-CN' THEN d.body_zh ELSE d.body_en END, '\n', chr(10))
FROM demo_new_collections c JOIN demo_collections d USING (slug)
CROSS JOIN (VALUES ('zh-CN'), ('en-US')) l(locale);

INSERT INTO collection_items (collection_id, package_id, sort, source_locale)
SELECT c.id, p.id, d.sort, 'zh-CN'
FROM demo_new_collections c JOIN demo_items d USING (slug)
JOIN packages p ON p.kind=d.kind AND p.token=d.token;
INSERT INTO collection_item_i18n(collection_id,package_id,locale,note,status,source_locale)
SELECT c.id,p.id,l.locale,CASE WHEN l.locale='zh-CN' THEN d.note_zh ELSE d.note_en END,
CASE WHEN l.locale='zh-CN' THEN 'source' ELSE 'manual' END,'zh-CN'
FROM demo_new_collections c JOIN demo_items d USING(slug)
JOIN packages p ON p.kind=d.kind AND p.token=d.token
CROSS JOIN (VALUES ('zh-CN'),('en-US')) l(locale);


CREATE TEMP TABLE demo_features (
  placement TEXT, target_type TEXT, package_id BIGINT, collection_id BIGINT, sort INTEGER,
  title_zh TEXT, title_en TEXT, subtitle_zh TEXT, subtitle_en TEXT,
  body_zh TEXT, body_en TEXT, cta_zh TEXT, cta_en TEXT
) ON COMMIT DROP;
INSERT INTO demo_features VALUES
  ('home_hero', 'package', (SELECT id FROM packages WHERE kind='cask' AND token='visual-studio-code'), NULL, 10,
   'Visual Studio Code', 'Visual Studio Code', '把想法写成代码。', 'Turn ideas into code.',
   '从轻量编辑到项目调试，用 **Visual Studio Code** 开始你的开发之旅。',
   'Start coding with **Visual Studio Code**, from quick edits to debugging your next project.', '查看详情', 'Explore'),
  ('home_secondary', 'package', (SELECT id FROM packages WHERE kind='cask' AND token='ghostty'), NULL, 20,
   'Ghostty', 'Ghostty', '终端，本该这么顺手。', 'A terminal that feels at home.',
   '用 **Ghostty** 搭配顺手的命令行工具，让日常开发更流畅。',
   'Pair **Ghostty** with useful CLI tools for a comfortable development workflow.', '查看详情', 'Explore'),
  ('home_secondary', 'collection', NULL, (SELECT id FROM collections WHERE slug='demo-new-mac'), 30,
   '新 Mac，从这里开始', 'Start with your new Mac', '日常所需，一次选好。', 'Choose your everyday essentials.',
   '浏览、影音、窗口管理和笔记，**新 Mac 必备** 为你整理六款常用应用。',
   'Explore six everyday apps for browsing, media, windows and notes in **New Mac essentials**.', '查看合集', 'View picks');

-- Deduplicate by position and target; write copy only for newly created features and preserve editorial changes.
CREATE TEMP TABLE demo_new_features ON COMMIT DROP AS
WITH inserted AS (
  INSERT INTO features (placement, target_type, package_id, collection_id, status, sort, source_locale)
  SELECT d.placement, d.target_type, d.package_id, d.collection_id, 'published', d.sort, 'zh-CN'
  FROM demo_features d
  WHERE NOT EXISTS (
    SELECT 1 FROM features f WHERE f.placement=d.placement AND f.target_type=d.target_type
    AND f.package_id IS NOT DISTINCT FROM d.package_id
    AND f.collection_id IS NOT DISTINCT FROM d.collection_id
  )
  RETURNING id, placement, target_type, package_id, collection_id
) SELECT * FROM inserted;

INSERT INTO feature_i18n (feature_id, locale, source_locale, badge, title, subtitle, body, cta_label)
SELECT f.id, l.locale, 'zh-CN',
  CASE WHEN l.locale='zh-CN' THEN '演示精选' ELSE 'Demo pick' END,
  CASE WHEN l.locale='zh-CN' THEN d.title_zh ELSE d.title_en END,
  CASE WHEN l.locale='zh-CN' THEN d.subtitle_zh ELSE d.subtitle_en END,
  CASE WHEN l.locale='zh-CN' THEN d.body_zh ELSE d.body_en END,
  CASE WHEN l.locale='zh-CN' THEN d.cta_zh ELSE d.cta_en END
FROM demo_new_features f JOIN demo_features d ON f.placement=d.placement AND f.target_type=d.target_type
  AND f.package_id IS NOT DISTINCT FROM d.package_id
  AND f.collection_id IS NOT DISTINCT FROM d.collection_id
CROSS JOIN (VALUES ('zh-CN'), ('en-US')) l(locale);

SELECT (SELECT count(*) FROM demo_new_collections) AS added_collections,
       (SELECT count(*) FROM demo_new_features) AS added_features;

-- Developer map demo data: per-App locations and Discover developer picks.
-- City text is what the map renders, so it is the English name here; real submissions keep the
-- city the editor or developer typed. The picks carry placeholder copy, never words attributed to
-- the real teams behind these Apps.
INSERT INTO package_locations (package_id, country_code, city, city_key, region, lat, lng, source, status, confidence, verified_at)
SELECT p.id, v.country_code, v.city, v.city_key, v.region, v.lat, v.lng, 'admin', 'approved', 'verified', now()
FROM (VALUES
  ('visual-studio-code', 'US', 'Seattle', 'seattle', 'americas', 47.6062, -122.3321),
  ('ghostty', 'US', 'San Francisco', 'bay-area', 'americas', 37.7749, -122.4194),
  ('ollama', 'US', 'San Francisco', 'bay-area', 'americas', 37.7749, -122.4194),
  ('codex', 'US', 'San Francisco', 'bay-area', 'americas', 37.7749, -122.4194),
  ('maestri', 'US', 'San Francisco', 'bay-area', 'americas', 37.7749, -122.4194),
  ('masscode', 'DE', 'Berlin', 'berlin', 'europe', 52.52, 13.405)
) AS v(token, country_code, city, city_key, region, lat, lng)
JOIN packages p ON p.kind = 'cask' AND p.token = v.token
ON CONFLICT (package_id) DO NOTHING;

INSERT INTO developer_picks (package_id, quote, sort, active, published_at)
SELECT p.id, v.quote, v.sort, TRUE, now()
FROM (VALUES
  ('ghostty', 'Demo pick: replace this placeholder with a quote the team approved.', 10),
  ('visual-studio-code', 'Demo pick: replace this placeholder with a quote the team approved.', 20),
  ('ollama', 'Demo pick: replace this placeholder with a quote the team approved.', 30)
) AS v(token, quote, sort)
JOIN packages p ON p.kind = 'cask' AND p.token = v.token
ON CONFLICT (package_id) DO NOTHING;

COMMIT;
