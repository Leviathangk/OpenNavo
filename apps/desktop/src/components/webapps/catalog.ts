export type WebAppCategory = 'ai' | 'social' | 'work' | 'dev' | 'media' | 'mail' | 'tools';

export interface WebAppCatalogEntry {
  id: string;
  name: string;
  url: string;
  category: WebAppCategory;
  /** Brand colour used by the built-in monogram tile. */
  color: string;
  warning?: 'drm';
}

export const WEBAPP_CATEGORIES: readonly WebAppCategory[] = ['ai', 'social', 'work', 'dev', 'media', 'mail', 'tools'];

export const WEBAPP_CATALOG: readonly WebAppCatalogEntry[] = [
  { id: 'chatgpt', name: 'ChatGPT', url: 'https://chatgpt.com/', category: 'ai', color: '#10A37F' },
  { id: 'claude', name: 'Claude', url: 'https://claude.ai/', category: 'ai', color: '#D97757' },
  {
    id: 'deepseek',
    name: 'DeepSeek',
    url: 'https://chat.deepseek.com/',
    category: 'ai',
    color: '#4D6BFE'
  },
  {
    id: 'gemini',
    name: 'Gemini',
    url: 'https://gemini.google.com/',
    category: 'ai',
    color: '#4285F4'
  },
  {
    id: 'perplexity',
    name: 'Perplexity',
    url: 'https://www.perplexity.ai/',
    category: 'ai',
    color: '#20808D'
  },
  { id: 'doubao', name: '豆包', url: 'https://www.doubao.com/chat/', category: 'ai', color: '#3B82F6' },
  { id: 'x', name: 'X', url: 'https://x.com/', category: 'social', color: '#111111' },
  {
    id: 'threads',
    name: 'Threads',
    url: 'https://www.threads.com/',
    category: 'social',
    color: '#000000'
  },
  { id: 'weibo', name: '微博', url: 'https://weibo.com/', category: 'social', color: '#E6162D' },
  { id: 'zhihu', name: '知乎', url: 'https://www.zhihu.com/', category: 'social', color: '#0066FF' },
  {
    id: 'xiaohongshu',
    name: '小红书',
    url: 'https://www.xiaohongshu.com/explore',
    category: 'social',
    color: '#E8263F'
  },
  { id: 'reddit', name: 'Reddit', url: 'https://www.reddit.com/', category: 'social', color: '#FF4500' },
  {
    id: 'discord',
    name: 'Discord',
    url: 'https://discord.com/app',
    category: 'social',
    color: '#5865F2'
  },
  {
    id: 'instagram',
    name: 'Instagram',
    url: 'https://www.instagram.com/',
    category: 'social',
    color: '#E4405F'
  },
  {
    id: 'linkedin',
    name: 'LinkedIn',
    url: 'https://www.linkedin.com/feed/',
    category: 'social',
    color: '#0A66C2'
  },
  { id: 'douban', name: '豆瓣', url: 'https://www.douban.com/', category: 'social', color: '#2E7D32' },
  { id: 'notion', name: 'Notion', url: 'https://www.notion.so/', category: 'work', color: '#111111' },
  { id: 'linear', name: 'Linear', url: 'https://linear.app/', category: 'work', color: '#5E6AD2' },
  {
    id: 'figma',
    name: 'Figma',
    url: 'https://www.figma.com/files/',
    category: 'work',
    color: '#E64A19'
  },
  { id: 'feishu', name: '飞书', url: 'https://www.feishu.cn/', category: 'work', color: '#3370FF' },
  {
    id: 'tencent-docs',
    name: '腾讯文档',
    url: 'https://docs.qq.com/desktop',
    category: 'work',
    color: '#2B7BF3'
  },
  { id: 'slack', name: 'Slack', url: 'https://app.slack.com/client', category: 'work', color: '#4A154B' },
  {
    id: 'yuque',
    name: '语雀',
    url: 'https://www.yuque.com/dashboard',
    category: 'work',
    color: '#00A86B'
  },
  { id: 'wps', name: 'WPS', url: 'https://www.kdocs.cn/', category: 'work', color: '#E64D2E' },
  { id: 'dingtalk', name: '钉钉', url: 'https://im.dingtalk.com/', category: 'work', color: '#0089FF' },
  {
    id: 'wecom',
    name: '企业微信',
    url: 'https://work.weixin.qq.com/',
    category: 'work',
    color: '#07C160'
  },
  { id: 'github', name: 'GitHub', url: 'https://github.com/', category: 'dev', color: '#24292F' },
  { id: 'gitlab', name: 'GitLab', url: 'https://gitlab.com/', category: 'dev', color: '#FC6D26' },
  {
    id: 'stackoverflow',
    name: 'Stack Overflow',
    url: 'https://stackoverflow.com/',
    category: 'dev',
    color: '#D97706'
  },
  {
    id: 'vercel',
    name: 'Vercel',
    url: 'https://vercel.com/dashboard',
    category: 'dev',
    color: '#111111'
  },
  { id: 'youtube', name: 'YouTube', url: 'https://www.youtube.com/', category: 'media', color: '#E50914' },
  {
    id: 'bilibili',
    name: 'Bilibili',
    url: 'https://www.bilibili.com/',
    category: 'media',
    color: '#E84A7A'
  },
  {
    id: 'spotify',
    name: 'Spotify',
    url: 'https://open.spotify.com/',
    category: 'media',
    color: '#159A45'
  },
  {
    id: 'netflix',
    name: 'Netflix',
    url: 'https://www.netflix.com/',
    category: 'media',
    color: '#B20710',
    warning: 'drm'
  },
  { id: 'douyin', name: '抖音', url: 'https://www.douyin.com/', category: 'media', color: '#FE2C55' },
  {
    id: 'netease-music',
    name: '网易云音乐',
    url: 'https://music.163.com/',
    category: 'media',
    color: '#C20C0C'
  },
  {
    id: 'apple-music',
    name: 'Apple Music',
    url: 'https://music.apple.com/',
    category: 'media',
    color: '#E5395B'
  },
  {
    id: 'outlook',
    name: 'Outlook',
    url: 'https://outlook.live.com/mail/',
    category: 'mail',
    color: '#0078D4'
  },
  { id: 'gmail', name: 'Gmail', url: 'https://mail.google.com/', category: 'mail', color: '#EA4335' },
  {
    id: 'excalidraw',
    name: 'Excalidraw',
    url: 'https://excalidraw.com/',
    category: 'tools',
    color: '#6965DB'
  },
  { id: 'canva', name: 'Canva', url: 'https://www.canva.com/', category: 'tools', color: '#00A3B8' },
  {
    id: 'weread',
    name: '微信读书',
    url: 'https://weread.qq.com/',
    category: 'tools',
    color: '#2B7A3D'
  },
  { id: 'medium', name: 'Medium', url: 'https://medium.com/', category: 'tools', color: '#111111' },
  {
    id: 'hackernews',
    name: 'Hacker News',
    url: 'https://news.ycombinator.com/',
    category: 'tools',
    color: '#D9480F'
  }
];
