// Simulated Mac App Store commands (store/security plan §5, §11.3) for browser mode and component tests.
// Search matches name, seller and genre case-insensitively, like the iTunes Search API's loose term matching.
// ?mock= switches: mas-engine (bundled helper can install), mas-rate-limit / mas-offline (search and lookup fail),
// mas-region-system (storefront guessed from the system region instead of the account).
import { events } from '../bindings';
import type { AppError, MasApp, MasInstalled, MasStatus, MasUpdate } from '../bindings';

const appError = (code: string, message: string, detail: string | null = null): AppError => ({
  code,
  message,
  detail
});

const DAY = 24 * 60 * 60 * 1000;
const MB = 1024 * 1024;
const iso = (daysAgo: number) => new Date(Date.now() - daysAgo * DAY).toISOString();

type Seed = Omit<MasApp, 'storeUrl' | 'country' | 'screenshots' | 'iconUrl' | 'currency'> & { shots?: number };

// Free, paid and in-app-purchase apps; three match the App Store apps in the uninstall fixtures by bundle ID.
const seeds: Seed[] = [
  {
    adamId: 497799835,
    bundleId: 'com.apple.dt.Xcode',
    name: 'Xcode',
    seller: 'Apple',
    version: '26.2',
    releaseNotes: 'Includes the macOS 27.2 SDK and fixes slow Simulator launches on some Macs.',
    releasedAt: iso(6),
    minimumOs: '15.6',
    sizeBytes: 3120 * MB,
    price: 0,
    formattedPrice: 'Free',
    rating: 3.4,
    ratingCount: 1820,
    description: 'Xcode includes everything developers need to build, test and distribute apps for Apple platforms.',
    genres: ['Developer Tools']
  },
  {
    adamId: 424389933,
    bundleId: 'com.apple.FinalCut',
    name: 'Final Cut Pro',
    seller: 'Apple',
    version: '11.2',
    releaseNotes: 'Faster magnetic timeline editing.',
    releasedAt: iso(80),
    minimumOs: '14.6',
    sizeBytes: 4300 * MB,
    price: 299.99,
    formattedPrice: '$299.99',
    rating: 4.1,
    ratingCount: 512,
    description: 'Professional video editing.',
    genres: ['Video']
  },
  {
    adamId: 409183694,
    bundleId: 'com.apple.iWork.Keynote',
    name: 'Keynote',
    seller: 'Apple',
    version: '14.5',
    releaseNotes: 'Live captions for presenter notes.',
    releasedAt: iso(12),
    minimumOs: '14.0',
    sizeBytes: 640 * MB,
    price: 0,
    formattedPrice: 'Free',
    rating: 4.3,
    ratingCount: 9031,
    description: 'Build stunning presentations.',
    genres: ['Productivity']
  },
  {
    adamId: 1666327168,
    bundleId: 'app.cyan.markedit',
    name: 'MarkEdit',
    seller: 'MarkEdit-app',
    version: '1.24.0',
    releaseNotes: 'Fixes flicker when switching themes on macOS 27; better table editing.',
    releasedAt: iso(5),
    minimumOs: '14.0',
    sizeBytes: 12 * MB,
    price: 0,
    formattedPrice: 'Free',
    rating: 4.8,
    ratingCount: 1203,
    description: 'A lightweight, open-source Markdown editor.',
    genres: ['Productivity', 'Developer Tools']
  },
  {
    adamId: 1225570693,
    bundleId: 'com.ulyssesapp.mac',
    name: 'Ulysses',
    seller: 'Ulysses GmbH & Co. KG',
    version: '37.2',
    releaseNotes: 'Better Markdown export.',
    releasedAt: iso(20),
    minimumOs: '14.0',
    sizeBytes: 92 * MB,
    price: 0,
    formattedPrice: 'Free',
    rating: 4.6,
    ratingCount: 3410,
    description: 'Focused Markdown writing, with an in-app subscription.',
    genres: ['Productivity']
  },
  {
    adamId: 1435957248,
    bundleId: 'com.agiletortoise.Drafts-OSX',
    name: 'Drafts',
    seller: 'Agile Tortoise',
    version: '48.1',
    releaseNotes: 'New Shortcuts actions.',
    releasedAt: iso(3),
    minimumOs: '14.0',
    sizeBytes: 34 * MB,
    price: 0,
    formattedPrice: 'Free',
    rating: 4.7,
    ratingCount: 880,
    description: 'Quickly capture text, with Markdown notes.',
    genres: ['Productivity']
  },
  {
    adamId: 1176895641,
    bundleId: 'com.ranchero.NetNewsWire-Evergreen',
    name: 'NetNewsWire',
    seller: 'Ranchero Software, LLC',
    version: '6.2',
    releaseNotes: null,
    releasedAt: iso(140),
    minimumOs: '13.5',
    sizeBytes: 18 * MB,
    price: 0,
    formattedPrice: 'Free',
    rating: 4.5,
    ratingCount: 410,
    description: 'An open-source RSS reader.',
    genres: ['News']
  },
  {
    adamId: 836500024,
    bundleId: 'com.tencent.xinWeChat',
    name: 'WeChat',
    seller: 'Tencent Technology (Shenzhen) Company Limited',
    version: '4.1.3',
    releaseNotes: 'Bug fixes.',
    releasedAt: iso(9),
    minimumOs: '13.0',
    sizeBytes: 520 * MB,
    price: 0,
    formattedPrice: 'Free',
    rating: 2.9,
    ratingCount: 60120,
    description: 'WeChat is a messaging and calling app.',
    genres: ['Social Networking']
  },
  {
    adamId: 1480068668,
    bundleId: 'com.charliemonroe.Downie-setapp',
    name: 'Downie',
    seller: 'Charlie Monroe Software',
    version: '4.10',
    releaseNotes: null,
    releasedAt: iso(30),
    minimumOs: '13.0',
    sizeBytes: 40 * MB,
    price: 19.99,
    formattedPrice: '$19.99',
    rating: 4.4,
    ratingCount: 95,
    description: 'Download videos from the web.',
    genres: ['Utilities']
  }
];

// Inline placeholder screenshots: browser mode works offline, like the rest of the mock.
function screenshot(adamId: number, index: number): string {
  const hue = (adamId % 360) + index * 40;
  const svg = `<svg xmlns="http://www.w3.org/2000/svg" width="1280" height="800"><defs><linearGradient id="g" x2="1" y2="1"><stop offset="0" stop-color="hsl(${hue},45%,22%)"/><stop offset="1" stop-color="hsl(${hue + 30},45%,12%)"/></linearGradient></defs><rect width="1280" height="800" fill="url(#g)"/><rect x="80" y="80" width="1120" height="640" rx="24" fill="rgba(255,255,255,0.06)"/></svg>`;
  return `data:image/svg+xml;charset=utf-8,${encodeURIComponent(svg)}`;
}

function record(seed: Seed, country: string): MasApp {
  const { shots = 3, ...rest } = seed;
  return {
    ...rest,
    currency: 'USD',
    iconUrl: null,
    screenshots: Array.from({ length: shots }, (_, index) => screenshot(seed.adamId, index)),
    storeUrl: `https://apps.apple.com/${country}/app/id${seed.adamId}?mt=12`,
    country
  };
}

/** Installed App Store apps: the uninstall fixtures' Xcode, Final Cut Pro and Keynote, plus Drafts. */
const installedSeeds: MasInstalled[] = [
  {
    adamId: 497799835,
    bundleId: 'com.apple.dt.Xcode',
    name: 'Xcode',
    version: '26.1',
    path: '/Applications/Xcode.app',
    installedAt: Date.now() - 380 * DAY
  },
  {
    adamId: 424389933,
    bundleId: 'com.apple.FinalCut',
    name: 'Final Cut Pro',
    version: '11.2',
    path: '/Applications/Final Cut Pro.app',
    installedAt: Date.now() - 600 * DAY
  },
  {
    adamId: 409183694,
    bundleId: 'com.apple.iWork.Keynote',
    name: 'Keynote',
    version: '14.4',
    path: '/Applications/Keynote.app',
    installedAt: Date.now() - 900 * DAY
  },
  {
    adamId: 1435957248,
    bundleId: 'com.agiletortoise.Drafts-OSX',
    name: 'Drafts',
    version: '48.0',
    path: '/Applications/Drafts.app',
    installedAt: Date.now() - 40 * DAY
  }
];

export interface AppStoreMockOptions {
  flags: ReadonlySet<string>;
}

export function createAppStoreMock({ flags }: AppStoreMockOptions) {
  const country = 'us';
  const apps = seeds.map(seed => record(seed, country));
  const byId = new Map(apps.map(app => [app.adamId, app]));
  /** Product pages opened through mas_open_store, newest last (tests read this). */
  const opened: number[] = [];
  // Each mock has its own copies, so installs and updates in one test never leak into the next.
  const installed: MasInstalled[] = installedSeeds.map(seed => ({ ...seed }));

  const status = (): MasStatus => ({
    country,
    countrySource: flags.has('mas-region-system') ? 'system' : 'account',
    engine: flags.has('mas-engine') ? 'ok' : 'missing',
    adminInstall: true
  });

  // Network failures behave like Rust: rate limits carry Retry-After seconds in detail.
  function guard() {
    if (flags.has('mas-offline')) throw appError('E_NETWORK', 'itunes_unreachable');
    if (flags.has('mas-rate-limit')) throw appError('E_MAS_RATE_LIMITED', 'rate_limited', '60');
  }

  const updates = (): MasUpdate[] =>
    installed.flatMap(local => {
      const store = byId.get(local.adamId);
      if (!store || store.version === local.version) return [];
      return [
        {
          adamId: local.adamId,
          bundleId: local.bundleId,
          name: store.name,
          installedVersion: local.version,
          storeVersion: store.version,
          releasedAt: store.releasedAt,
          releaseNotes: store.releaseNotes,
          sizeBytes: store.sizeBytes,
          confidence: 'high' as const
        }
      ];
    });

  const handlers: Record<string, (args: Record<string, unknown>) => unknown> = {
    mas_status: () => status(),
    mas_search: args => {
      guard();
      const term = String(args.term ?? '')
        .trim()
        .toLowerCase();
      const limit = Math.min(Math.max(Number(args.limit ?? 20), 1), 50);
      if (!term) return [];
      return apps
        .filter(app =>
          [app.name, app.seller, ...app.genres, app.description].some(text => text.toLowerCase().includes(term))
        )
        .slice(0, limit);
    },
    mas_lookup: args => {
      guard();
      return ((args.adamIds as number[] | undefined) ?? []).flatMap(id => byId.get(id) ?? []);
    },
    mas_lookup_bundles: args => {
      guard();
      const ids = (args.bundleIds as string[] | undefined) ?? [];
      return Object.fromEntries(ids.flatMap(id => apps.filter(app => app.bundleId === id).map(app => [id, app])));
    },
    mas_installed: () => installed,
    mas_updates: () => updates(),
    mas_open_store: args => {
      opened.push(Number(args.adamId));
      return null;
    }
  };

  /** A finished install or update task: the copy on this Mac now has the store's version. */
  function markInstalled(adamId: number) {
    const store = byId.get(adamId);
    if (!store) return;
    const local = installed.find(app => app.adamId === adamId);
    queueMicrotask(() => void events.appstoreChanged.emit({ reason: 'installed' }));
    if (local) local.version = store.version;
    else
      installed.push({
        adamId,
        bundleId: store.bundleId,
        name: store.name,
        version: store.version,
        path: `/Applications/${store.name}.app`,
        installedAt: Date.now()
      });
  }

  return { handlers, opened, updates, markInstalled };
}
