import jaJP from './messages/ja-JP.json';
import esES from './messages/es-ES.json';
import ptBR from './messages/pt-BR.json';
import ruRU from './messages/ru-RU.json';
import deDE from './messages/de-DE.json';
import frFR from './messages/fr-FR.json';
import itIT from './messages/it-IT.json';
import zhHant from './messages/zh-Hant.json';
// Shared base copy (08 §12.2, §12.5), merged into each app's resources; also supplies @opennavo/ui defaults.
import type { SiteLocale } from './types';
import { selectPlural } from './plural';

const zhCN = {
  getButton: {
    get: '获取',
    open: '打开',
    installed: '已安装',
    update: '更新',
    queued: '排队中',
    running: '正在处理',
    cancel: '取消',
    unavailable: '不可用',
    download: '下载'
  },
  action: {
    checkUpdates: '检查更新',
    updateTo: '更新到 {version}',
    updateAll: '全部更新（{count}）',
    retry: '重试',
    viewLog: '查看日志',
    cancel: '取消',
    openInApp: '在 OpenNavo 中打开',
    copyInstallCommand: '复制安装命令',
    addToBrewfile: '加入 Brewfile 清单',
    inBrewfile: '已在清单中',
    uninstall: '卸载',
    delete: '删除',
    copy: '复制',
    copied: '已复制',
    clear: '清除'
  },
  kind: {
    cask: 'App',
    formula: '命令行工具',
    link: 'App'
  },
  card: {
    installs30d: '30 天安装',
    version: '版本',
    category: '分类'
  },
  nav: {
    breadcrumb: '面包屑',
    pagination: '分页',
    previous: '上一页',
    next: '下一页',
    page: '第 {page} 页',
    loadMore: '加载更多',
    categories: '分类筛选'
  },
  installCommand: {
    title: '安装命令'
  },
  cask: {
    version: '可安装版本',
    unknown: '尚未确认',
    noMac: '未声明支持 macOS',
    rosetta: '需要 Rosetta',
    legacy: '部分平台提供旧版本',
    declarations: '安装与卸载内容',
    install: '安装',
    uninstall: '卸载',
    cleanup: '清理用户数据',
    source: '来源文件',
    target: '声明的目标位置',
    declaration: '查看完整声明',
    download: '下载地址'
  },
  hero: {
    viewDetails: '查看详情'
  },
  about: {
    title: '介绍',
    machineTranslated: 'AI 翻译',
    viewOriginal: '查看原文',
    expand: '展开全文',
    collapse: '收起'
  },
  gallery: {
    label: '截图',
    item: '截图 {index}',
    dialog: '截图预览',
    position: '{index} / {total}',
    previous: '上一张',
    next: '下一张',
    close: '关闭'
  },
  dialog: {
    close: '关闭',
    confirm: '确认',
    cancel: '取消'
  },
  toast: {
    region: '通知',
    view: '查看',
    close: '关闭通知'
  },
  menu: {
    more: '更多操作'
  },
  palette: {
    label: '命令面板',
    placeholder: '搜索 App 或操作',
    results: '搜索结果',
    empty: '没有匹配的结果',
    loading: '正在搜索…',
    hintSelect: '↑↓ 选择',
    hintOpen: '↵ 打开',
    hintInstall: '⌘↵ 安装 / 更新',
    hintCopy: '⌘C 复制 brew 命令'
  },
  collection: {
    viewAll: '查看全部 {count} 款'
  },
  rank: {
    up: '上升 {n} 位',
    down: '下降 {n} 位',
    same: '排名不变'
  },
  progress: {
    busy: '进行中'
  },
  task: {
    queued: '排队中：',
    expandLog: '展开日志',
    collapseLog: '收起日志'
  },
  log: {
    label: '日志'
  },
  chart: {
    other: '其他',
    item: '项目',
    value: '数值',
    share: '占比'
  },
  table: {
    sortBy: '按{label}排序'
  },
  topNav: {
    label: '主导航',
    home: 'OpenNavo 首页',
    search: '搜索 App',
    download: '下载客户端',
    openMenu: '打开菜单',
    closeMenu: '关闭菜单'
  },
  version: {
    timeline: '版本记录',
    latest: '最新',
    installed: '当前安装',
    patch: '补丁',
    prerelease: '预发布',
    machineTranslation: 'AI 翻译',
    noNotes: '此版本没有单独的更新说明',
    viewOriginal: '查看原文',
    expand: '展开',
    collapse: '收起',
    updateTo: '更新到此版本',
    showEarlier: '显示更早的 {count} 个版本',
    sources: {
      webpage: '官网更新页',
      sparkle: 'Sparkle 更新源',
      github_release: 'GitHub Release',
      manual: '人工整理',
      editorial: 'OpenNavo 编辑',
      homebrew: 'Homebrew 收录'
    }
  },
  state: {
    loadFailedTitle: '加载失败',
    loadFailedDescription: '网络或服务暂时不可用，稍后重试。',
    noResultsTitle: '没有找到「{q}」',
    noResultsDescription: '试试英文名或拼音，或者告诉我们想收录什么。',
    offlineTitle: '已离线',
    offlineDescription: '正在显示本地缓存的目录，联网后自动更新。'
  },
  // The website's language suggestion banner, shown in the suggested language before its messages load (X3).
  languageSuggestion: {
    message: '是否使用{language}浏览？',
    switch: '切换到{language}',
    dismiss: '不再提示'
  }
};

export type SharedMessages = typeof zhCN;

const enUS: SharedMessages = {
  getButton: {
    get: 'Get',
    open: 'Open',
    installed: 'Installed',
    update: 'Update',
    queued: 'Queued',
    running: 'Working',
    cancel: 'Cancel',
    unavailable: 'Unavailable',
    download: 'Download'
  },
  action: {
    checkUpdates: 'Check for Updates',
    updateTo: 'Update to {version}',
    updateAll: 'Update All ({count}) | Update All ({count})',
    retry: 'Retry',
    viewLog: 'View Log',
    cancel: 'Cancel',
    openInApp: 'Open in OpenNavo',
    copyInstallCommand: 'Copy Install Command',
    addToBrewfile: 'Add to Brewfile',
    inBrewfile: 'In Brewfile',
    uninstall: 'Uninstall',
    delete: 'Delete',
    copy: 'Copy',
    copied: 'Copied',
    clear: 'Clear'
  },
  kind: {
    cask: 'App',
    formula: 'Command-line tool',
    link: 'App'
  },
  card: {
    installs30d: '30-day installs',
    version: 'Version',
    category: 'Category'
  },
  nav: {
    breadcrumb: 'Breadcrumb',
    pagination: 'Pagination',
    previous: 'Previous',
    next: 'Next',
    page: 'Page {page}',
    loadMore: 'Load more',
    categories: 'Category filter'
  },
  installCommand: {
    title: 'Install command'
  },
  cask: {
    version: 'Available version',
    unknown: 'Not confirmed',
    noMac: 'No declared macOS support',
    rosetta: 'Requires Rosetta',
    legacy: 'Older versions available on some platforms',
    declarations: 'Installation and removal contents',
    install: 'Install',
    uninstall: 'Uninstall',
    cleanup: 'Remove user data',
    source: 'Source file',
    target: 'Declared destination',
    declaration: 'View full declaration',
    download: 'Download URL'
  },
  hero: {
    viewDetails: 'View Details'
  },
  about: {
    title: 'About',
    machineTranslated: 'AI translated',
    viewOriginal: 'View original',
    expand: 'Read more',
    collapse: 'Show less'
  },
  gallery: {
    label: 'Screenshots',
    item: 'Screenshot {index}',
    dialog: 'Screenshot viewer',
    position: '{index} of {total}',
    previous: 'Previous screenshot',
    next: 'Next screenshot',
    close: 'Close'
  },
  dialog: {
    close: 'Close',
    confirm: 'Confirm',
    cancel: 'Cancel'
  },
  toast: {
    region: 'Notifications',
    view: 'View',
    close: 'Dismiss notification'
  },
  menu: {
    more: 'More actions'
  },
  palette: {
    label: 'Command palette',
    placeholder: 'Search apps or actions',
    results: 'Results',
    empty: 'No matching results',
    loading: 'Searching…',
    hintSelect: '↑↓ Select',
    hintOpen: '↵ Open',
    hintInstall: '⌘↵ Install / Update',
    hintCopy: '⌘C Copy brew command'
  },
  collection: {
    viewAll: 'View all {count} | View all {count}'
  },
  rank: {
    up: 'Up {n} | Up {n}',
    down: 'Down {n} | Down {n}',
    same: 'No change'
  },
  progress: {
    busy: 'In progress'
  },
  task: {
    queued: 'Queued:',
    expandLog: 'Show log',
    collapseLog: 'Hide log'
  },
  log: {
    label: 'Log'
  },
  chart: {
    other: 'Other',
    item: 'Item',
    value: 'Value',
    share: 'Share'
  },
  table: {
    sortBy: 'Sort by {label}'
  },
  topNav: {
    label: 'Main',
    home: 'OpenNavo home',
    search: 'Search apps',
    download: 'Download',
    openMenu: 'Open menu',
    closeMenu: 'Close menu'
  },
  version: {
    timeline: 'Release history',
    latest: 'Latest',
    installed: 'Installed',
    patch: 'Patch',
    prerelease: 'Pre-release',
    machineTranslation: 'AI translated',
    noNotes: 'No separate release notes for this version',
    viewOriginal: 'View original',
    expand: 'Expand',
    collapse: 'Collapse',
    updateTo: 'Update to this version',
    showEarlier: 'Show {count} earlier version | Show {count} earlier versions',
    sources: {
      webpage: 'Release notes page',
      sparkle: 'Sparkle feed',
      github_release: 'GitHub Release',
      manual: 'Curated',
      editorial: 'OpenNavo editorial',
      homebrew: 'Added to Homebrew'
    }
  },
  state: {
    loadFailedTitle: 'Couldn’t load',
    loadFailedDescription: 'The network or service is temporarily unavailable. Try again later.',
    noResultsTitle: 'No results for “{q}”',
    noResultsDescription: 'Try the English name, or tell us what you’d like to see listed.',
    offlineTitle: 'Offline',
    offlineDescription: 'Showing the cached catalog. It updates automatically when you’re back online.'
  },
  languageSuggestion: {
    message: 'Would you like to browse in {language}?',
    switch: 'Switch to {language}',
    dismiss: 'Don’t suggest again'
  }
};

/** Every site locale: the website renders shared components in all ten, the apps in their six. */
export const sharedMessages: Record<SiteLocale, SharedMessages> = {
  'zh-CN': zhCN,
  'en-US': enUS,
  'ja-JP': jaJP,
  'es-ES': esES,
  'pt-BR': ptBR,
  'ru-RU': ruRU,
  'de-DE': deDE,
  'fr-FR': frFR,
  'it-IT': itIT,
  'zh-Hant': zhHant
};

/** Replace {name} placeholders; retain missing parameters verbatim to expose omissions. */
export function interpolate(template: string, params: Record<string, string | number> = {}): string {
  return template.replace(/\{(\w+)\}/g, (placeholder, key: string) =>
    key in params ? String(params[key]) : placeholder
  );
}

/** Shared components use the same plural rules as vue-i18n. */
export function interpolatePlural(
  template: string,
  params: Record<string, string | number>,
  count: number,
  locale: SiteLocale
): string {
  return interpolate(selectPlural(template, count, locale), params);
}
