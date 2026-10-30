export default {
  articles: {
    title: '文章',
    subtitle: 'Mac 软件推荐、评测、教程和合集',
    all: '全部',
    typesLabel: '文章类型',
    types: {
      recommendation: '推荐',
      review: '评测',
      tutorial: '教程',
      roundup: '合集',
      news: '新闻'
    },
    emptyTitle: '还没有文章',
    emptyDescription: '文章发布后会显示在这里。',
    readingTime: '阅读约 {minutes} 分钟',
    published: '{date} 发布',
    updated: '{date} 更新',
    editorial: 'OpenNavo 编辑部',
    sourceText: '这篇文章还没有当前语言的版本，下面显示的是原文。',
    testNote: '测试环境：',
    toc: '目录',
    sources: '参考来源',
    appCardNote: '当前目录数据：版本和安装量随目录更新，可能与撰写时不同。',
    mentionedApps: '文中提到的 App',
    related: '相关文章',
    withdrawnTitle: '这篇文章已撤下',
    withdrawnBody: '文章内容可能已过时或不再准确，因此不再提供。可以看看其他文章。',
    backToList: '浏览全部文章'
  },
  brewfile: {
    installTitle: '安装清单中的软件',
    title: '我的清单',
    description: '软件先加入清单，再统一安装或导出。清单仅保存在本机。',
    count: '{count} 项',
    emptyTitle: '清单是空的',
    emptyDescription: '在软件详情页点击「加入清单」，或从已安装软件的更多菜单中添加。',
    browse: '去发现',
    fileTitle: '由 OpenNavo 生成',
    download: '导出 Brewfile',
    moveUp: '上移 {name}',
    moveDown: '下移 {name}',
    remove: '从清单移除 {name}',
    add: '加入清单',
    inList: '已在清单中',
    removeShort: '从清单移除',
    added: '已加入我的清单',
    full: '清单最多 200 项，请先移除一些软件。',
    saveFailed: '无法保存或读取清单，请检查本机存储后重试。',
    install: '安装 {count} 个软件',
    installHint: '自动跳过已安装、正在安装及不可用的软件。',
    copy: '复制 Brewfile',
    exported: 'Brewfile 已导出'
  },
  app: {
    name: 'OpenNavo'
  },
  nav: {
    browse: '浏览',
    lists: '清单',
    tools: '工具',
    mine: '我的',
    discover: '发现',
    categories: '分类',
    rankings: '排行榜',
    recent: '最近更新',
    installed: '已安装',
    updates: '更新',
    history: '更新记录',
    settings: '设置',
    updatesBadge: '{count} 个可用更新',
    cleanup: '清理电脑',
    cleanupBadge: '{count} 个失效的后台项目仍在加载',
    monitor: '电脑监控',
    security: '扫描病毒',
    securityBadge: '{count} 项需要处理',
    firewall: '应用防火墙',
    firewallBadge: '{count} 个连接等待确认'
  },
  env: {
    label: 'Homebrew',
    checking: '正在检测…',
    checkingHint: '读取本机 Homebrew 环境',
    ready: 'Homebrew 可用',
    noBrew: '未安装 Homebrew',
    noBrewHint: '点击开始安装',
    version: '{version} · {arch}',
    mirrors: {
      official: '官方源',
      tuna: '清华 TUNA 镜像',
      ustc: '中科大 USTC 镜像',
      aliyun: '阿里云镜像'
    }
  },
  toolbar: {
    back: '后退',
    forward: '前进',
    search: '搜索 {count} 个 App',
    searchShort: '搜索 App',
    taskRunning: '{action} {name}',
    taskScanning: '扫描 {name} 安装包',
    taskQueued: '另有 {count} 个排队'
  },
  rail: {
    title: '概览',
    subtitle: '本机 Homebrew 状态',
    installed: '已安装',
    updates: '可更新',
    updatesHint: '下载约 {size}',
    updatesNone: '全部是最新',
    disk: '磁盘占用',
    diskHint: '含缓存 {size}',
    lastCheck: '上次检查',
    lastCheckAuto: '每天 {time} 自动',
    lastCheckManual: '未开启自动检查',
    never: '尚未检查',
    weeklyTop: '本周热门',
    weeklyTopHint: '近 30 天 App 安装量'
  },
  common: {
    empty: '暂无内容',
    refreshFailed: '部分内容加载失败，已保留上次成功加载的内容。',
    notFound: '内容不存在或已下架',
    loadMore: '加载更多',
    viewAll: '查看全部',
    viewRankings: '查看排行榜',
    retry: '重试',
    loading: '正在加载…',
    more: '更多操作',
    copy: '复制',
    copied: '已复制',
    cancel: '取消',
    close: '关闭',
    count: '{count} 个'
  },
  offline: {
    title: '无法连接 OpenNavo 服务',
    description: '正在显示本地目录；截图、版本记录与合集需要联网后查看。'
  },
  discover: {
    featured: '本周精选',
    viewDetails: '查看详情',
    allCategories: '全部',
    categoriesLabel: '分类',
    popularApps: '热门 App',
    popularAppsHint: '近 30 天 Homebrew 安装量',
    recentlyUpdated: '最近更新',
    recentlyUpdatedHint: '最近发布新版本的 App',
    collections: '合集',
    installCollection: '一键安装合集',
    collectionCount: '{title} {count} 款'
  },
  categories: {
    title: '分类',
    subtitle: '{count} 个分类，按用途浏览',
    packages: '{count} 个',
    empty: '这个分类下还没有包',
    sortLabel: '排序',
    sorts: {
      popular: '热门',
      updated: '最近更新',
      name: '名称'
    }
  },
  rankings: {
    title: '排行榜',
    subtitle: '按 Homebrew 官方安装统计，每天更新',
    periodLabel: '统计周期',
    periods: {
      installs30d: '30 天',
      installs90d: '90 天',
      installs365d: '一年'
    },
    noStats: '暂无统计'
  },
  recent: {
    title: '最近更新',
    subtitle: '最近发布新版本的 App，最新的排在最前面'
  },
  search: {
    title: '搜索',
    results: '「{q}」的 {count} 个结果',
    empty: '没有找到「{q}」',
    emptyHint: '换个关键词，或试试英文名、拼音缩写',
    alias: '别名：{name}',
    prompt: '输入名称、用途或命令开始搜索',
    catalogIncomplete: '第三方应用目录同步失败，搜索结果可能不完整。',
    catalogEmpty: '目录里没有找到「{q}」',
    sources: {
      label: '结果来源',
      all: '全部',
      catalog: '目录'
    }
  },
  appStore: {
    title: 'App Store',
    badge: 'App Store',
    get: '获取',
    getIn: '在 App Store 中获取 {name}',
    free: '免费',
    fromApple: '来自 Apple · {region}商店',
    fromAppleNoRegion: '来自 Apple',
    rating: '{rating} 分 · {count} 个评分',
    rateLimited: 'App Store 搜索太频繁了，请过一分钟再试。',
    offline: '暂时连不上 App Store，请检查网络后重试。',
    searchFailed: 'App Store 搜索失败，请稍后再试。',
    empty: 'App Store 里没有找到「{q}」',
    emptyHint: '换个关键词试试，或者在设置里更换 App Store 地区',
    showAll: '显示全部 {count} 个',
    requiresOs: 'macOS {version} 或更高版本',
    installedVersion: '已安装 {version}',
    headerMeta: '由 App Store 下载 · 需要已登录 Apple ID',
    viewInStore: '在 App Store 中查看',
    alsoTitle: '也可以这样获取',
    whatsNew: '新功能 · {version}',
    whatsNewDated: '新功能 · {version} · {date}',
    about: '介绍',
    info: {
      seller: '开发者',
      version: '版本',
      size: '大小',
      requires: '系统要求',
      category: '类别',
      region: '商店地区',
      bundleId: 'Bundle ID'
    },
    privacyNote: '这些信息由你的 Mac 直接向 Apple 获取，不经过 OpenNavo 的服务器。',
    notInRegion: '这款 App 在{region}商店不可用',
    notInRegionHint: '它可能已经下架，或者只在其他地区的商店上架。',
    settings: {
      region: '商店地区',
      regionHint: '决定搜索结果、价格和能否获取。自动时读取 App Store 账户的地区，读不到就用系统地区',
      auto: '自动',
      autoDetected: '自动（{region}）',
      search: '在搜索中显示 App Store 结果',
      searchHint: '停止输入后才向 Apple 搜索，每分钟最多 15 次',
      updates: '检查 App Store 更新',
      updatesHint: '把 App Store 应用的新版本列在「更新」里',
      privacy: '搜索、详情和更新检查都由这台 Mac 直接连接 Apple 完成，不经过 OpenNavo 的服务器。'
    }
  },
  collections: {
    title: '合集',
    subtitle: '编辑挑选的软件组合，一次装好',
    empty: '还没有合集',
    items: '{count} 款',
    install: '一键安装合集',
    installTitle: '安装合集「{title}」',
    installDescription: '将依次安装 {count} 个包，已安装的会跳过。',
    installConfirm: '开始安装',
    allInstalled: '合集里的包都已安装',
    installed: '已安装',
    queued: '已加入队列：{count} 个',
    offline: '合集需要联网后查看'
  },
  package: {
    link: {
      chip: '官网下载',
      chipGithub: 'GitHub',
      meta: '下载地址来自 {source} · OpenNavo 不托管安装包',
      notHosted: 'OpenNavo 不托管安装包，下载直接来自开发者。',
      homepage: '主页',
      downloadTitle: '下载',
      officialDownload: '前往官网下载',
      release: '{version} · {time}发布',
      recommended: '推荐',
      download: '下载',
      allFiles: '在 GitHub 查看全部文件',
      filtered: '已按这台 Mac（{chip}）筛选',
      others: '其他安装包（{count}）',
      githubReleases: '前往 GitHub 下载',
      teamId: '开发者签名',
      pricingLabel: '价格',
      adopted: 'Homebrew 已收录这款 App，现在可以一键安装',
      arch: {
        arm64: 'Apple 芯片',
        x86_64: 'Intel',
        universal: '通用'
      },
      pricing: {
        free: '免费',
        paid: '付费',
        freemium: '免费，含付费功能',
        subscription: '订阅',
        open_source: '开源免费'
      },
      verdict: {
        verifying: '正在核验 {file}…',
        checked: '已核验 {file}：{details}',
        title: '已核验 {file}',
        view: '查看',
        open: '打开安装包',
        reveal: '在访达中显示',
        who: '{signer}（{team}）',
        teamMatch: '签名 {who} 与登记一致',
        teamMismatch: '签名 {who} 与登记的开发者不一致',
        signed: '开发者签名 {who}',
        unsigned: '没有开发者签名',
        zip: '压缩包没有解开，其中 App 的签名会在首次打开时由 macOS 核对',
        invalid: '签名无效',
        notarized: '已经 Apple 公证',
        notNotarized: '没有经过 Apple 公证',
        clean: '病毒库未发现威胁',
        threat: '发现 {name}',
        threatQuarantined: '发现 {name}，已移到隔离区',
        notScanned: '没有做病毒扫描',
        skipped: '没有做病毒扫描：{reason}',
        modified: '签名后有文件被改动，可能被篡改'
      }
    },
    arch: {
      arm64: 'Apple 芯片',
      x86_64: 'Intel'
    },
    autoUpdates: '自带更新',
    disabled: '已停用',
    deprecated: '已弃用',
    paused: '暂停更新',
    homepage: '主页',
    fromTap: '来自',
    thirdPartyTap: '第三方仓库',
    stats: {
      label: '数据',
      installs30d: '近 30 天安装',
      rank: '{kind}榜第 {rank}',
      installs365d: '近一年安装',
      total365: '365 天累计',
      latest: '最新版本',
      size: '下载大小',
      cadence: '更新节奏',
      releases30d: '近 30 天 {count} 个版本'
    },
    cadence: {
      daily: '每天',
      weekly: '每周',
      biweekly: '每两周',
      monthly: '每月',
      irregular: '不定期'
    },
    tabs: {
      label: '详情页签',
      overview: '概览',
      versions: '版本记录',
      dependencies: '依赖与冲突',
      details: '安装细节'
    },
    disabledNotice: 'Homebrew 已停用此包：{reason}',
    deprecatedNotice: 'Homebrew 已弃用此包：{reason}',
    staleNotice: '超过 90 天没有新版本，OpenNavo 已暂停推荐和安装；Homebrew 仍在提供此应用。',
    noReason: '未说明原因',
    replacement: '替代：{token}',
    release: {
      new: '新版本',
      title: '{version} 更新内容',
      source: {
        github_release: '来自 GitHub Release',
        sparkle: '来自应用内更新源',
        webpage: '来自官网更新页',
        manual: '人工整理',
        editorial: 'OpenNavo 编辑',
        homebrew: '来自 Homebrew 提交记录'
      },
      viewAll: '查看全部 {count} 个版本',
      noNotes: '这个版本没有单独的更新说明。',
      emptyTitle: '还没有版本记录',
      prerelease: '预发布',
      filterLabel: '版本筛选',
      filterAll: '全部 {count}',
      filterWithNotes: '有更新说明 {count}',
      filterMine: '我装过的 {count}',
      languageLabel: '正文语言',
      languageTranslated: '中文',
      languageOriginal: '原文',
      mineEmpty: '已加载的版本里没有你装过的',
      count30d: '近 30 天',
      count30dUnit: '个版本',
      cadenceHint: {
        daily: '几乎每天发布',
        weekly: '大约每周发布',
        biweekly: '大约两周发布一次',
        monthly: '大约每月发布',
        irregular: '发布时间不固定'
      },
      brewLabel: 'Homebrew 收录',
      brewSync: '≈ 同步',
      brewLater: '晚 {time}',
      brewEarlier: '早 {time}',
      brewHint: '{earlier}/{compared} 个早于上游',
      lagMinutes: '{n} 分钟',
      lagHours: '{n} 小时',
      lagDays: '{n} 天',
      sourcesTitle: '数据来源',
      sourceInfo: {
        webpage: {
          title: '官网更新页',
          body: '从开发者官网的更新说明页提取要点'
        },
        github_release: {
          title: 'GitHub Releases',
          body: '项目在 GitHub 发布的版本说明与发布时间'
        },
        sparkle: {
          title: '应用内更新源',
          body: 'App 内置更新检查使用的 Sparkle appcast'
        },
        manual: { title: '人工整理', body: '编辑根据官方公告整理' },
        editorial: {
          title: 'OpenNavo 编辑',
          body: 'OpenNavo 根据官方信息撰写的更新说明'
        },
        homebrew: {
          title: 'Homebrew 提交记录',
          body: 'Homebrew 仓库里该包的版本更新提交，得到收录时间'
        },
        local: { title: '本机记录', body: '你在 OpenNavo 里的每次安装和更新' }
      },
      localUpgrade: '你在 {time} 通过 OpenNavo 从 {from} 更新到此版本 · 用时 {duration}',
      localInstall: '你在 {time} 通过 OpenNavo 安装了此版本 · 用时 {duration}',
      notify: '有新版本时通知我',
      notifyHint: '所有 App 共用 · 系统通知 + 菜单栏角标',
      pin: '固定在当前版本',
      pinHint: '等同 brew pin，「全部更新」时跳过',
      pinHintSelfUpdate: '等同 brew pin；但 {name} 自带的更新器仍可能自行升级'
    },
    monthly: {
      title: '月均安装量',
      d30: '近 30 天',
      avg90: '近 90 天月均',
      avg365: '近一年月均',
      full: '{count} 次',
      trend: {
        rising: '近期安装在增长',
        slowing: '近期安装在放缓，多数老用户靠应用自带更新升级',
        steady: '安装量保持稳定'
      }
    },
    related: '相似应用',
    screenshots: '截图',
    caveats: '注意事项',
    info: {
      homepage: '主页',
      binaries: '附带命令',
      autoUpdates: '自动更新',
      autoYes: '应用自带（auto_updates）',
      autoNo: '由 Homebrew 更新',
      uninstall: '卸载时',
      arch: '架构',
      license: '许可证'
    },
    spec: {
      downloadSource: '下载来源',
      fontFiles: '字体文件',
      title: '基本信息',
      price: '价格',
      system: '系统',
      chip: '芯片',
      autoUpdates: '自动更新',
      openSource: '开源',
      repository: '仓库',
      githubRepository: 'GitHub 仓库',
      checked: '核查日期',
      developer: '开发者',
      homepage: '主页',
      token: '软件包标识符',
      installedVersion: '已安装版本',
      installLocation: '安装位置',
      yes: '是',
      no: '否'
    },
    repo: {
      stars: '星标数',
      checked: '仓库数据核查于 {date}。',
      updated: '{days} 天前更新',
      unmaintained: '可能已停止维护',
      archived: '已归档'
    },
    deps: {
      macos: '系统要求',
      formulae: '依赖的命令行工具',
      casks: '依赖的 App',
      emptyTitle: '没有依赖',
      emptyDescription: '安装时不会附带安装其他包。',
      runtime: '运行时依赖',
      build: '构建依赖',
      other: '其他：',
      type: {
        runtime: '运行时',
        build: '构建',
        test: '测试',
        optional: '可选',
        recommended: '推荐',
        usesFromMacos: '使用系统自带',
        cask: 'App'
      },
      conflicts: '冲突',
      dependents: '被 {count} 个包依赖'
    },
    details: {
      tap: '来源仓库',
      names: '其他名称',
      location: '安装位置',
      pkgs: '安装包',
      kegOnly: 'keg-only',
      kegOnlyValue: '不链接到 /opt/homebrew，需要手动加入 PATH',
      download: '下载地址',
      source: 'Homebrew 定义文件',
      repo: '源代码'
    },
    installed: '已安装 {version}',
    installedSelfUpdated: '已安装 {version}（应用已自行更新）',
    installedAt: '安装于 {date}',
    updateTo: '更新到 {version}',
    open: '打开',
    getNeedsBrew: '先安装 Homebrew',
    actions: {
      label: '更多操作',
      uninstall: '卸载',
      reinstall: '重新安装',
      pin: '固定版本',
      unpin: '取消固定',
      reveal: '在访达中显示',
      copyCommand: '复制 brew 命令',
      web: '在网页上查看'
    },
    commandCopied: '已复制 brew 命令',
    offline: '联网后可查看截图与更多信息',
    notFound: '本地目录里没有这个包',
    pinned: '已固定',
    localSince: '你在 {date} 更新到此版本'
  },
  library: {
    title: '已安装',
    summary: '{count} 个 App · 占用 {size}',
    summaryCount: '{count} 项',
    filterPlaceholder: '筛选名称或 Bundle ID…',
    scanProgress: {
      running: '正在统计占用空间',
      done: '占用空间统计完成',
      partial: '统计完成，部分应用失败',
      completed: '已完成 {completed} / {total}',
      scope: '应用本体、应用数据与缓存',
      retrying: '等待重试 {count} 个',
      failed: '{count} 个应用失败，原因见列表'
    },
    retryFootprint: '点击重新计算此应用的占用空间',
    recalculate: '重新统计占用',
    refresh: '刷新',
    empty: '没有找到可以卸载的 App',
    emptyFiltered: '没有符合条件的项目',
    columns: {
      name: '名称',
      version: '版本',
      size: '占用空间',
      status: '状态',
      actions: '操作',
      lastUsed: '最近使用'
    },
    status: {
      installed: '已安装',
      latest: '最新',
      outdated: '可更新',
      pinned: '已固定',
      selfUpdated: '最新（应用已自行更新）',
      running: '更新中 {percent}',
      runningUnknown: '处理中',
      queued: '排队中',
      unknown: '未知',
      uninstalling: '卸载中 {percent}',
      protected: '当前 App',
      managed: '受管理',
      adoptable: '可接管'
    },
    storage: {
      title: '存储',
      total: '共 {size}',
      caption: '磁盘占用',
      cache: '下载缓存',
      homebrew: 'Homebrew',
      others: '其他 App'
    },
    backup: {
      title: '备份与迁移',
      description: '把 Homebrew 管理的 App 导出为 Brewfile，换新 Mac 时一键恢复。',
      export: '导出',
      restore: '恢复',
      exported: '已导出 {count} 个包'
    },
    restore: {
      title: '从 Brewfile 恢复',
      summary: '可安装 {ready} 个 · 已安装 {installed} 个 · 暂不支持 {unsupported} 个',
      install: '安装 {count} 个',
      status: {
        ready: '可安装',
        installed: '已安装',
        skipped: '无需操作',
        unsupported: '暂不支持',
        invalid: '无效',
        not_found: '找不到'
      }
    },
    health: {
      title: '健康检查',
      ok: 'brew doctor 没有发现问题',
      warnings: '{count} 条警告',
      run: '运行检查',
      running: '正在检查…'
    },
    adoptableHint: 'Homebrew 收录了这个 App，接管后可以在 OpenNavo 里更新',
    sources: {
      all: '全部',
      homebrew: 'Homebrew',
      app_store: 'App Store',
      other: '其他',
      direct: '手动安装',
      pkg: 'PKG 安装'
    },
    sourceFilter: '按来源筛选',
    unused: '长期未用',
    unusedHint: '超过 {days} 天没有打开过',
    running: '运行中',
    self: '当前正在使用的 App',
    systemNote: '系统自带的 App（如 Safari、邮件）受 macOS 保护，不在这里显示。',
    selectAll: '全选',
    selectRow: '选择 {name}',
    batch: {
      selected: '已选 {count} 个 App',
      size: 'App 本体共 {size} · 相关文件在下一步扫描',
      clear: '取消选择',
      uninstall: '批量卸载'
    },
    menu: {
      info: '显示简介',
      adopt: '用 Homebrew 接管',
      detail: '在 OpenNavo 中查看'
    },
    unusedCard: {
      title: '长期未使用',
      description: '{count} 个 App 超过 {days} 天没打开过，App 本体共 {size}。',
      show: '只看这些 App',
      all: '显示全部 App',
      none: '最近 {days} 天里每个 App 都打开过。'
    }
  },
  updates: {
    review: {
      title: '安装前请确认',
      body: '已核对应用标识和目标版本。请确认以下情况；你的选择仅对本次安装包有效。',
      unsigned_developer: '旧版、新版或安装包缺少可验证的开发者签名。请仅在信任来源时继续，macOS 仍可能阻止打开。',
      repair_installed: '已安装应用的签名损坏。新版本已通过签名和身份检查，继续将替换旧应用。',
      not_notarized: '新版本或安装包未经 Apple 公证。macOS 可能阻止打开。',
      non_newer: '目标版本不高于已安装版本，将进行重装或降级。旧版本可能无法读取新版应用数据。'
    },
    unsigned: {
      title: '无法确认开发者身份',
      body: '旧版、新版或安装包缺少可验证的开发者签名。请仅在信任来源时继续，macOS 仍可能阻止打开。',
      confirm: '仍要安装',
      badge: '开发者未验证'
    },
    title: '更新',
    summary: '{count} 个可用更新 · 下载约 {size}',
    summaryNone: '全部是最新',
    lastCheck: '上次检查 {time}',
    check: '检查更新',
    checking: '正在检查…',
    updateAll: '全部更新',
    changelog: '更新内容',
    update: '更新',
    pinned: '已固定，不会更新',
    patch: '补丁',
    empty: '所有软件都是最新的',
    emptyHint: '有新版本时会在这里列出',
    source: {
      homebrew: 'Homebrew',
      mas: 'App Store',
      sparkle: 'App 自带更新源',
      electron: 'App 自带更新源',
      github: 'GitHub',
      vendor: '开发者网站',
      msupdate: 'Microsoft AutoUpdate'
    },
    badge: {
      critical: '重要更新',
      beta: '测试版',
      major: '大版本',
      teamMismatch: '签名不一致'
    },
    hint: {
      relaunch: '已更新到 {version}，重新打开后生效',
      informational: '这个版本需要到开发者网站查看或下载',
      running: '正在运行，更新前会先请你退出',
      admin: '更新时需要管理员密码',
      skipped: '已跳过 {version}',
      teamMismatch: '预期的开发者签名与已装 App 不一致，OpenNavo 不会安装这个更新'
    },
    actions: {
      skip: '跳过这个版本',
      unskip: '取消跳过 {version}',
      beta: '接收测试版',
      stable: '只接收正式版',
      unignoreApp: '恢复检查这个 App',
      ignoreApp: '不再检查这个 App',
      relaunch: '重新打开',
      view: '查看'
    },
    toast: {
      skipped: '已跳过 {name} {version}',
      unskipped: '{name} 的新版本会重新提醒',
      beta: '{name} 改为接收测试版',
      stable: '{name} 只接收正式版',
      unignoredApp: '已恢复检查 {name} 的更新',
      ignoredApp: '不再检查 {name} 的更新'
    },
    notes: {
      title: '{name} {version} 更新内容'
    },
    masSignedOut: '上次 App Store 更新未能确认账户状态。请重试，或在 App Store 中完成更新。',
    failedSources: '这次未能完成 {sources} 的部分检查，已取得的结果仍正常显示。',
    checkFailures: {
      summary: '部分 App 或更新来源未能完成检查，已取得的结果仍正常显示。',
      details: '查看检查详情',
      rulesUnavailable: '更新规则暂时无法刷新。已尽可能使用缓存规则；缺少规则的检查未能完成。',
      rateLimited: '更新服务器请求过多，请稍后重试。',
      invalidUrl: '更新地址无效，需要应用开发者修正更新配置。',
      network: '无法连接更新服务器，请稍后重试。',
      failed: '未能完成更新检查。'
    },
    openAppStore: '打开 App Store',
    major: {
      titleOne: '{name} 是大版本更新',
      title: '{count} 个大版本更新',
      body: '大版本可能改变界面、要求重新授权，或不再兼容旧的文件。确定要更新吗？',
      others: '其他更新会照常进行。',
      skip: '暂不更新',
      confirm: '继续更新'
    },
    filter: {
      label: '来源',
      all: '全部 {count}',
      homebrew: 'Homebrew {count}',
      mas: 'App Store {count}',
      other: '其他来源 {count}'
    },
    meta: {
      from: '来自 {host}',
      teamMatch: '预期 Team ID 一致'
    },
    sources: {
      title: '检查哪些来源',
      subtitle: '关掉的来源不再检查',
      homebrew: 'Homebrew',
      homebrewHint: '运行 brew outdated',
      mas: 'App Store',
      masHint: '直接向 Apple 查询',
      sparkle: 'App 自带更新源',
      sparkleHint: '安装前核对签名和公证',
      electron: 'Electron 自带更新源',
      electronHint: '核对 sha512 和签名后安装',
      github: 'GitHub Releases',
      githubHint: '核对文件摘要和开发者签名',
      vendor: '开发者网站',
      vendorHint: '按 OpenNavo 维护的规则，核对开发者签名'
    },
    queued: '已加入队列：{count} 个',
    history: {
      recheck: '重新检查',
      title: '更新记录',
      subtitle: '本机 · 近 30 天 {count} 次',
      filterLabel: '结果',
      all: '全部',
      succeeded: '成功',
      failed: '失败',
      today: '今天 · {date}',
      yesterday: '昨天 · {date}',
      viewLog: '查看日志',
      retry: '重试',
      undo: '撤销',
      purge: '彻底删除',
      canceled: '已取消',
      batch: '{action} {count} 个{kind}',
      empty: '还没有更新记录',
      rollback: '回滚'
    },
    month: {
      title: '本月',
      since: '{date} 至今',
      updated: '已更新',
      updatedUnit: '次',
      successRate: '成功率',
      successHint: '失败 {count} 次'
    },
    auto: {
      title: '自动更新',
      subtitle: '由后台助手按计划执行',
      check: '每天 {time} 检查',
      checkHint: 'brew update + outdated',
      casks: '自动安装 App 更新',
      casksHint: '关闭时只发通知',
      greedy: '包含自带更新的 App',
      greedyHint: '等同 --greedy'
    },
    mirror: {
      switch: '切换',
      latency: '全部走镜像 · {ms} ms'
    },
    next: {
      today: '今天 {time}',
      tomorrow: '明天 {time}',
      title: '下次检查',
      text: '{time}。若 Mac 处于睡眠，唤醒后补做检查。',
      off: '自动检查已关闭，可以在设置中开启。'
    },
    log: {
      title: '{name} 的日志',
      empty: '没有日志'
    },
    rollback: {
      title: '把 {name} 回滚到 {version}？',
      body: '换回这次更新前保留的 {version}。如果 {name} 正在运行，会先请它正常退出，回滚后再打开。',
      skip: '之后不再提示 {version}，有更新的版本时再提示。',
      confirm: '回滚',
      notQuit: '{name} 没有退出，没有回滚；退出后可以再试'
    }
  },
  history: {
    clear: '清空记录',
    clearDescription: '确定清空全部更新记录吗？此操作不可恢复，不影响已安装的 App 和正在进行的任务。',
    clearNote: '还能撤销的卸载和清理、还能回滚的更新会保留。',
    cleared: '更新记录已清空',
    clearedKept: '已清空，{count} 条还能撤销或回滚的记录已保留',
    title: '更新记录',
    subtitle: '本机所有安装、更新与卸载',
    search: '按名称筛选',
    exportCsv: '导出 CSV',
    exported: '已导出 {count} 条记录',
    empty: '还没有记录',
    duration: '{time}'
  },
  settings: {
    title: '设置',
    sections: {
      general: '通用',
      mirrors: '下载源',
      homebrew: 'Homebrew',
      appstore: 'App Store',
      privacy: '隐私',
      about: '关于',
      cleanup: '清理',
      monitor: '电脑监控',
      security: '安全防护'
    },
    general: {
      followSystem: '跟随系统（当前：{language}）',
      systemDialogsRestartHint: '系统对话框重启后切换语言。',
      language: '界面语言',
      appearance: '外观',
      appearanceSystem: '跟随系统',
      appearanceLight: '浅色',
      appearanceDark: '深色',
      launchAtLogin: '登录时启动',
      launchAtLoginHint: '启动后只显示菜单栏图标',
      keepInMenuBar: '关闭窗口后留在菜单栏',
      trayShowCount: '菜单栏显示可更新数量',
      notifyUpdates: '有可用更新时发通知',
      zapByDefault: '卸载时默认清理相关文件',
      zapByDefaultHint: '应用数据、缓存和偏好设置等会逐项列出，确认后再删',
      updates: '检查更新',
      autoCheck: '每天自动检查更新',
      checkTime: '检查时间',
      runBrewUpdate: '检查前先更新 Homebrew 索引',
      runBrewUpdateHint: 'brew update',
      uninstall: '卸载',
      deleteMode: '删除方式',
      deleteModeHint: '移到废纸篓可以撤销；直接删除会立即释放空间'
    },
    customMirrors: {
      add: '添加自定义源',
      edit: '编辑自定义源',
      delete: '删除自定义源',
      actions: '管理「{name}」',
      local: '仅本机',
      localHint: '自定义源仅保存在这台 Mac，不上传或同步。',
      fallback: '留空的地址使用官方源。',
      officialFallback: '使用官方源',
      name: '名称',
      apiDomain: 'API 地址',
      bottleDomain: 'Bottle 地址',
      brewGitRemote: 'brew Git 仓库地址',
      coreGitRemote: 'core Git 仓库地址',
      advanced: '高级设置 · Git 仓库',
      check: '检查连接',
      probeHint: '仅检查各地址是否可访问，不代表一定能完成下载。',
      untested: '尚未检查',
      success: '地址可连接',
      failure: '连接失败',
      checkFailed: '无法连接：{fields}',
      save: '保存修改',
      saveAndUse: '保存并使用',
      nameRequired: '请填写名称。',
      nameInvalid: '名称最多 120 个字符，不能包含控制字符。',
      urlInvalid: '请填写 HTTP 或 HTTPS 地址，不能包含登录凭据或片段。',
      apiQueryInvalid: 'API 地址不能包含查询参数，请填写不带 ? 的基址。',
      addressRequired: '请至少填写一个自定义地址。',
      deleteConfirm: '从这台 Mac 删除「{name}」？',
      deleteActive: '该源正在使用，删除后将切回官方源。',
      limit: '最多可保存 {count} 个自定义源。'
    },
    mirrors: {
      title: '下载源',
      description: '同时作用于 API 元数据、Bottle 二进制包和 brew 自身的 Git 仓库。',
      copyEnv: '复制终端环境变量',
      envCopied: '已复制，可粘贴到终端或 shell 配置',
      envHint: '只影响 OpenNavo 发起的 brew；需要在终端里使用同一个源时复制下面的变量。'
    },
    homebrew: {
      path: 'brew 路径',
      auto: '自动检测',
      version: '版本',
      prefix: '安装前缀',
      analytics: '向 Homebrew 发送匿名统计',
      analyticsFollow: '跟随 brew 自身的设置',
      analyticsOff: '关闭（HOMEBREW_NO_ANALYTICS=1）',
      missing: '没有找到 Homebrew',
      install: '安装 Homebrew'
    },
    privacy: {
      crashReports: '发送匿名崩溃报告',
      crashReportsHint: '默认关闭；开启后只上传崩溃时的调用栈',
      appManagement: 'App 管理',
      appManagementHint: '更新和卸载 App 时需要',
      fullDiskAccess: '完全磁盘访问权限',
      fullDiskAccessHint: '查看占用空间、卸载 App 和从废纸篓撤销时需要',
      enable: '开启',
      waiting: '等待开启…',
      granted: '已开启',
      denied: '未开启',
      unknown: '无法检测',
      clearSearch: '清除最近搜索'
    },
    about: {
      openSource: '开源帮助',
      view: '查看',
      draft: '待审',
      version: '版本 {version}',
      website: '网站',
      feedback: '反馈问题',
      privacy: '隐私说明',
      catalog: '本地目录：{count} 个包，同步于 {time}',
      sync: '立即同步'
    },
    saved: '已保存'
  },
  mirrors: {
    official: '官方源',
    probe: '重新测速',
    probing: '正在测速…',
    fastest: '最快',
    recommended: '推荐',
    unavailable: '不可用',
    configFailed: '暂时拿不到镜像列表，仍可使用官方源和本机自定义源。',
    slowOfficial: '官方源响应较慢（{official}），{name} 只需 {latency}。',
    failedOfficial: '官方源无法连接，{name} 可以连接（{latency}）。',
    useSuggested: '改用{name}',
    selectedFailed: '所选下载源测速失败，安装可能会失败。'
  },
  welcome: {
    title: '欢迎使用 OpenNavo',
    subtitle: '开始前先检查一下这台 Mac，只需几秒。',
    checks: '环境检测',
    checking: '正在检测…',
    detectFailed: '检测没有完成。',
    redetect: '重新检测',
    macos: 'macOS {version} · {arch}',
    macosOk: '符合要求',
    clt: 'Xcode 命令行工具',
    cltOk: '已安装',
    cltMissing: '安装 Homebrew 时会一并安装',
    homebrew: 'Homebrew',
    homebrewOk: '{version} · {prefix}',
    homebrewMissing: '未找到，{prefix} 为空',
    source: {
      title: '下载源',
      official: '官方源安装',
      mirror: '其他镜像安装',
      mirrorMissing: '暂时拿不到镜像列表',
      retry: '重试',
      hint: '之后的安装和更新也会使用这个下载源，可以在「设置 → 下载源」里换成其他镜像。',
      hintBrew: 'OpenNavo 安装和更新 App 时使用这个下载源，不读取终端里设置的镜像变量。'
    },
    install: '安装 Homebrew',
    passwordHint: '安装需要管理员密码，系统会弹出对话框；OpenNavo 不会保存密码。',
    skip: '跳过，先随便看看',
    failed: {
      title: 'Homebrew 没有装好',
      E_SUDO: '没有输入管理员密码，安装已取消。',
      E_DOWNLOAD: '安装脚本下载失败，可以换一个下载源再试。',
      viewLog: '查看日志',
      logTitle: '安装 Homebrew 的日志'
    },
    permissions: {
      required: '必需',
      optional: '可选',
      enable: '开启',
      waiting: '等待开启…',
      granted: '已开启',
      confirm: '我已开启',
      appManagement: {
        title: '允许 OpenNavo 管理 App',
        body: '更新和卸载 App 时需要。',
        enable: '开启 App 管理'
      },
      fullDiskAccess: {
        title: '完全磁盘访问权限',
        body: '查看占用空间、卸载 App 和从废纸篓撤销时需要。',
        enable: '开启完全磁盘访问权限'
      }
    },
    done: '开始使用',
    arch: {
      arm64: 'Apple 芯片',
      x86_64: 'Intel'
    }
  },
  permission: {
    relaunch: {
      updated: 'OpenNavo 已更新，重新打开后即可继续使用。',
      ineffective: '重新打开后仍未生效。请在「完全磁盘访问权限」里关闭 OpenNavo 再重新开启，然后重新打开 OpenNavo。',
      openSettings: '打开系统设置',
      body: '已在系统设置中开启，重新打开 OpenNavo 后生效。',
      action: '重新打开',
      pending: '重新打开后生效',
      busy: '有任务正在进行，完成后再重新打开'
    },
    guide: {
      appManagement: 'App 管理',
      fullDiskAccess: '完全磁盘访问权限',
      dragTitle: '把 {name} 拖到上方的列表',
      plusTitle: '点按列表下方的「+」，选择 {name}',
      toggleHint: '列表里已有 {name} 时，打开它旁边的开关即可。',
      dragLabel: '拖动 {name} 图标',
      close: '关闭',
      done: {
        appManagement: '已允许 OpenNavo 管理 App',
        fullDiskAccess: '已开启完全磁盘访问权限'
      },
      doneHint: '如果系统提示「退出并重新打开」，点它即可。'
    },
    prompt: {
      appManagement: {
        title: '需要允许 OpenNavo 管理 App',
        body: 'macOS 阻止了 OpenNavo 修改下面的 App。开启「App 管理」后重试。'
      },
      fullDiskAccess: {
        title: '需要完全磁盘访问权限',
        body: '下面的 App 已经移除，但应用数据还没删：删除这些数据需要「完全磁盘访问权限」。开启后重试即可完成卸载。'
      },
      enable: '开启',
      waiting: '等待开启…',
      retry: '重试',
      retryAll: '全部重试',
      later: '稍后'
    }
  },
  fullDiskGate: {
    title: '先开启「完全磁盘访问权限」',
    body: '「已安装」要读取各个 App 在资源库里的数据，才能算准占用空间、找全卸载后留下的文件，并能从废纸篓撤销卸载。',
    points: {
      footprint: '算准每个 App 的占用空间，包括应用数据和缓存',
      leftovers: '卸载时找全容器、群组容器等相关文件',
      undo: '卸载后可以从废纸篓撤销'
    },
    row: '在系统设置中允许 OpenNavo 后，这里会自动继续。',
    unknown: 'macOS 没能告知权限状态。如果已经开启：',
    privacy: '只在这台 Mac 上读取，不会上传任何文件或路径。'
  },
  deeplink: {
    installTitle: '安装 {name}？',
    installDescription: '网页请求在这台 Mac 上安装它。确认后将执行下面的命令；取消则不做任何操作。',
    installConfirm: '安装',
    formulaUnsupported: 'OpenNavo 只收录 App（Homebrew Cask），这个链接指向的是命令行工具。',
    preview: {
      invalid: '这个链接打不开',
      invalidBody: '链接里的参数不完整或不符合要求，OpenNavo 没有做任何操作。',
      startup: {
        looking: '正在这台 Mac 上查找 {label}…',
        found: '找到 {count} 个标签为 {label} 的启动项',
        foundBody:
          '这是从网页打开的预览，还没有做任何改动。同一个标签可能被多个程序共用，也可能被冒用，请先核对签名、文件位置和所属 App，再决定是否停用或移除。',
        view: '查看 {name}',
        notFound: '这台 Mac 上没有标签为 {label} 的启动项',
        notFoundBody: '它可能没有安装，或者 macOS 没有让 OpenNavo 读取它所在的位置。没有做任何改动。'
      },
      scan: {
        looking: '正在这台 Mac 上查找 {token}…',
        title: '扫描 {name}',
        body: '这是从网页打开的预览：OpenNavo 在这台 Mac 上找到了这个 App。点「扫描这个 App」之后才会开始扫描。',
        action: '扫描这个 App',
        notFound: '这台 Mac 上没有找到 {token}',
        notFoundBody: 'OpenNavo 只能扫描已经装在这台 Mac 上的 App。没有做任何操作。'
      },
      uninstall: {
        title: '预览卸载 {name}',
        body: '这是从网页打开的预览，还没有删除任何东西。点「卸载」后会先列出要移除的内容，你确认之后才会执行。',
        several: '这台 Mac 上有 {count} 个 App 对应 {token}，选一个查看：',
        view: '查看 {name}',
        notFound: '这台 Mac 上没有找到 {token}',
        notFoundBody: '它可能已经卸载了，或者安装方式让 OpenNavo 认不出来。没有做任何操作。',
        permission: '开启「App 管理」和「完全磁盘访问权限」之后，才能预览卸载 {token}。'
      }
    }
  },
  tray: {
    available: '{count} 个可用更新',
    upToDate: '全部是最新的',
    upToDateHint: '有新版本时会显示在这里',
    running: {
      upgrade: '正在更新 {name}',
      install: '正在安装 {name}',
      uninstall: '正在卸载 {name}',
      other: '正在处理 {name}'
    },
    queued: '另有 {count} 个任务排队',
    update: '更新',
    updateAll: '全部更新（{count}）',
    more: '还有 {count} 个，在 OpenNavo 中查看',
    open: '打开 OpenNavo',
    settings: '设置',
    queuedToast: '已加入队列'
  },
  quit: {
    title: '还有 {n} 个任务没有完成',
    description: '退出会中断正在进行的安装，已完成的不受影响。',
    confirm: '仍要退出',
    cancel: '继续运行'
  },
  restore: {
    title: '上次有 {n} 个任务没有完成',
    description: '继续会按原来的顺序执行；放弃会取消这些任务。',
    resume: '继续',
    discard: '放弃'
  },
  updater: {
    autoCheck: '自动检查 OpenNavo 更新',
    autoCheckHint: '启动后检查，每天最多成功检查一次。',
    foundTitle: '发现新版本',
    foundDescription: 'OpenNavo {version} 已可下载',
    versionChange: '当前版本 {current} → 新版本 {next}',
    notesTitle: '本次更新',
    noNotes: '此版本未提供更新说明。',
    fullNotes: '查看完整更新说明',
    later: '稍后',
    restartHint: '安装完成后需要重启，正在进行的任务可先完成。',
    readyTitle: '更新已就绪',
    downloadingUnknown: '正在下载更新…',
    installFailed: '下载或安装失败，请重试。',
    viewUpdate: '查看更新',
    previewHint: '开发演示：模拟下载和安装进度，不会真实更新；重新启动仅关闭演示，可再次查看。',
    previewNotes: '- 精选合集布局更紧凑，浏览更多应用\n- 每天自动检查 OpenNavo 新版本\n- 修复已知问题，提升使用体验',
    previewFailure: '模拟下载失败',
    title: '客户端更新',
    current: '当前版本 {version}',
    check: '检查更新',
    latest: '已是最新版本',
    available: '新版本 {version}',
    install: '下载并安装',
    downloading: '正在下载 {percent}',
    installed: '已安装，重新启动后生效',
    restart: '重新启动',
    busy: '有任务正在进行，完成后再重新启动',
    failed: '检查更新失败'
  },
  palette: {
    placeholder: '搜索 App 或操作',
    apps: 'App',
    actions: '操作',
    navigation: '跳转',
    open: '打开',
    install: '安装',
    update: '更新 {name}',
    reveal: '在访达中显示 {name}',
    viewAll: '查看「{q}」的全部结果',
    goTo: '前往{page}',
    installedVersion: '已安装 {version}',
    updatable: '可更新',
    copied: '已复制 {command}'
  },
  tasks: {
    ops: {
      install: '安装',
      upgrade: '更新',
      uninstall: '卸载',
      reinstall: '重新安装',
      pin: '固定版本',
      unpin: '取消固定',
      cleanup: '清理缓存',
      update: '更新 Homebrew',
      install_homebrew: '安装 Homebrew',
      remove_leftovers: '清理残留',
      clean: '清理电脑',
      av_db_update: '更新病毒库',
      rollback: '回滚'
    },
    running: {
      install: '正在安装 {name}',
      upgrade: '正在更新 {name}',
      uninstall: '正在卸载 {name}',
      reinstall: '正在重新安装 {name}',
      pin: '正在固定 {name}',
      unpin: '正在取消固定 {name}',
      cleanup: '正在清理缓存',
      update: '正在更新 Homebrew',
      install_homebrew: '正在安装 Homebrew',
      remove_leftovers: '正在清理残留：{name}',
      clean: '正在清理电脑',
      av_db_update: '正在更新病毒库',
      rollback: '正在回滚 {name}'
    },
    phases: {
      preparing: '准备',
      fetching: '获取信息',
      downloading: '下载',
      scanning: '扫描安装包',
      installing: '安装',
      linking: '链接',
      cleaning: '清理',
      uninstalling: '卸载',
      updating: '更新索引',
      finishing: '完成'
    },
    step: '第 {index} 步，共 {count} 步：{phase}',
    remaining: '剩余约 {time}',
    queue: '排队中',
    oneAtATime: 'brew 一次只运行一个任务',
    triggers: {
      manual: '手动',
      schedule: '自动',
      deeplink: '网页',
      bundle: '批量',
      retry: '重试'
    },
    succeeded: '{name} {action}完成',
    failed: '{name} {action}失败'
  },
  errors: {
    E_STARTUP_PROTECTED: '这是 macOS 自带的项目，不能在这里更改',
    E_AV_CONSENT: '需要先同意下载病毒库',
    E_AV_NOT_READY: '病毒库还没有下载，请先在「病毒扫描」里下载',
    E_AV_ENGINE: '病毒扫描引擎没有正常启动，请稍后重试',
    E_AV_SCAN_FAILED: '扫描没有完成，请重新扫描',
    E_AV_DB_RATE_LIMITED: '病毒库更新太频繁，被官方服务器暂时限制了，稍后会自动重试',
    E_AV_DB_UPDATE: '病毒库更新失败，请检查网络后重试',
    E_RESTORE_CONFLICT: '原来的位置已经有同名文件，没有恢复',
    E_MAS_NOT_SIGNED_IN: '无法确认 App Store 账户状态。请重试，或在 App Store 中完成操作',
    E_MAS_NOT_OWNED: '这个账户还没有获取过这款 App，请在 App Store 里点「获取」',
    E_MAS_PAID: '付费 App 请在 App Store 里购买',
    E_MAS_OTHER_ACCOUNT: '这款 App 由其他 Apple 账户购买，请在 App Store 里更新',
    E_MAS_REGION: '这款 App 在当前商店地区不可用',
    E_MAS_RATE_LIMITED: 'App Store 请求太频繁了，请过一分钟再试',
    E_UPDATE_DIGEST: '下载文件的大小或摘要与更新清单不一致，没有安装',
    E_UPDATE_SIGNATURE: '更新包的代码签名无效，没有安装',
    E_UPDATE_FEED_SIGNATURE: '更新包未通过更新源的签名验证，没有安装',
    E_UPDATE_IDENTITY: '更新包的应用标识与目标应用不一致，没有安装',
    E_UPDATE_CHANGED: '应用或更新文件在校验后发生变化，没有安装，请重新检查更新',
    E_UPDATE_NOT_NEWER: '目标版本不高于已安装版本，请从已安装应用中选择重新安装',
    E_UPDATE_CONFIRMATION: '安装需要你确认，请查看提示或重试',
    E_UPDATE_VERIFY_UNAVAILABLE: 'macOS 暂时无法完成开发者验证，没有安装。请稍后重试',
    E_UPDATE_INTEGRITY: '下载的更新没有通过完整性校验，没有安装',
    E_UPDATE_PREPARE: '无法准备更新文件，请检查磁盘空间和访问权限后重试',
    E_UPDATE_ARCHIVE: '无法读取、挂载或解压更新安装包，请查看日志后重试',
    E_UPDATE_VERSION: '更新安装包的实际版本与发布清单不一致，没有安装',
    E_UPDATE_TEAM_MISMATCH: '新版本的开发者签名和已安装的不一致，没有安装',
    E_UPDATE_NOT_NOTARIZED: '新版本没有经过 Apple 公证，没有安装',
    E_UPDATE_ARCH: '新版本不支持这台 Mac 的芯片',
    E_UPDATE_MIN_OS: '新版本需要更新的 macOS',
    E_UPDATE_FEED: '读取更新源失败，请稍后再试',
    E_UPDATE_DMG_LICENSE: '这个安装包要求先同意许可协议，请手动安装这个版本',
    E_UPDATE_SWAP: '替换 App 时出错，已恢复到原来的版本',
    E_ADMIN_CANCELED: '已取消管理员授权',
    E_NOT_IMPLEMENTED: '这项功能还没有开放',
    E_DOWNLOAD_UNSAFE: '这个安装包没有通过核验，已阻止打开',
    E_MAS_ENGINE: 'App Store 下载组件出错，请改用 App Store 完成',
    E_MAS_HELPER_MISSING: '当前应用缺少 App Store 下载组件。请重新安装完整的 OpenNavo 应用，或在 App Store 中完成操作',
    E_NOT_INSTALLABLE: '这款 App 只能从官网下载，不能用 Homebrew 安装',
    E_LINK_URL: '这个下载地址不在登记范围内，没有打开',
    E_APP_RUNNING: '应用正在运行，已暂缓更新；退出后可再次更新',
    E_APP_QUIT_FAILED: '应用未退出，已暂缓更新；请保存工作并退出后重试',
    E_APP_CHECK_FAILED: '无法检查应用运行状态，已暂缓更新；请刷新已安装列表后重试',

    actionFailed: '操作没有完成',
    E_BREW_NOT_FOUND: '需要先安装 Homebrew',
    E_INVALID_ARG: '参数无效',
    E_CHECKSUM: '下载中断：SHA-256 校验不一致',
    E_DOWNLOAD: '下载失败，请检查网络或切换下载源',
    E_APP_EXISTS: '安装位置已存在同名 App，本次操作未完成；请查看日志。安装任务重试时会重新检查并请求接管确认',
    E_NOT_FOUND: '在 Homebrew 中找不到这个包',
    E_DISABLED: 'Homebrew 已停用此包',
    E_LOCKED: '另一个 Homebrew 进程正在运行，稍后自动重试',
    E_PERMISSION: '没有权限修改该 App，需要在系统设置中允许',
    E_FULL_DISK_ACCESS: '删除应用数据需要「完全磁盘访问权限」',
    E_SUDO: '需要管理员密码',
    E_MACOS_VERSION: '系统版本不满足要求',
    E_REQUIRED_BY: '有其他包依赖它，不能卸载',
    E_CONFLICT: '与已安装的包冲突',
    E_INTERRUPTED: '任务被中断',
    E_TIMEOUT: '长时间没有响应',
    E_UNKNOWN: '操作失败，可以查看日志',
    E_NETWORK: '无法连接 OpenNavo 服务',
    E_PROTECTED: '这个 App 受保护，不能卸载',
    E_APP_MANAGEMENT: '需要先允许 OpenNavo 管理 App',
    E_UNINSTALL_INCOMPLETE: '应用仍然存在，卸载未完成；请查看日志后重试',
    E_PLAN_EXPIRED: '文件有变化，请重新扫描',
    E_SCAN_CANCELED: '已取消扫描',
    E_RESTORE_UNAVAILABLE: '有些文件已经不在废纸篓里，没能放回',
    E_TRASH_FAILED: '没能移到废纸篓',
    E_MONITOR_UNAVAILABLE: '读不到这台 Mac 的系统信息',
    E_FAN_UNSUPPORTED: '这台 Mac 的芯片暂不支持风扇控制',
    E_FAN_HELPER_NOT_ENABLED: '风扇辅助程序还没有安装或允许',
    E_FAN_HELPER_UNSIGNED: '这份 OpenNavo 没有正式签名（比如开发版），装不了风扇辅助程序。请用正式安装包',
    E_FAN_HELPER_UNAVAILABLE: '连不上风扇辅助程序，请稍后再试',
    E_FAN_WRITE_FAILED: '没能改变风扇设置',
    E_FAN_NO_MANUAL_SPEED: '还没有上次的手动转速',
    E_FAN_UNAUTHORIZED: '风扇辅助程序没通过签名校验，请重新安装 OpenNavo',
    E_FAN_SENSOR_UNAVAILABLE: '读不到温度，为了安全不能切到手动',
    E_INSTALL_THREAT: '安装包里发现威胁，已停止安装',
    E_INSTALL_SCAN: '安装前扫描没能完成，没有安装。可以稍后重试，或在「设置 → 安全防护」里关闭安装前扫描',
    E_PASTE_EXPIRED: '这次粘贴已经过期，已自动丢弃',
    E_ACCESSIBILITY_DENIED: '需要在系统设置里允许 OpenNavo 使用辅助功能',
    E_VT_DISABLED: 'VirusTotal 查询没有开启',
    E_VT_KEY_REQUIRED: '请先在设置里填写 VirusTotal API key',
    E_VT_KEY_INVALID: 'VirusTotal API key 无效，请检查后重新填写',
    E_VT_RATE_LIMITED: 'VirusTotal 查询太频繁了，公共 API 每分钟最多 4 次，请稍后再试',
    E_VT_UNAVAILABLE: '暂时连不上 VirusTotal，请稍后再试',
    E_KEYCHAIN: '没能读写钥匙串',
    E_AUTOMATION_DENIED: '需要在「系统设置 → 隐私与安全性 → 自动化」里允许 OpenNavo 控制 System Events',
    E_STARTUP_CHANGED: '这个启动项刚刚被其他程序改过，请刷新后再试',
    E_LOGIN_ITEM_EXISTS: '这个 App 已经会在登录时打开',
    E_ROLLBACK_UNAVAILABLE: '保留的旧版本已不可用，或这个 App 之后又有变化，不能回滚',
    E_ROLLBACK_INTEGRITY: '保留的旧版本没有通过签名校验，没有回滚',
    E_FW_NOT_BUNDLED: '这个版本没有包含防火墙组件，请使用 OpenNavo 正式版',
    E_FW_NOT_IN_APPLICATIONS: '请先把 OpenNavo 移到「应用程序」文件夹，再重新打开',
    E_FW_ACTIVATION: 'macOS 没有完成系统扩展的安装，请重试',
    E_FW_REMOVAL: '没能移除应用防火墙，请重试',
    E_FW_FILTER: 'macOS 没能更新网络过滤设置，请重试',
    E_FW_CONSENT_DENIED: '没有允许过滤网络内容，防火墙没有开启',
    E_FW_DISCONNECTED: '防火墙组件没有响应，请稍后重试',
    E_FW_RULE_INVALID: '规则不正确：请检查程序、地址和端口',
    E_FW_UNKNOWN_ALERT: '这个连接已经处理过了',
    E_FW_BLOCKLIST_URL: '只支持 https 网址或这台 Mac 上的文件',
    E_FW_BLOCKLIST_FETCH: '没能下载屏蔽列表，请检查网址和网络',
    E_FW_BLOCKLIST_FORMAT: '没有在文件里找到可用的域名或 IP',
    E_FW_BLOCKLIST_TOO_LARGE: '屏蔽列表太大了，最多 50 MB、100 万条',
    E_FW_BLOCKLIST_LIMIT: '屏蔽列表已经到上限：最多 32 个，合计不超过 256 MB',
    E_FW_BLOCKLIST_FILE: '找不到这个文件，或者没有权限读取'
  },
  installConfirm: {
    title: '接管现有 {name}？',
    description: '检测到安装位置已存在 App。确认后将尝试交由 Homebrew 管理，成功后可通过 OpenNavo 更新和卸载。',
    matchHint: '只有现有应用与安装包内容一致时才能接管；不一致时会失败，不会强制覆盖。',
    confirm: '确认接管',
    blockedTitle: '暂时无法安装 {name}',
    blockedDescription: '无法确认安装位置或现有应用的状态。未执行安装或接管，请检查应用与安装信息后重试。'
  },
  tapConfirm: {
    title: '从第三方仓库安装 {name}？',
    description: '{name} 不在 Homebrew 官方仓库里，由下面这个来源仓库的作者维护，没有经过 Homebrew 审核。',
    hint: '第一次安装时，Homebrew 会从 GitHub 下载这个仓库并信任该应用，镜像源对它不起作用。请只在信任作者时继续。',
    confirm: '继续安装'
  },
  runningApps: {
    title: '应用正在运行',
    description: '请先保存工作，再退出应用进行更新。',
    batchDescription: '以下应用正在运行，请先保存工作。你可以跳过这些应用，或退出后一起更新。',
    later: '稍后',
    skip: '跳过这些应用',
    quit: '退出并更新',
    quitAll: '退出这些应用并更新',
    reopen: '更新成功后重新打开',
    quitHint: '如果应用未退出或你取消退出，将暂缓该项更新，不会强制关闭。',
    deferred: '已暂缓'
  },
  page: {
    comingSoon: '页面内容在后续任务中接入。'
  },
  appInfo: {
    title: '简介',
    open: '打开',
    reveal: '在访达中显示',
    detail: '在 OpenNavo 中查看',
    appStore: 'App Store 详情',
    adoptTitle: '可以交给 Homebrew 管理',
    adoptBody: 'Homebrew 收录了 {name}。接管后能在 OpenNavo 里一键更新，卸载时也能用上更全的清理规则。',
    adopt: '用 Homebrew 接管',
    footprint: '占用空间',
    estimate: '预估',
    footprintCaption: '空间占用',
    appBytes: 'App 本体',
    dataBytes: '应用数据',
    cacheBytes: '缓存',
    footprintIncomplete: '没有完全磁盘访问权限，部分数据没有算进去。',
    footprintPartial: '有些文件读不到，实际占用可能更大。',
    fields: {
      path: '位置',
      bundleId: 'Bundle ID',
      version: '版本',
      vendor: '开发者',
      arch: '架构',
      source: '来源',
      updater: '更新方式',
      installedAt: '安装于',
      lastUsed: '最近使用'
    },
    updaterNone: 'OpenNavo 暂时不能更新它',
    arch: {
      arm64: 'Apple 芯片',
      x86_64: 'Intel',
      universal: '通用'
    },
    protected: {
      self: 'OpenNavo 不能卸载自己，可以在访达里把它移到废纸篓。',
      system: '这是 macOS 的一部分，不能卸载。',
      managed: '这个 App 由管理员管理，不能在这里卸载。'
    },
    uninstall: '卸载',
    unknown: '未知'
  },
  uninstall: {
    title: '卸载 {name}？',
    titleMany: '卸载 {count} 个 App？',
    runningTitle: '正在卸载 {name}',
    runningTitleMany: '正在卸载 {count} 个 App',
    doneTitle: '卸载完成',
    failedTitle: '扫描没有完成',
    command: '将执行 {command}，再清理下面勾选的相关文件。',
    scanning: '正在查找相关文件…',
    checked: '已检查 {checked} / {total} 个位置',
    selection: '已选 {selected} / {total} 项 · {size}',
    admin: '{count} 项需要管理员密码',
    sharedKept: '{count} 项共用，已保留',
    selectAll: '全选',
    onlyApp: '只删 App',
    recommended: '恢复建议',
    groups: {
      app: 'App 本体',
      app_data: '应用数据',
      caches: '缓存',
      preferences: '偏好设置与窗口状态',
      logs: '日志与崩溃报告',
      background: '后台项目',
      cli: '命令行工具链接',
      plugins: '插件、驱动与扩展',
      receipts: '安装记录',
      maybe: '可能相关 · 请确认'
    },
    groupHints: {
      app_data: 'Application Support · Containers · Group Containers',
      caches: 'Caches · HTTPStorages · 临时文件',
      preferences: 'Preferences · Saved Application State · Cookies',
      logs: 'Logs · DiagnosticReports',
      background: 'LaunchAgents · LaunchDaemons · 提权助手',
      cli: '指向 App 内部的符号链接',
      plugins: 'Internet Plug-Ins · Audio · 共享组件',
      receipts: 'pkgutil 安装回执',
      maybe: '只是名称相似，默认不勾选'
    },
    itemCount: '{count} 项',
    unselected: '未选 · {size}',
    files: '{count} 个文件',
    link: '链接',
    reveal: '在访达中显示',
    required: '必须移除',
    adminHint: '位于系统目录，执行时需要管理员密码',
    shared: '共用',
    sharedWith: '也被 {names} 使用，默认保留',
    reasons: {
      app_bundle: 'App',
      brew_uninstall: 'Homebrew 卸载步骤',
      zap_rule: 'Homebrew 规则',
      bundle_id: 'Bundle ID',
      app_group: 'App Group',
      launchd: '后台服务',
      symlink: '指向 App',
      pkg_receipt: '安装回执',
      team_id: '同一开发者',
      name_exact: '名称一致',
      name_similar: '名称相似'
    },
    reasonHints: {
      app_bundle: 'App 本身',
      brew_uninstall: 'brew uninstall 一定会处理，不能取消',
      zap_rule: '来自 Homebrew Cask 的 zap 清单（人工维护）',
      bundle_id: '文件名与 App 或其内置组件的 Bundle ID 一致',
      app_group: 'App 签名里声明的共享容器',
      launchd: 'launchd 配置指向这个 App 的程序',
      symlink: '链接目标在 App 内部，卸载后会失效',
      pkg_receipt: 'pkgutil 记录显示由这个 App 的安装包写入',
      team_id: '厂商目录，签名的 Team ID 一致',
      name_exact: '文件夹名与 App 名称完全一致',
      name_similar: '只是名称相似，默认不勾选'
    },
    phases: {
      preparing: '正在退出 App',
      uninstalling: '正在移除 App 本体',
      cleaning: '正在清理相关文件',
      finishing: '正在写入记录'
    },
    notices: {
      rescanned: '文件有变化，已经重新扫描。',
      appManagement: '需要先允许 OpenNavo 管理 App，才能删除不是用 Homebrew 安装的 App。',
      incomplete: '没有「完全磁盘访问权限」，{count} 个位置没能检查：{paths}。开启后能清理得更干净。',
      running: '{names} 正在运行，卸载前会先退出。',
      siblings: '同一个 Homebrew 包（{cask}）里的 {names} 也会一起卸载。',
      brewSudo: 'Homebrew 的卸载步骤可能会单独请求一次管理员密码。',
      personal: {
        chat_history: '{name} 的应用数据里有聊天记录、图片和文件（约 {size}）。需要保留的话，请先在 App 里备份。',
        documents: '{name} 的应用数据里有你的文稿和项目文件（约 {size}）。需要保留的话，请先另存或导出。',
        media: '{name} 的应用数据里有照片或音视频资源（约 {size}）。需要保留的话，请先导出。',
        browser_profile: '{name} 的应用数据里有书签、历史记录和扩展（约 {size}）。登录了账号的话，重装后可以同步回来。',
        vm_disk: '{name} 的应用数据里有虚拟机磁盘、镜像和数据卷（约 {size}），删除后需要重新下载。',
        other: '{name} 的应用数据里可能有你的个人内容（约 {size}）。需要保留的话，请先备份。'
      },
      uninstaller: '{name} 有官方卸载程序，它能一并处理授权和共享组件。'
    },
    mode: {
      label: '删除方式',
      trash: '移到废纸篓',
      delete: '直接删除',
      deleteWarning: '直接删除无法撤销'
    },
    confirm: '卸载',
    confirmClean: '卸载并清理 · {size}',
    confirmDelete: '卸载并永久删除 · {size}',
    confirmMany: '卸载 {count} 个 App · {size}',
    cancel: '取消',
    retry: '重新扫描',
    scanFailed: '扫描没有完成：{reason}',
    background: '在后台继续',
    backgroundHint: '关闭窗口不会中断，完成后会通知你。',
    log: '显示日志',
    hideLog: '收起日志',
    waiting: '排队中',
    batch: {
      meta: 'App + {count} 项相关文件',
      admin: '需要管理员密码',
      exclude: '从这次卸载中去掉',
      include: '加入这次卸载',
      skipped: '不卸载'
    },
    result: {
      titleOne: '{name} 已卸载',
      titleMany: '已卸载 {count} 个 App',
      heroTrash: '文件在废纸篓里，清倒之前随时可以撤销。',
      heroDelete: '文件已经永久删除。',
      freed: '可释放空间',
      freedNow: '已释放空间',
      freedHint: '清倒废纸篓后生效',
      freedNowHint: '已生效',
      moved: '已移到废纸篓',
      deleted: '已删除',
      countHint: '包含 App 本体',
      skipped: '跳过',
      skippedNone: '没有问题',
      skippedSee: '原因见下方',
      skippedTitle: '这些项目没能处理',
      skippedHint: '稍后可以在「清理电脑」里再清理一次。',
      failed: '{name} 没有卸载：{reason}',
      quitFailed: '它没有退出，可能在等你保存修改。切换过去保存或关闭后再试。',
      finished: '已完成',
      notFinished: '没有完成',
      retry: '重新卸载',
      undo: '撤销卸载',
      purge: '彻底删除这些文件',
      done: '完成',
      brewNote:
        'Homebrew 卸载的 App 撤销后，文件会放回原处，但 Homebrew 记录不会恢复；它会显示为「手动安装 · 可接管」。'
    },
    skipReasons: {
      in_use: '文件正在被使用',
      permission: '没有权限',
      admin_canceled: '没有提供管理员密码',
      missing: '文件已经不在了',
      changed: '扫描后文件有变化，为了安全跳过',
      protected: '受保护的位置',
      running: 'App 正在运行',
      tool_unavailable: '工具没有运行，打开后重试'
    },
    toasts: {
      done: '已卸载 {name}',
      doneMany: '已卸载 {count} 个 App',
      doneBody: '{count} 项已移到废纸篓 · 可释放 {size}',
      deletedBody: '已删除 {count} 项 · 释放 {size}',
      undo: '撤销',
      undone: '已撤销卸载',
      undoneBody: '{count} 项已放回原处',
      undoMissing: '{count} 个文件没能放回',
      purged: '已彻底删除，释放 {size}',
      needsFullDisk: '读取废纸篓需要「完全磁盘访问权限」。'
    }
  },
  cleanup: {
    title: '清理电脑',
    rescan: '重新扫描',
    subtitle: {
      idle: '找出缓存、日志和已删除 App 的残留，确认后再清理',
      scanning: '正在扫描：只读，不会删除任何东西',
      review: '可释放 {size} · {time}扫描',
      running: '正在清理，可以离开这个页面',
      done: '清理完成',
      failed: '扫描没有完成'
    },
    overview: {
      title: '看看这台 Mac 能腾出多少空间',
      titleAgain: '过段时间再扫一遍',
      body: '扫描只读，大约 30 秒。找到的内容按风险分三档，默认只勾可再生的和足够旧的。',
      scan: '开始扫描',
      never: '还没有扫描过',
      lastScan: '上次扫描：{time}',
      lastResult: '上次扫描（{time}）还有 {size} 可以清理'
    },
    risks: {
      rebuildable: '可再生',
      review: '需要确认',
      personal: '个人文件'
    },
    riskHints: {
      rebuildable: '直接删除，空间立刻释放；删了会自动重建',
      review: '只预选够旧的；清理前请看一眼',
      personal: '只列出来，默认不选；选了会移到废纸篓'
    },
    categories: {
      app_caches: '应用缓存',
      logs: '日志与诊断报告',
      browser_caches: '浏览器缓存',
      dev_caches: '开发工具缓存',
      homebrew: 'Homebrew 下载缓存与旧版本',
      temp: '临时文件',
      leftovers: '已删除 App 的残留',
      installers: '旧安装包',
      project_artifacts: '项目构建产物',
      trash: '废纸篓',
      device_backups: 'iPhone / iPad 备份与 Xcode 归档',
      large_files: '大文件与旧下载',
      docker: 'Docker 可回收空间'
    },
    categoryHints: {
      app_caches: 'App 自己生成的缓存，删掉后会自动重建',
      logs: '7 天前的 App 日志和崩溃报告',
      browser_caches: '只清缓存，不碰历史记录、Cookie、密码和扩展',
      dev_caches: '下次构建或安装时会重新生成、重新下载',
      homebrew: '等同 brew cleanup：旧版本和下载缓存',
      temp: '3 天没动、没被打开的临时文件',
      leftovers: 'App 已经不在这台 Mac 上，这些文件还留着',
      installers: '下载和桌面里的 dmg、pkg 安装包；对应 App 已经装好的先选上',
      project_artifacts: '30 天没改动的项目里的 node_modules、target、build 等；重新安装依赖或重新构建即可恢复',
      trash: '勾选即清空，无法恢复',
      device_backups: '清倒废纸篓后，备份不能再用来恢复设备，归档不能再用来解析崩溃日志',
      large_files: '超过 1 GB、90 天没打开的文件',
      docker: '由 Docker 自己的清理命令删除，默认不选；数据卷只显示、不清理'
    },
    scan: {
      starting: '正在准备扫描',
      scanning: '正在扫描{name}',
      found: '已找到 {size}',
      readOnly: '只读，不会删除任何东西',
      cancel: '取消'
    },
    summary: {
      reclaimable: '可释放',
      excludesPersonal: '不含个人文件',
      selected: '已选 {count} 项',
      breakdown: '直接删除 {delete} · 移到废纸篓 {trash}',
      onlyRebuildable: '只选可再生',
      recommended: '恢复建议',
      inUse: '另有 {size} 正在使用'
    },
    notices: {
      rescanned: '结果已更新，请再确认一次。',
      services: '已删除的 App（{names}）还有 {count} 个后台项目在开机时加载，只会报错、耗电，建议清理。',
      incomplete: '没有「完全磁盘访问权限」，这些位置没检查：{paths}。开启后能找到更多。'
    },
    category: {
      atLeast: '至少 {size}',
      selected: '已选 {size}',
      none: '未选',
      allSkipped: '全部跳过',
      skipped: '已跳过 {count} 项，只展示、不可选',
      lastModified: '最后修改：{time}',
      servicesLoading: '{count} 个后台项目仍在开机时加载',
      inUse: '{size} 正在使用'
    },
    skipReasons: {
      running: '正在运行，退出后可以清理',
      in_use: '正在使用',
      busy: '工具正在运行',
      recent: '最近有改动',
      git_tracked: '里面有 Git 跟踪的文件',
      protected: '受保护',
      permission: '没有权限',
      tool_stopped: '没有运行，打开后重新扫描就能看到可回收空间',
      volumes: '数据卷可能存着数据库等数据，只显示、不清理'
    },
    reasons: {
      orphan_service: '后台服务',
      installer_installed: '已安装',
      device_support_latest: '最新版本',
      device_support_latestHint: '默认保留。清理后，下次连接这台设备时 Xcode 会重新准备，需要几分钟。',
      tool_download_cache: '要重新下载',
      tool_download_cacheHint: '可以重建，但下次构建或测试时要重新下载，可能要等较久、用掉不少流量，所以默认不勾。',
      docker_images: '未使用的镜像',
      docker_imagesHint: '没有任何容器在用的镜像，需要时会重新拉取或构建。',
      docker_build_cache: '构建缓存',
      docker_build_cacheHint: '下次构建镜像会慢一些，构建结果不受影响。',
      docker_containers: '已停止的容器',
      docker_containersHint: '容器里自己写入的改动会一起删除，数据卷不受影响。'
    },
    item: {
      command: '命令',
      admin: '需要管理员密码',
      reveal: '在访达中显示',
      protect: '始终保留',
      keptOpen: '保留 {count} 个正在使用的文件'
    },
    footer: {
      selected: '已选 {count} 项 · {size}',
      both: '{delete} 直接删除，无法撤销；{trash} 移到废纸篓，可以撤销',
      deleteOnly: '{size} 直接删除，无法撤销',
      trashOnly: '全部移到废纸篓，可以撤销',
      nothing: '还没有选择',
      clean: '清理 {size}'
    },
    confirm: {
      title: '清理 {size}？',
      count: '共 {count} 项',
      delete: '{size} 直接删除：缓存、日志、开发工具缓存等，删了会自动重建，无法撤销。',
      trash: '{size} 移到废纸篓：已删除 App 的残留等，完成后可以撤销。',
      emptyTrash: '你勾选了「废纸篓」：里面现有的东西会被清空，无法恢复。',
      admin: '其中 {count} 项是系统级的后台项目，会请求一次管理员密码。',
      cancel: '取消',
      clean: '清理'
    },
    run: {
      title: '正在清理',
      delete: '直接删除',
      trash: '移到废纸篓',
      command: '执行 {command}',
      hint: '清理是任务队列里的一个任务，离开这个页面不会中断，完成后会通知你。'
    },
    result: {
      title: '已释放 {size}',
      failedTitle: '清理没有完成',
      trashPending: '另有 {size} 在废纸篓里，清倒后释放；清倒前随时可以撤销。',
      rebuilds: '可再生的文件会在需要时自动重建。',
      freed: '已释放',
      freedHint: '立即生效',
      trashed: '移到废纸篓',
      trashedHint: '{count} 项 · 清倒后释放',
      trashedNone: '这次没有',
      restored: '已撤销，放回原处',
      skipped: '跳过',
      skippedCount: '{count} 项',
      skippedHint: '原因见下方',
      skippedNone: '没有问题',
      skippedTitle: '这些没有清理',
      free: '可用空间',
      undo: '撤销移到废纸篓的项目',
      purge: '彻底删除这些文件',
      done: '完成'
    },
    toasts: {
      protected: '已加入保护名单',
      protectedBody: '以后扫描不会再出现',
      undo: '撤销',
      restored: '已撤销',
      restoredBody: '{count} 项放回了原处',
      purged: '已彻底删除',
      purgedBody: '又释放了 {size}',
      done: '清理完成',
      doneBody: '已释放 {size}',
      view: '查看'
    },
    failed: {
      title: '扫描没有完成'
    },
    rail: {
      disk: '磁盘',
      diskSub: '{name} · 共 {size}',
      free: '可用 {size}',
      diskUnknown: '暂时读不到磁盘空间。',
      used: '已用',
      reclaimable: '可清理',
      available: '可用',
      last: '上次扫描',
      lastScan: '{time} · 可释放 {size}',
      lastNone: '还没有扫描过。每次清理都会记进任务记录，可以随时查看。',
      history: '查看任务记录',
      protected: '保护名单',
      protectedCount: '{count} 项',
      protectedBody:
        '名单里的文件和 App 不会出现在结果里。密码管理器、VPN、AI 工具、虚拟机等类别本来就受保护，不用手动添加。',
      manage: '管理'
    },
    entry: {
      title: '清理电脑',
      description: '找出缓存、日志和已删除 App 的残留，确认后再清理。',
      found: '可释放 {size} · {time}扫描。',
      active: '{count} 个已删除 App 的后台项目仍在开机时加载',
      scan: '开始扫描',
      open: '去清理'
    },
    settings: {
      rebuildable: '可再生的文件',
      rebuildableHint: '缓存、日志、开发工具缓存：直接删除，空间立刻释放',
      fixed: '固定',
      deleteMode: '需要确认的文件',
      deleteModeHint: '已删除 App 的残留等：移到废纸篓可以撤销；直接删除立即释放空间',
      protected: '保护名单',
      added: '{date} 添加',
      remove: '移除',
      add: '添加文件夹…',
      empty: '还没有添加。在清理结果里点「始终保留」也会加到这里。',
      always:
        '钥匙串和密码管理器、VPN 和代理工具、AI 工具、虚拟机和容器的磁盘文件、浏览器历史和 Cookie、iCloud 云盘始终受保护，不用手动添加。'
    },
    groups: {
      deviceSupport: 'Xcode 设备支持文件 · {platform}',
      temp: '系统临时文件夹',
      trash: '废纸篓里的项目',
      installers: '下载和桌面',
      xcodeArchive: 'Xcode 归档',
      deviceBackup: '设备备份',
      deviceBackupModel: '设备备份 · {model}'
    },
    skipReasonsBy: {
      running: '{names} 正在运行，退出后可以清理',
      in_use: '{names} 正在使用',
      busy: '{names} 正在运行'
    }
  },
  monitor: {
    title: '电脑监控',
    subtitle: '{model} · {chip} · {memory} 内存 · macOS {os} · 已运行 {uptime}',
    subtitleLoading: '正在读取这台 Mac 的信息',
    menuBarButton: '菜单栏 · {count} 项',
    waiting: '正在读取…',
    modules: {
      cpu: 'CPU',
      gpu: 'GPU',
      memory: '内存',
      disk: '磁盘',
      network: '网络',
      battery: '电池',
      sensors: '传感器',
      fans: '风扇'
    },
    moduleHints: {
      cpu: '使用率',
      gpu: '使用率',
      memory: '已用比例',
      disk: '可用空间',
      network: '上传 / 下载速度',
      battery: '电量',
      sensors: '最热的传感器'
    },
    intro: {
      title: '电脑监控已经放到菜单栏',
      body: 'CPU 和内存各是一个菜单栏项，点它们看详情；想放别的指标就在电脑监控里打开，数量不限。不需要可以在设置里关掉。',
      view: '查看'
    },
    health: {
      good: '一切正常',
      goodBody: 'CPU、内存、磁盘和电池都在正常范围',
      goodBodyNoBattery: 'CPU、内存和磁盘都在正常范围',
      attention: '{count} 项需要注意',
      critical: '{count} 项需要尽快处理',
      issues: {
        cpuBusy: 'CPU 已经连续 3 分钟超过 85%',
        memoryPressure: '内存压力偏高',
        memoryCritical: '内存压力紧急，App 可能变慢或被系统关闭',
        diskLow: '磁盘只剩 {size}',
        batteryHealth: '电池健康度只有 {percent}',
        batteryHot: '电池温度 {temp}',
        temperature: 'CPU 温度 {temp}',
        uptime: '已经 {count} 天没有重启',
        diskSmart: '「{name}」的 SMART 状态异常，请尽快备份'
      },
      actions: {
        processes: '查看进程',
        cleanup: '去清理电脑',
        fans: '调整风扇'
      }
    },
    cpu: {
      split: '用户 {user} · 系统 {system}',
      load: '负载',
      loadLong: '负载（1、5、15 分钟）',
      coresLabel: '核心',
      cores: '{p} 个性能核 + {e} 个能效核',
      performanceLegend: '{count} 个性能核（橙）',
      efficiencyLegend: '{count} 个能效核（紫）',
      uptime: '运行时间',
      power: '功耗'
    },
    gpu: {
      cores: '{chip} · {count} 核 GPU',
      model: '型号',
      noProcesses: 'macOS 不提供按进程的 GPU 占用，这里不显示进程。',
      power: '功耗'
    },
    memory: {
      summary: '{used} / {total} · 内存压力{pressure}',
      pressure: {
        normal: '正常',
        warning: '偏高',
        critical: '紧急'
      },
      app: 'App 内存',
      wired: '联动内存',
      compressed: '已压缩',
      cached: '缓存的文件',
      swap: '交换'
    },
    disk: {
      volume: '{name} · 共 {total}',
      read: '读取',
      write: '写入',
      readAt: '读取 {value}',
      writeAt: '写入 {value}',
      volumes: '卷',
      internal: '内置',
      external: '外接',
      volumeFree: '{kind} · 可用 {free} / {total}',
      cleanup: '上次扫描可释放 {size} · 去清理电脑',
      cleanupNever: '去清理电脑',
      devices: '磁盘设备',
      smartLabel: 'SMART 状态',
      smart: {
        verified: '已验证',
        failing: '异常',
        unsupported: '不支持'
      },
      wear: '磨损',
      powerOn: '通电时间',
      written: '累计写入',
      temperature: '温度'
    },
    network: {
      down: '下载',
      up: '上传',
      interface: '接口',
      localIp: '本机 IP',
      totalDown: '开机以来下载',
      totalUp: '开机以来上传',
      link: '{kind}（{name}）',
      kinds: {
        wifi: 'Wi-Fi',
        ethernet: '有线',
        vpn: 'VPN',
        other: '其他'
      },
      privacy: '不显示公网 IP 和 Wi-Fi 名称：前者要请求外部服务器，后者要定位权限。'
    },
    battery: {
      charging: '正在充电',
      chargingTime: '正在充电 · 还需 {time} 充满',
      onAc: '已接通电源',
      onBattery: '使用电池',
      onBatteryTime: '使用电池 · 剩余 {time}',
      health: '健康度',
      cycles: '循环次数',
      designCapacity: '设计容量',
      maxCapacity: '当前最大容量',
      temperature: '温度',
      voltage: '电压',
      power: '功率',
      adapter: '电源适配器'
    },
    sensors: {
      hottest: '{name} · 当前最热',
      temperatures: '温度',
      note: 'Apple 芯片的传感器是热区，不对应某一个核心。',
      names: {
        cpuPerformance: 'CPU 性能核心',
        cpuEfficiency: 'CPU 能效核心',
        cpu: 'CPU',
        gpu: 'GPU',
        soc: '芯片',
        ssd: 'SSD',
        battery: '电池'
      }
    },
    fans: {
      control: '控制',
      auto: '自动',
      manual: '手动',
      modeAuto: '自动 · 系统控制',
      modeManual: '手动 · {rpm}',
      all: '所有风扇',
      min: '最小 {rpm}',
      max: '最大 {rpm}',
      range: '最小 {min} · 最大 {max}',
      rangeLabel: '转速范围',
      single: '风扇',
      left: '左侧风扇',
      right: '右侧风扇',
      numbered: '风扇 {n}',
      sync: '同步所有风扇',
      full: '全速',
      restore: '恢复自动',
      restoreLast: '恢复上次的手动转速',
      autoNote: '系统根据温度自动调节。切到「手动」可以自己设定转速。',
      helperTitle: '手动控制风扇要先装一个辅助程序',
      helperBody:
        '它只做风扇控制，以系统身份在后台运行，只接受 OpenNavo 的指令。安装后按系统提示在「登录项与扩展」里允许，可能要输一次管理员密码。',
      install: '安装辅助程序',
      approvalTitle: '还差一步',
      approvalBody: '在「系统设置 → 通用 → 登录项与扩展」里允许 OpenNavo 在后台运行，然后点「我已允许」。',
      recheck: '我已允许',
      brokenTitle: '风扇辅助程序没有响应',
      brokenBody: '它已经装好，但 OpenNavo 连不上它。重新安装一般就能恢复。',
      reinstall: '重新安装辅助程序',
      unsupported: '这台 Mac 的芯片暂不支持风扇控制，目前只显示转速。',
      safety:
        '退出 OpenNavo、崩溃或无响应 15 秒、睡眠、写入失败时，风扇自动交还系统控制；温度持续 10 秒达到 95 °C 时，手动风扇会拉到全速。没有「关闭风扇」，最低是系统最小转速。',
      reasons: {
        user: '你调整了风扇',
        app_quit: 'OpenNavo 退出了',
        heartbeat_lost: 'OpenNavo 没有响应',
        sleep: 'Mac 进入了睡眠',
        wake_reapplied: '唤醒后重新应用了手动转速',
        overheat: '温度持续 10 秒达到 95 °C',
        write_failed: '写入风扇设置失败',
        helper_started: '辅助程序重新启动了',
        helper_uninstalled: '辅助程序已卸载'
      },
      toasts: {
        manual: '风扇已切到手动',
        manualBody: '退出 OpenNavo 或睡眠时会回到自动；持续过热时会拉到全速',
        auto: '风扇已交还系统控制',
        full: '风扇已拉到全速',
        reverted: '风扇已回到自动',
        ready: '风扇辅助程序已就绪',
        readyBody: '切到「手动」就能自己设定转速'
      }
    },
    processes: {
      topCpu: '占用 CPU 最多的进程',
      topMemory: '占用内存最多的进程',
      byCpu: 'CPU',
      byMemory: '内存',
      sortBy: '排序方式',
      system: '系统',
      background: '后台',
      quit: '退出',
      quitTitle: '退出 {name}？',
      quitBody: '和在 {name} 里按 ⌘Q 一样，未保存的内容由 App 自己提示。',
      quitDone: '已让 {name} 退出',
      activityMonitor: '在活动监视器中打开',
      activityValue: '活动监视器口径 {value}',
      topDisk: '磁盘读写最多的进程',
      topNetwork: '网络流量最多的进程',
      diskSplit: '读 {read} · 写 {write}',
      scaleCpu: '占整机比例 · 1 核 ≈ {percent}',
      cores: '≈{value} 核',
      self: '本应用',
      view: '显示方式',
      viewProcess: '进程',
      viewApp: '应用',
      breakdown: {
        title: '整机 CPU 的去向',
        rest: '其余进程（{count}）',
        restAny: '其余进程',
        finished: '已结束的短命进程',
        finishedFrom: '主要来自 {names}',
        other: '系统与 root 进程',
        hintRest: '还在运行，但没有排进上面名单的进程。',
        hintFinished: '这些进程在两次采样之间已经结束，没法逐个列出；这里是它们的父进程回收后统计到的 CPU 时间。',
        hintOther: 'OpenNavo 无权读取内核、root 和其他用户进程的 CPU；这里是整机占用减去上面各项后的余量。'
      }
    },
    detail: {
      open: '查看{name}详情',
      minuteAgo: '60 秒前',
      now: '现在',
      range60: '最近 60 秒',
      range1h: '最近 1 小时',
      rangeLabel: '曲线范围',
      hourAgo: '1 小时前'
    },
    menuBar: {
      title: '菜单栏',
      show: '在菜单栏显示',
      showHint: '单独一项，点它在下方弹出这一项的详情',
      style: '样式',
      styleHint: '菜单栏里的样子',
      styles: {
        text: '两行文字',
        value: '只显示数值',
        line: '迷你折线',
        bars: '柱状'
      },
      on: '菜单栏',
      onHint: '已在菜单栏显示',
      added: '已把「{name}」放到菜单栏',
      addedBody: '点它就能在下方看到这一项的详情',
      removed: '已从菜单栏移除「{name}」',
      keepTitle: '关闭窗口时菜单栏项会一起消失',
      keepBody: '现在关闭主窗口会退出 OpenNavo。打开「关闭窗口后留在菜单栏」，监控项就会一直显示。',
      keepAction: '一起打开'
    },
    popover: {
      open: '在 OpenNavo 中查看',
      settings: '菜单栏设置'
    },
    rail: {
      menuBar: '菜单栏',
      menuBarSub: '每项单独显示，点它看详情 · 数量不限',
      empty: '菜单栏里还没有监控项',
      crowded: '项目多时，放不下的会被刘海挡住；可以把样式改成「只显示数值」。',
      refresh: '每 {count} 秒刷新 · 按住 ⌘ 拖动调整顺序',
      adjustFans: '调整风扇',
      privacy: '数据只在这台 Mac 上',
      privacyBody: '不联网、不上传、不保存历史；曲线只留在内存里。'
    },
    settings: {
      menuBar: '菜单栏',
      interval: '刷新频率',
      intervalHint: '越快越耗电；屏幕关闭和睡眠时自动暂停',
      seconds: '{count} 秒',
      notVisible: '开启后没看到？',
      notVisibleHint: 'macOS 26 起要在「系统设置 → 菜单栏」里允许 OpenNavo；项目太多时，放不下的会被刘海挡住',
      openSettings: '打开系统设置',
      menuBarNote:
        '每个指标都是独立的菜单栏项，数量不限；默认开启 CPU 和内存。点任意一项，就在它下面弹出这一项的详情。',
      helper: '辅助程序',
      helperHint: '{status}。只用来控制风扇；没安装时照常显示温度和转速',
      helperStatus: {
        unsupported: '这台 Mac 没有风扇',
        not_installed: '未安装',
        requires_approval: '等待在「登录项与扩展」里允许',
        enabled: '已启用',
        error: '辅助程序出错，可以重新安装'
      },
      install: '安装',
      reinstall: '重新安装',
      uninstall: '卸载',
      autoRestore: '自动交还系统控制',
      autoRestoreHint:
        '退出、崩溃或无响应 15 秒、睡眠、写入失败时，风扇回到自动；温度持续 10 秒达到 95 °C 时，手动风扇拉到全速',
      always: '始终开启',
      relaunch: '重新打开 OpenNavo 时',
      relaunchHint: '风扇从自动开始；传感器详情里可以一键恢复上次的手动转速',
      privacy: '所有指标只在这台 Mac 上计算和显示：不联网、不上传、不保存历史；不显示公网 IP 和 Wi-Fi 名称。',
      alerts: '提醒',
      alertsHint:
        '出现需要注意的问题时发通知，同一问题 1 小时内只提醒一次。开启后，即使菜单栏没有监控项也会在后台低频检查'
    }
  },
  security: {
    title: '安全防护',
    subtitle: '体检、病毒扫描、启动项和实时防护，结果只保存在这台 Mac 上',
    tabs: {
      label: '安全防护',
      overview: '概览',
      scan: '病毒扫描',
      startup: '启动项',
      protection: '实时防护',
      quarantine: '隔离区'
    },
    overview: {
      attention: '有 {count} 项需要你看一下',
      newStartup: '新增 {count} 个开机启动项',
      openThreats: '{count} 个威胁还没有处理',
      allGood: '一切正常',
      allGoodHint: '系统防护都已开启，也没有待处理的威胁或新启动项',
      viewStartup: '查看启动项',
      postureTitle: '系统安全体检',
      postureHint: '只读检查；需要修改时，会打开系统设置里对应的面板',
      openSettings: '打开设置',
      scheduleOff: '定时扫描：关闭',
      scheduleDaily: '定时扫描：每天 {time}',
      scheduleWeekly: '定时扫描：每{day} {time}'
    },
    postureState: {
      ok: '正常',
      warn: '注意',
      bad: '风险',
      info: '提示',
      unknown: '未知'
    },
    posture: {
      sip: {
        title: '系统完整性保护（SIP）',
        ok: '已开启',
        bad: '系统完整性保护已关闭',
        unknown: '无法读取'
      },
      ssv: {
        title: '系统卷签名封存',
        ok: '系统卷已签名封存',
        bad: '系统卷没有封存',
        unknown: '无法读取'
      },
      gatekeeper: {
        title: 'Gatekeeper',
        ok: '只允许运行经过验证的 App',
        bad: 'Gatekeeper 已关闭，任何来源的 App 都能直接运行',
        unknown: '无法读取'
      },
      filevault: {
        title: 'FileVault 磁盘加密',
        ok: '已开启',
        bad: 'FileVault 没有开启，丢失 Mac 时数据可能被读取',
        unknown: '无法读取'
      },
      firewall: {
        title: '防火墙',
        ok: '已开启',
        bad: '防火墙已关闭（它只拦截入站连接，在公共网络建议开启）',
        unknown: '无法读取'
      },
      xprotect: {
        title: 'XProtect（Apple 内置防护）',
        ok: '已是最新',
        bad: 'XProtect 超过 30 天没有更新',
        unknown: '无法读取版本',
        detail: '版本 {version} · {time}更新'
      },
      updates: {
        title: '自动安装安全更新',
        ok: '已开启',
        bad: '安全响应和数据文件不会自动安装',
        unknown: '无法读取'
      },
      screenlock: {
        title: '屏幕锁定',
        ok: '唤醒后立即要求密码',
        bad: '唤醒后不会立即要求密码',
        unknown: '无法读取'
      },
      autologin: {
        title: '自动登录',
        ok: '已关闭',
        bad: '已开启自动登录，开机不需要密码',
        unknown: '无法读取'
      },
      guest: {
        title: '访客用户',
        ok: '已关闭',
        bad: '访客用户已开启',
        unknown: '无法读取'
      },
      sharing: {
        title: '共享服务',
        ok: '没有开启远程共享服务',
        bad: '已开启远程共享服务',
        unknown: '无法读取',
        detail: '已开启：{services}',
        services: {
          ssh: '远程登录',
          files: '文件共享',
          screen: '屏幕共享',
          remote: '远程管理'
        }
      }
    },
    engine: {
      title: '病毒库',
      setUp: '下载病毒库',
      db: '病毒库 {version} · {time}更新',
      downloading: '正在下载病毒库…',
      noDb: '还没有下载病毒库',
      lastScan: '上次{kind}：{date}，发现 {count} 个威胁',
      neverScanned: '还没有扫描过',
      consentTitle: '先下载官方病毒库',
      consentBody: '病毒扫描使用 ClamAV 引擎和它的官方病毒库。病毒库直接从 ClamAV 官方服务器下载，不经过 OpenNavo。',
      consentSize: '首次下载约 113 MB，之后只下载增量，磁盘上约占 170 MB。',
      consentMemory: '扫描时引擎约占 1.1 GB 内存，空闲 5 分钟后自动退出。',
      consentSource: '默认每 4 小时检查一次更新，遵守官方服务器的频率限制。',
      consentCoverage:
        'ClamAV 对 Mac 专用恶意软件覆盖有限，所以 OpenNavo 同时核对签名、公证和启动项，并显示 Apple XProtect 的状态。',
      download: '同意并下载',
      downloadFailed: '病毒库下载失败',
      rateLimited: '病毒库更新被官方服务器暂时限制，稍后会自动重试'
    },
    scan: {
      forceLabel: '强制重新检查所有文件',
      forceHint: '不复用之前的干净结果；此模式下全盘扫描允许每个文件最多检查 5 分钟。',
      performanceHint:
        '全盘扫描最多使用两个扫描进程，会占用更多内存。默认每个文件的扫描预算为 60 秒，超时会标为未完成检查。',
      cacheSummary: '已有 {count} 个文件在核对内容和病毒库版本后复用了干净结果。',
      denied:
        '没有权限读取：{folders}，这些位置没有扫描。可以在「系统设置 → 隐私与安全性 → 文件与文件夹」里允许 OpenNavo',
      folders: {
        downloads: '下载',
        desktop: '桌面',
        documents: '文稿'
      },
      quick: {
        title: '快速扫描',
        duration: '通常几分钟；下载和桌面里有大安装包时会更久',
        body: '启动项指向的程序、下载、桌面、临时文件夹和最近 7 天新装的 App',
        action: '快速扫描'
      },
      full: {
        title: '全盘扫描',
        duration: '可能要 1 小时以上',
        body: '整台 Mac；不扫描系统卷、iCloud 未下载的文件、Time Machine 和网络卷',
        action: '开始全盘扫描'
      },
      custom: {
        title: '自定义扫描',
        duration: '取决于选择的内容',
        body: '选择文件夹或外置硬盘',
        action: '选择并扫描'
      },
      failed: '扫描没有开始',
      running: '正在{kind} · 已检查 {count} 个文件（{size}）',
      state: {
        done: '扫描完成 · 检查了 {count} 个文件 · {time}',
        canceled: '扫描已取消 · 检查了 {count} 个文件',
        failed: '扫描中断 · 检查了 {count} 个文件'
      },
      progressLabel: '扫描进度',
      skipped: {
        dataless: '跳过 {count} 个 iCloud 未下载的文件',
        tooLarge: '{count} 个文件超出扫描上限，没有完整扫描',
        unreadable: '{count} 个文件无法读取（没有权限、已加密或格式不支持）',
        excluded: '{count} 个文件在排除列表里'
      }
    },
    findings: {
      title: '发现的威胁（{count}）',
      quarantine: '隔离',
      quarantined: '已隔离 {name}',
      reveal: '在访达中显示',
      trust: '信任此文件',
      delete: '删除',
      more: '{name} 的更多操作',
      status: {
        open: '待处理',
        quarantined: '已隔离',
        deleted: '已删除',
        trusted: '已信任'
      },
      none: '没有发现威胁',
      noneHint: '病毒库能识别的威胁都没有出现在这次扫描的范围里'
    },
    startup: {
      summary: '{count} 个第三方启动项',
      notRead: '还没有读取过启动项',
      untrusted: '其中 {count} 个未签名或未公证',
      missing: '未能完整检查：{categories}。部分内容无法读取或解析。',
      grant: '开启权限',
      btmUnknown: '这台 Mac 的后台项目数据库格式没有识别出来，部分登录项可能没有列出',
      new: '新',
      toggle: '启用 {name}',
      openSettings: '在系统设置中管理',
      none: '没有启动项',
      noneHint: 'macOS 自带的项目不在这里显示',
      noApps: '没有 App 会自动启动',
      others: '其他 {count} 项',
      othersHint: '命令行工具、脚本、扩展和插件',
      items: '{count} 项',
      stateMixed: '{count} 项已启用',
      add: '添加开机启动项',
      addHint: '选中的 App 会在你登录这台 Mac 时自动打开',
      addSearch: '搜索 App',
      addEmpty: '没有可以添加的 App',
      adding: '正在添加…',
      added: '已添加，{name} 会在登录时打开',
      refreshing: '正在检查启动项…',
      removed: '已移除 {name}，可以在隔离区恢复',
      removeTitle: '移除「{name}」？',
      removeBody: '会先停用它，再把配置文件移到隔离区，之后可以恢复。',
      removeBodyAdmin: '会先停用它，再把配置文件移到隔离区，之后可以恢复。这是系统级项目，需要输入一次管理员密码。',
      remove: '移除',
      enable: '启用',
      disable: '停用',
      keep: '保留',
      scan: '用病毒库扫描',
      adminHint: '这是系统级项目，停用或移除时需要输入一次管理员密码。',
      riskHigh: '高风险',
      avClean: '没有发现威胁',
      avThreat: '识别为 {name}',
      avNotScanned: '还没有扫描',
      category: {
        launch_items: '后台服务',
        btm: '后台项目',
        login_items: '登录项',
        cron: '定时任务',
        browser_extensions: '浏览器扩展',
        kexts: '内核扩展',
        system_extensions: '系统扩展',
        app_extensions: 'App 扩展',
        auth_plugins: '授权插件',
        dir_services_plugins: '目录服务插件',
        spotlight: '聚焦插件',
        quicklook: '快速查看插件',
        dock_tiles: '程序坞插件',
        library_inserts: '动态库注入',
        library_proxies: '动态库代理',
        login_hooks: '登录钩子',
        periodic: '周期脚本',
        startup_scripts: '启动脚本',
        shell_config: 'Shell 配置文件',
        event_rules: '事件规则'
      },
      scope: {
        user: '当前用户',
        system: '整台电脑'
      },
      state: {
        running: '运行中',
        loaded: '已加载',
        enabled: '已启用',
        disabled: '已停用',
        unknown: '未知',
        removed: '已移除'
      },
      trust: {
        apple: 'Apple',
        known: '已知开发者',
        unknown: '无法确认',
        untrusted: '未签名或未公证'
      },
      trigger: {
        at_load: '开机运行',
        keep_alive: '常驻',
        interval: '定时运行',
        calendar: '按日程运行',
        watch_paths: '文件变化时运行',
        events: '事件触发'
      },
      fields: {
        category: '类型',
        scope: '范围',
        path: '位置',
        program: '程序',
        owner: '所属 App',
        signature: '签名',
        team: 'Team ID',
        state: '状态',
        triggers: '运行方式',
        firstSeen: '首次发现',
        av: '病毒库结果'
      },
      restore: '恢复',
      restored: '已恢复 {name}',
      removedBackup: '已移除 {name}，可以在这里恢复',
      removeBodyBackup: '会先备份再移除，之后可以在启动项列表里恢复。'
    },
    signature: {
      apple: 'Apple 签名',
      appStore: 'App Store 签名',
      notarized: '已公证',
      notNotarized: '签名有效，但未公证',
      adhoc: '临时签名，无法确认开发者',
      unsigned: '没有签名',
      invalid: '签名无效',
      notCode: '不是程序文件，不适用签名',
      unknown: '无法核对签名',
      modified: '签名有效，但签名后有文件被改动'
    },
    quarantine: {
      summary: '{count} 个文件，共 {size}',
      empty: '隔离区是空的',
      emptyHint: '隔离的威胁和移除的启动项会放在这里，可以随时恢复',
      reason: {
        startup: '移除的启动项',
        user: '手动隔离'
      },
      restore: '恢复',
      restored: '已恢复 {name}',
      delete: '彻底删除',
      deleteTitle: '彻底删除「{name}」？',
      deleteBody: '删除后无法恢复。'
    },
    protection: {
      title: '实时防护',
      startupAlerts: {
        title: '启动项变化提醒',
        body: '出现新的开机启动项、登录项或后台服务时立即提醒你，并显示签名和所属 App。它是事后提醒，不能阻止第一次运行。'
      },
      watchDownloads: {
        title: '下载文件自动扫描',
        body: '监视「下载」文件夹，新文件下载完成后用病毒库扫描；安装包还会核对签名和公证。'
      },
      needsDb: '需要先下载病毒库',
      waiting: '{stage} · 等待 Apple 授权',
      later: {
        realtime: {
          title: '启动项实时拦截',
          body: '能知道是哪个程序添加的启动项，并立即停用。需要 Apple 批准 Endpoint Security 授权。'
        },
        exec: {
          title: '未公证程序运行确认',
          body: '下载来的、未经公证的程序第一次运行前先询问你。需要 Endpoint Security 授权。'
        },
        ransomware: {
          title: '勒索软件防护',
          body: '某个程序在短时间内大量写出加密文件时，暂停它并询问你。需要 Endpoint Security 授权。'
        }
      },
      eventsTitle: '最近的记录',
      noEvents: '还没有记录',
      events: {
        startup_added: '新增启动项：{name}',
        startup_removed: '启动项已移除：{name}',
        download_threat: '下载的文件有威胁：{name}',
        download_unsigned: '下载的安装包没有签名：{name}',
        scan_threat: '扫描发现威胁：{name}',
        scheduled_scan_skipped: '定时扫描已跳过：{name}',
        signature_changed: '开发者签名变了：{name}',
        install_threat: '安装包有威胁，已停止安装：{name}',
        paste_blocked: '暂停了一次可疑的粘贴：{name}'
      },
      installThreatUnnamed: '安装包有威胁，已停止安装'
    },
    toasts: {
      startupAdded: '出现 {count} 个新的开机启动项',
      startupAddedBody: '{name} · {signer}',
      review: '查看',
      threats: '扫描发现 {count} 个威胁',
      clean: '扫描完成，没有发现威胁',
      view: '查看',
      signatureChanged: '{count} 个 App 的开发者签名变了',
      signatureChangedBody: '{names}：签名的开发者和之前不同，可能被替换过',
      installThreat: '安装包里发现威胁，已停止安装'
    },
    rail: {
      engine: '扫描引擎',
      version: '版本',
      engineVersion: 'ClamAV {version}',
      database: '病毒库',
      noDatabase: '未下载',
      size: '大小',
      engineState: '引擎',
      running: '运行中 · {size}',
      idle: '未运行',
      protection: '实时防护',
      on: '开启',
      off: '关闭',
      limitsTitle: '能力范围',
      limits:
        '病毒库对 Mac 专用恶意软件覆盖有限；在拿到 Apple 授权前，启动项提醒只能事后提醒、不能拦截。它是对 macOS 自带防护的补充，不能代替它。超过 100 MB 的普通文件、超过 1 GB 或展开后超过 2 GB 的安装包不会完整扫描。'
    },
    avSkipped: {
      no_database: '还没有下载病毒库',
      no_consent: '还没有同意下载病毒库',
      excluded: '所在文件夹在排除列表里',
      too_large: '超出扫描上限',
      unreadable: '无法读取（没有权限、已加密或格式不支持）'
    },
    installScan: {
      clean: '安装前扫描：检查了 {count} 个文件，没有发现威胁',
      skipped: '安装前扫描没有完成：{reason}',
      skippedPartial: '安装前扫描检查了 {count} 个文件，但没有完整扫描：{reason}',
      threat: '安装前扫描发现：{names}'
    },
    settings: {
      interval: '病毒库检查频率',
      intervalHint: '先查询版本记录，有新版本才下载增量；官方要求间隔不少于 1 小时',
      everyHours: '每 {count} 小时',
      resident: '保持扫描引擎常驻',
      residentHint: '下载扫描会更快，但会一直占用约 1.1 GB 内存。关闭时，引擎空闲 5 分钟后自动退出',
      watchDownloadsHint: '监视「下载」文件夹；需要先下载病毒库',
      startupAlertsHint: '出现新的开机启动项时通知你',
      exclusions: '扫描排除项',
      exclusionsHint: '这些文件夹在所有扫描中都会跳过',
      addExclusion: '添加文件夹',
      removeExclusion: '移除',
      choose: '选择…',
      beforeInstall: '安装前扫描',
      beforeInstallHint:
        'Homebrew 下载完成后、安装之前先扫描下载的安装文件，发现威胁就停止安装；需要先下载病毒库。安装过程中额外下载的内容（例如从源码构建，或安装脚本自己联网下载）不在扫描范围内',
      schedule: {
        title: '定时扫描',
        frequency: '自动快速扫描',
        hint: '在设定的时间做一次快速扫描。只在 OpenNavo 运行时进行，不会唤醒 Mac；使用电池时跳过',
        off: '关闭',
        daily: '每天',
        weekly: '每周',
        weekday: '星期',
        time: '时间',
        next: '下次扫描：{time}',
        skipped: '上次跳过：{reason}',
        reason: {
          no_database: '还没有下载病毒库',
          no_consent: '还没有同意下载病毒库',
          on_battery: '当时在用电池供电',
          busy: '当时有其他扫描在进行',
          error: '扫描没能开始'
        }
      },
      optional: {
        title: '可选功能',
        hint: '默认关闭，需要你自己开启'
      },
      paste: {
        title: '粘贴命令保护',
        hint: '在终端里用 ⌘V 粘贴「下载并执行」这类可疑命令时，先暂停并请你确认。支持终端、iTerm2、Warp、kitty、Alacritty、Ghostty、WezTerm、Hyper 和 Tabby；用菜单或右键粘贴不会检查。只在本机检查，不保存粘贴的内容',
        needsAccess: '需要辅助功能权限',
        needsAccessHint: '允许后，OpenNavo 才能在终端粘贴前检查命令',
        grant: '开启权限'
      },
      virusTotal: {
        title: 'VirusTotal 查询',
        hint: '用你自己的 API key，按文件的 SHA-256 查询 VirusTotal 的检测结果；只在你点击查询时发送哈希，不上传文件',
        key: 'API key',
        keySaved: '已保存在钥匙串里',
        keyHint: '在 virustotal.com 注册账户后，可以在个人资料里找到 API key',
        removeKey: '移除 key',
        placeholder: '粘贴 API key',
        saveKey: '保存',
        terms:
          'VirusTotal 公共 API 每分钟最多查询 4 次，只限非商业的个人使用；用于商业用途时，你需要自己具备 VirusTotal 的商业许可。OpenNavo 不会自动查询，也不会上传任何文件。'
      }
    },
    signatures: {
      title: '已安装 App 签名体检',
      never: '还没有检查过已安装 App 的签名',
      summaryIssues: '检查了 {count} 个 App，{issues} 个有问题',
      summaryClean: '检查了 {count} 个 App，签名和公证都正常',
      issue: {
        invalid: '签名无效',
        unsigned: '没有签名',
        adhoc: '临时签名',
        not_notarized: '未经公证',
        team_changed: '开发者变了',
        unreadable: '读不到签名',
        modified: '签名后有改动'
      },
      check: '检查签名',
      recheck: '重新检查',
      viewAll: '查看全部 {count} 个',
      dialog: {
        title: '签名有问题的 App',
        hint: '签名有问题不一定有风险：有的 App 自己更新、写入缓存时也会这样。拿不准的，可以先在访达里看看，或到官网重新下载；确定不再用的，可以直接卸载。',
        uninstallSelected: '卸载所选（{count}）',
        empty: '没有签名问题了',
        missing: '找不到这个 App，可能已经卸载了'
      },
      hint: {
        invalid: '签名没有通过校验：App 里有文件在签名后被改动、增加或缺失，也可能是签名已损坏。',
        modified: '签名本身没问题，但里面的部分组件在签名后被改动过。',
        unsigned: '没有签名，系统无法确认它来自谁。',
        adhoc: '只有临时签名，没有开发者身份，系统无法确认它来自谁。',
        not_notarized: '有开发者签名，但没有经过 Apple 公证。',
        team_changed: '签名它的开发者和上次检查时不一样了。如果你没有重新安装过它，请多留心。',
        unreadable: '这次没能读到它的签名，稍后重新检查看看。'
      }
    },
    paste: {
      title: '已暂停粘贴到「{app}」',
      body: '这条命令看起来会从网上下载并运行程序。如果不是你自己要运行的，请丢弃。',
      bodyUnchecked: '要粘贴的内容太长，没有完整检查。确认是你自己要粘贴的，再允许这一次。',
      pattern: {
        pipe_to_interpreter: '把下载的内容直接交给 Shell 运行',
        download_execute: '下载后立即运行',
        encoded_command: '包含经过编码的命令',
        remove_quarantine: '去掉 macOS 的隔离标记，绕过安全检查',
        too_long: '内容太长，没有完整检查'
      },
      expires: '{seconds} 秒后自动丢弃',
      allow: '允许这一次',
      discard: '丢弃'
    },
    virusTotal: {
      lookup: '在 VirusTotal 查询',
      malicious: 'VirusTotal：{count} 个引擎报毒',
      suspicious: 'VirusTotal：{count} 个引擎认为可疑',
      clean: 'VirusTotal：没有引擎报毒',
      notFound: 'VirusTotal 没有这个文件的记录',
      engines: '共 {total} 个引擎检测',
      analyzed: '{time}分析',
      hashOnly: '只发送了文件的 SHA-256，没有上传文件',
      report: '查看报告'
    },
    downloadThreat: {
      title: '下载的文件有威胁：{file}',
      body: '{signature} · 扫描这个文件后可以把它移到隔离区',
      action: '扫描并处理'
    }
  },
  firewall: {
    title: '应用防火墙',
    subtitle: 'App 联网前先问你，按你的规则允许或阻止，判断都在这台 Mac 上完成',
    tabs: {
      label: '应用防火墙',
      rules: '规则',
      activity: '连接记录',
      blocklists: '屏蔽列表',
      settings: '设置'
    },
    unavailable: {
      not_bundled: {
        title: '这个版本没有包含防火墙组件',
        body: '应用防火墙需要随正式版一起签名发布的网络系统扩展，开发版和未签名的版本没有这个组件。请使用 OpenNavo 正式版。'
      },
      not_in_applications: {
        title: '请先把 OpenNavo 移到「应用程序」文件夹',
        body: 'macOS 只允许「应用程序」文件夹里的 App 安装系统扩展。移动后重新打开 OpenNavo，再回到这里开启。'
      },
      unsupported_os: {
        title: '这台 Mac 的系统版本不支持',
        body: '应用防火墙需要 macOS 13 或更新的版本。'
      }
    },
    setup: {
      title: '开启应用防火墙',
      lead: '每个 App 第一次连接网络时，OpenNavo 会先问你允许还是阻止，并记住你的选择。',
      points: {
        ask: { title: '联网前先问你', body: '能看到是哪个 App、要连到哪里、谁签的名，再决定允许或阻止。' },
        rules: { title: '按 App 或按目的地', body: '可以放行整个 App，也可以只允许或阻止某个网站、某个端口。' },
        blocklists: { title: '屏蔽列表', body: '导入广告、跟踪或恶意网站列表，命中的连接直接阻止。' }
      },
      enable: '开启应用防火墙',
      retry: '重新开启',
      steps: '开启时 macOS 会请你确认两次：先在「系统设置」里允许 OpenNavo 的系统扩展，再允许它过滤网络内容。',
      privacy: '判断都在这台 Mac 上完成，连接信息不会上传。',
      failedTitle: '没能开启应用防火墙',
      failed: '没能开启：{reason}',
      removed: '应用防火墙已移除，不用马上重启。剩下的组件会在下次重启 Mac 时由 macOS 清除。'
    },
    options: {
      allowApple: '自动允许 Apple 的程序',
      allowAppleHint: 'curl、ssh、python 等命令行工具仍会询问。',
      allowInstalled: '自动允许已经安装的 App',
      allowInstalledHint: '只问之后新装的 App，打扰更少。'
    },
    progress: {
      title: '正在开启应用防火墙',
      uninstalling: '正在移除应用防火墙…',
      install: '安装系统扩展',
      installHint: '正在把防火墙组件交给 macOS，通常只要几秒钟。',
      approve: '在「系统设置」中允许',
      approveHint:
        '在「系统设置」→「通用」→「登录项与扩展」→「网络扩展」里打开 OpenNavo；macOS 14 及更早的版本在「隐私与安全性」里点「允许」。',
      consent: '允许过滤网络内容',
      consentHint: '在弹出的对话框里点「允许」。',
      openSettings: '打开系统设置',
      reboot: '重启 Mac 后完成安装。'
    },
    status: {
      on: '应用防火墙已开启',
      off: '应用防火墙已暂停',
      offBody: '现在不拦截任何连接，规则都还在。',
      toggle: '应用防火墙',
      rules: '{count} 条规则',
      blockedToday: '今天阻止 {count} 次',
      disconnected: '防火墙组件没有响应。',
      disconnectedBody: '已有规则仍然生效。如果一直这样，可以关掉防火墙再重新打开。',
      serviceMissing: '防火墙服务没有正常启动。',
      serviceMissingBody: '已有规则仍然生效，没有规则的连接会先放行并记录。重启 Mac 后会恢复。',
      preparing: '正在检查已经安装的 App，通常几秒钟完成；这期间防火墙照常工作。',
      mode: '没有规则的连接',
      modes: {
        ask: '询问我',
        allow: '允许并记录',
        block: '阻止并记录'
      },
      modeHints: {
        ask: '弹出询问，你的回答会变成规则。',
        allow: '不再询问，可以在「连接记录」里查看。',
        block: '新的 App 都连不上网，直到你为它添加允许规则。'
      },
      pending: '{count} 个连接等待你确认',
      review: '去确认'
    },
    signer: {
      apple: 'Apple 签名',
      app_store: 'App Store',
      developer: '开发者：{team}',
      developer_id: '已签名',
      adhoc: '本机签名',
      unsigned: '未签名',
      invalid: '签名无效'
    },
    rules: {
      search: '搜索 App、开发者或地址',
      filterLabel: '筛选规则',
      filters: {
        all: '全部',
        allow: '允许',
        block: '阻止',
        user: '我设置的',
        auto: '自动添加'
      },
      add: '添加规则',
      allApps: '所有 App',
      allAppsHint: '对每个 App 都生效',
      anyRemote: '任何地址',
      anyPort: '任何端口',
      port: '端口 {port}',
      action: {
        allow: '允许',
        block: '阻止'
      },
      untilQuit: '直到 App 退出',
      source: {
        user: '我设置的',
        apple: 'Apple 程序',
        installed: '已安装的 App'
      },
      lastMatch: '{time}用过 · {count} 次',
      neverMatched: '还没用过',
      menuLabel: '「{name}」的规则操作',
      menu: {
        allow: '改为允许',
        block: '改为阻止',
        edit: '编辑',
        delete: '删除'
      },
      noMatch: '没有符合条件的规则',
      empty: '还没有规则',
      emptyBody: 'App 联网时你的回答会变成规则，出现在这里。',
      deleteTitle: '删除这条规则？',
      deleteBody: '删除后，「{name}」下次联网时按「没有规则的连接」处理。',
      deleted: '已删除规则'
    },
    sheet: {
      addTitle: '添加规则',
      editTitle: '编辑规则',
      program: '程序',
      oneApp: '一个 App',
      allApps: '所有 App',
      allAppsHint: '对每个 App 都生效，需要指定地址或端口。',
      chooseApp: '选择 App 或程序…',
      change: '更换',
      needApp: '请选择一个 App 或程序',
      action: '操作',
      remote: '目的地',
      someRemote: '指定地址',
      remotePlaceholder: 'example.com、*.example.com、IP 或网段',
      remoteHint: '*.example.com 包含它的所有子域名；网段写成 10.0.0.0/8 这样。',
      port: '端口',
      somePort: '指定端口',
      invalid: '地址或端口的格式不对',
      needDestination: '给所有 App 设的规则要指定地址或端口，否则会放行或阻止整台 Mac 的网络。',
      order: '越具体的规则越优先：指定了地址或端口的规则先生效；同样具体时，阻止优先于允许。',
      save: '保存',
      saved: '已保存规则',
      added: '已添加规则'
    },
    activity: {
      filterLabel: '筛选记录',
      filters: {
        all: '全部',
        blocked: '只看阻止'
      },
      hint: '被规则放行的连接不记录',
      clear: '清空记录',
      clearTitle: '清空连接记录？',
      clearBody: '只删除记录，规则不受影响。',
      port: '端口 {port}',
      reason: {
        rule: '按规则',
        blocklist: '屏蔽列表',
        apple: 'Apple 程序，自动允许',
        installed: '已安装的 App，自动允许',
        mode: '没有规则',
        answer: '你的回答',
        timeout: '没有及时回答，先放行',
        no_client: 'OpenNavo 没在运行，先放行'
      },
      action: {
        allow: '已允许',
        block: '已阻止'
      },
      count: '{count} 次',
      allowApp: '允许这个 App',
      blockApp: '阻止这个 App',
      allowDestination: '允许这个地址',
      blockDestination: '阻止这个地址',
      ruled: {
        allow: '已允许「{name}」联网',
        block: '已阻止「{name}」联网',
        allowDestination: '已允许「{name}」连接 {destination}',
        blockDestination: '已阻止「{name}」连接 {destination}'
      },
      ruledShort: {
        allow: '已改为允许',
        block: '已改为阻止'
      },
      more: '加载更早的记录',
      empty: '还没有记录',
      emptyBlocked: '还没有被阻止的连接',
      emptyBody: '被阻止的连接、你的回答和自动允许的连接会记在这里。'
    },
    blocklists: {
      intro: '连接到列表里的网站或 IP 时直接阻止，不再询问。来自网址的列表每天自动更新。',
      source: '屏蔽列表的网址或文件',
      placeholder: 'https://… 或本地文件路径',
      add: '添加',
      chooseFile: '选择文件…',
      added: '已添加「{name}」，共 {count} 条',
      addFailed: '没能添加屏蔽列表',
      toggle: '启用「{name}」',
      entries: '{count} 条',
      updated: '{time}更新',
      refresh: '立即更新「{name}」',
      remove: '移除「{name}」',
      empty: '还没有屏蔽列表',
      emptyBody: '支持 hosts 文件，也支持每行一个域名、IP 或网段的列表。',
      boundary: '有些 App 自己查好 IP 再直接连接，这类连接只能按列表里的 IP 屏蔽，按域名可能拦不到。',
      removeTitle: '移除这个屏蔽列表？',
      removeBody: '移除「{name}」后，列表里的地址不再被直接阻止。',
      removeAction: '移除'
    },
    settings: {
      automatic: '自动允许',
      allowInstalledHint: '开启防火墙时已经装好的 App 直接放行，只询问之后新装的 App。',
      closedHint: '这些设置由系统扩展保存，OpenNavo 退出后也照常生效。',
      remove: '移除',
      removeLabel: '移除应用防火墙',
      removeHint: '关闭过滤并卸载系统扩展，规则和记录会一起删除。macOS 可能会要求输入密码。',
      removeAction: '移除',
      removeTitle: '移除应用防火墙？',
      removeBody: '之后所有 App 都能直接联网，规则、屏蔽列表和连接记录会一起删除。可以随时重新开启。'
    },
    alert: {
      title: '「{name}」想连接网络',
      warning: {
        unsigned: '这个程序没有签名，无法确认是谁开发的。',
        adhoc: '这个程序只有本机签名，无法确认是谁开发的。',
        invalid: '这个程序的签名无效，可能被改动过。'
      },
      to: '连接到',
      port: '端口 {port}',
      path: '程序位置',
      parents: '由谁启动',
      args: '命令参数',
      scope: '范围',
      scopes: {
        app: '这个 App 的所有连接',
        destination: '只限这个地址'
      },
      until: '有效期',
      untils: {
        always: '永久',
        app_quits: '直到 App 退出'
      },
      waiting: '这个 App 还有 {count} 个连接在等这次回答',
      queue: '还有 {count} 个连接等待确认',
      block: '阻止',
      allow: '允许',
      none: '没有等待确认的连接',
      deadline: '{time} 内不回答会先放行这次连接',
      released: '这次连接已先放行，你的回答仍会记成规则。'
    },
    rail: {
      title: '防火墙',
      state: '状态',
      notSetUp: '未开启',
      paused: '已暂停',
      on: '运行中',
      noResponse: '没有响应',
      mode: '没有规则时',
      rules: '规则',
      blockedToday: '今天阻止',
      version: '组件版本',
      limitsTitle: '它管什么',
      limits:
        '只管这台 Mac 上的 App 向外发起的连接，不管别人连进来的连接，那由 macOS 自带的防火墙负责。OpenNavo 没有运行时没法询问，没有规则的连接会先放行，并记在连接记录里。'
    }
  },
  permissionGate: {
    title: '开启权限，开始使用',
    body: '完成下面两项授权后，就能使用「{feature}」。',
    bodyApps: '完成下面两项授权后，就能继续操作下面的 App。',
    bodyInstall: '完成下面两项授权后，就能安装 App。',
    browse: '先浏览功能',
    continue: '继续',
    locked: '开启「App 管理」和「完全磁盘访问权限」后，才能使用「{feature}」。',
    unlock: '开启权限'
  }
};
