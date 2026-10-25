// Simulated app firewall commands (firewall plan §4) for browser mode and component tests.
// By default this Mac can run the firewall but has not turned it on: Enable installs the extension, waits for approval
// in System Settings (open_approval approves it), then asks to filter network content (accepted).
// Like the host, enable and disable answer with the status from before the change; the new filter state follows a tick
// later as a firewall:status event. A saved filter configuration turns back on without asking again; only the first
// consent shows the dialog. An answered question's shorter list goes out before the reply returns.
// ?mock= switches: firewall-on (installed, filtering, with rules, activity and a block list), firewall-alerts (on, with
// two connections waiting), firewall-alert-released (like firewall-alerts, but the first question's connections were
// already let through after nobody answered), firewall-not-bundled / firewall-not-in-applications (cannot run here),
// firewall-consent-denied (the filter dialog is declined), firewall-disconnected (the extension stops answering: the
// status carries E_FW_DISCONNECTED and reading or changing rules, settings, activity and block lists fails with it;
// turning filtering off and on again brings it back), firewall-service-missing (macOS started the extension without
// the service the app talks to, so the same reads fail; a restart fixes it), firewall-removal-pending (removed earlier;
// macOS deletes the stopped extension at the next restart), firewall-removal-fails (removing stops filtering, but the
// password prompt is dismissed and the extension stays).
// Component tests also drive ask() (a new connection asks) and reconnect() (the extension answers again).
import { events as ipcEvents } from '../bindings';
import type {
  AppError,
  FirewallAlert,
  FirewallApp,
  FirewallBlocklist,
  FirewallEnableOptions,
  FirewallEvent,
  FirewallEventsQuery,
  FirewallReply,
  FirewallRule,
  FirewallRuleInput,
  FirewallSettings,
  FirewallStatus
} from '../bindings';

const appError = (code: string, message: string, detail: string | null = null): AppError => ({ code, message, detail });

const MINUTE = 60 * 1000;
const HOUR = 60 * MINUTE;

const bundleApp = (
  name: string,
  bundle: string,
  executable: string,
  bundleId: string,
  team: { id: string; name: string } | null,
  signer: FirewallApp['signer'] = team ? 'developer_id' : 'apple'
): FirewallApp => ({
  key: team ? `${team.id}:${bundleId}` : `apple:${bundleId}`,
  name,
  path: `${bundle}/Contents/MacOS/${executable}`,
  bundlePath: bundle,
  bundleId,
  signingId: bundleId,
  teamId: team?.id ?? null,
  teamName: team?.name ?? null,
  signer,
  iconPath: null
});

const tool = (name: string, path: string, signer: FirewallApp['signer'], signingId: string | null): FirewallApp => ({
  key: signer === 'apple' ? `apple:${signingId}` : `path:${path}`,
  name,
  path,
  bundlePath: null,
  bundleId: null,
  signingId,
  teamId: null,
  teamName: null,
  signer,
  iconPath: null
});

const GOOGLE = { id: 'EQHXZ8M8AV', name: 'Google LLC' };
const MICROSOFT = { id: 'UBF8T346G9', name: 'Microsoft Corporation' };
const MOZILLA = { id: '43AQ936H96', name: 'Mozilla Corporation' };
const SPOTIFY = { id: '2FNC3A47ZF', name: 'Spotify' };

const APPS = {
  chrome: bundleApp('Google Chrome', '/Applications/Google Chrome.app', 'Google Chrome', 'com.google.Chrome', GOOGLE),
  code: bundleApp(
    'Visual Studio Code',
    '/Applications/Visual Studio Code.app',
    'Electron',
    'com.microsoft.VSCode',
    MICROSOFT
  ),
  firefox: bundleApp('Firefox', '/Applications/Firefox.app', 'firefox', 'org.mozilla.firefox', MOZILLA),
  spotify: bundleApp('Spotify', '/Applications/Spotify.app', 'Spotify', 'com.spotify.client', SPOTIFY),
  music: bundleApp('Music', '/System/Applications/Music.app', 'Music', 'com.apple.Music', null),
  curl: tool('curl', '/usr/bin/curl', 'apple', 'com.apple.curl'),
  python: tool('python3', '/opt/homebrew/Cellar/python@3.13/3.13.7/bin/python3.13', 'adhoc', 'python3'),
  helper: tool('sync-helper', '/Users/mock/.local/bin/sync-helper', 'unsigned', null)
} satisfies Record<string, FirewallApp>;

export interface FirewallMockOptions {
  flags: ReadonlySet<string>;
  tickMs: number;
}

export function createFirewallMock({ flags, tickMs }: FirewallMockOptions) {
  const now = Date.now();
  const asking = flags.has('firewall-alerts') || flags.has('firewall-alert-released');
  const missing = flags.has('firewall-service-missing');
  const disconnected = flags.has('firewall-disconnected');
  const on = flags.has('firewall-on') || asking || disconnected || missing;
  const removed = !on && flags.has('firewall-removal-pending');
  const SERVICE_MISSING = { code: 'E_FW_DISCONNECTED', detail: 'firewall_service_missing' } as const;
  // macOS keeps the filter configuration saved at the first consent; turning it on again doesn't ask.
  let configured = on;
  const timers = new Set<ReturnType<typeof setTimeout>>();
  let sequence = 0;
  const id = (prefix: string) => `${prefix}-${++sequence}`;
  const later = (run: () => void, ticks = 1) => {
    const timer = setTimeout(() => {
      timers.delete(timer);
      run();
    }, tickMs * ticks);
    timers.add(timer);
  };

  let settings: FirewallSettings = {
    mode: 'ask',
    allowApple: true,
    allowInstalled: true
  };
  let status: FirewallStatus = {
    availability: flags.has('firewall-not-bundled')
      ? 'not_bundled'
      : flags.has('firewall-not-in-applications')
        ? 'not_in_applications'
        : 'available',
    extension: on ? 'installed' : removed ? 'removal_pending' : 'not_installed',
    extensionVersion: on ? '1.0.0' : null,
    filter: on ? 'on' : 'off',
    connected: on && !disconnected && !missing,
    mode: 'ask',
    ruleCount: 0,
    pendingAlerts: 0,
    blockedToday: on ? 14 : 0,
    // Rust reports a filter that is on but doesn't answer as E_FW_DISCONNECTED.
    error: missing ? SERVICE_MISSING : disconnected ? { code: 'E_FW_DISCONNECTED', detail: null } : null
  };

  const rule = (
    app: FirewallApp | null,
    action: FirewallRule['action'],
    source: FirewallRule['source'],
    extra: Partial<FirewallRule> = {}
  ): FirewallRule => ({
    id: id('rule'),
    app,
    action,
    remote: null,
    port: null,
    until: 'always',
    pid: null,
    source,
    createdAt: now - 3 * 24 * HOUR,
    lastMatchAt: now - 4 * MINUTE,
    matchCount: 128,
    ...extra
  });
  // The rules that decided the activity below (its ruleId); the engine checks app + destination rules first, then
  // rules for every app, block lists, rules for a whole app, automatic allows, and finally the mode.
  const decided = {
    telemetry: rule(APPS.code, 'block', 'user', {
      remote: 'dc.services.visualstudio.com',
      lastMatchAt: now - HOUR,
      matchCount: 9
    }),
    helper: rule(APPS.helper, 'block', 'user', {
      lastMatchAt: now - 20 * MINUTE,
      matchCount: 3
    }),
    // Answered "only this address".
    mozilla: rule(APPS.firefox, 'allow', 'user', {
      remote: 'www.mozilla.org',
      port: 443,
      lastMatchAt: now - 2 * HOUR,
      matchCount: 1
    }),
    music: rule(APPS.music, 'allow', 'apple', { matchCount: 12 }),
    doubleclick: rule(null, 'block', 'user', {
      remote: '*.doubleclick.net',
      lastMatchAt: now - 4 * HOUR,
      matchCount: 2
    })
  };
  let rules: FirewallRule[] = on
    ? [
        rule(APPS.chrome, 'allow', 'installed'),
        rule(APPS.code, 'allow', 'user'),
        decided.telemetry,
        rule(APPS.spotify, 'allow', 'installed', { matchCount: 40 }),
        decided.music,
        decided.helper,
        decided.mozilla,
        decided.doubleclick
      ]
    : [];

  const event = (
    app: FirewallApp,
    action: FirewallEvent['action'],
    reason: FirewallEvent['reason'],
    remoteHost: string | null,
    remoteAddress: string,
    ago: number,
    extra: Partial<FirewallEvent> = {}
  ): FirewallEvent => ({
    id: id('event'),
    at: now - ago,
    app,
    remoteAddress,
    remoteHost,
    port: 443,
    protocol: 'tcp',
    action,
    reason,
    ruleId: null,
    blocklistId: null,
    count: 1,
    ...extra
  });
  let activity: FirewallEvent[] = on
    ? [
        event(APPS.helper, 'block', 'rule', 'telemetry.example.net', '203.0.113.24', 20 * MINUTE, {
          ruleId: decided.helper.id,
          count: 3
        }),
        event(APPS.code, 'block', 'rule', 'dc.services.visualstudio.com', '20.42.65.85', HOUR, {
          ruleId: decided.telemetry.id,
          count: 9
        }),
        event(APPS.firefox, 'allow', 'answer', 'www.mozilla.org', '34.120.208.123', 2 * HOUR, {
          ruleId: decided.mozilla.id
        }),
        event(APPS.chrome, 'block', 'blocklist', 'pagead2.googlesyndication.com', '142.250.72.194', 3 * HOUR, {
          blocklistId: 'list-1',
          count: 6
        }),
        event(APPS.firefox, 'block', 'rule', 'stats.g.doubleclick.net', '142.250.80.2', 4 * HOUR, {
          ruleId: decided.doubleclick.id,
          count: 2
        }),
        event(APPS.music, 'allow', 'apple', 'init.itunes.apple.com', '17.253.144.10', 5 * HOUR, {
          ruleId: decided.music.id
        }),
        event(APPS.firefox, 'allow', 'timeout', 'push.services.mozilla.com', '34.107.243.93', 8 * HOUR),
        event(APPS.python, 'allow', 'no_client', 'pypi.org', '151.101.0.223', 26 * HOUR, { count: 2 })
      ]
    : [];

  let blocklists: FirewallBlocklist[] = on
    ? [
        {
          id: 'list-1',
          name: 'raw.githubusercontent.com',
          source: 'https://raw.githubusercontent.com/example/hosts/main/ads.txt',
          enabled: true,
          entries: 84213,
          updatedAt: now - 5 * HOUR,
          error: null
        }
      ]
    : [];

  const alert = (app: FirewallApp, remoteHost: string | null, remoteAddress: string, port: number, extra = {}) =>
    ({
      id: id('alert'),
      app,
      pid: 4000 + sequence,
      args: [app.path],
      ancestors: [],
      remoteAddress,
      remoteHost,
      port,
      protocol: 'tcp',
      at: Date.now(),
      // The app's other connections still held for this answer; none once they were let through.
      waiting: 0,
      // Held TCP connections are let through after 2 minutes without an answer (UDP: 5 seconds).
      released: false,
      deadline: Date.now() + 120_000,
      ...extra
    }) satisfies FirewallAlert;
  let alerts: FirewallAlert[] = asking
    ? [
        alert(
          APPS.firefox,
          'incoming.telemetry.mozilla.org',
          '34.117.65.55',
          443,
          flags.has('firewall-alert-released') ? { released: true, deadline: null } : { waiting: 2 }
        ),
        alert(APPS.python, null, '198.51.100.7', 8080, {
          args: [APPS.python.path, 'sync.py', '--upload'],
          ancestors: [
            { pid: 3120, name: 'zsh', path: '/bin/zsh' },
            {
              pid: 3007,
              name: 'iTerm2',
              path: '/Applications/iTerm.app/Contents/MacOS/iTerm2'
            }
          ]
        })
      ]
    : [];

  const counted = (): FirewallStatus => ({
    ...status,
    ruleCount: rules.length,
    pendingAlerts: alerts.length
  });
  const publish = () => {
    status = counted();
    void ipcEvents.firewallStatus.emit(status);
  };
  const publishAlerts = () => {
    publish();
    void ipcEvents.firewallAlerts.emit({
      alerts: alerts.map(item => ({ ...item }))
    });
  };
  status = counted();

  const requireBundled = () => {
    if (status.availability === 'not_bundled') throw appError('E_FW_NOT_BUNDLED', 'firewall_not_bundled');
    if (status.availability === 'not_in_applications')
      throw appError('E_FW_NOT_IN_APPLICATIONS', 'firewall_not_in_applications');
  };
  const requireInstalled = () => {
    requireBundled();
    if (status.extension !== 'installed') throw appError('E_FW_DISCONNECTED', 'firewall_not_installed');
  };
  // Rules, settings, block lists and activity live in the extension; while it doesn't answer, reading or changing them
  // fails. Turning filtering on or off (and removing) goes through macOS and still works.
  const requireConnected = () => {
    if (status.extension === 'installed' && status.filter !== 'needs_consent' && !status.connected)
      throw appError('E_FW_DISCONNECTED', 'firewall_request', missing ? SERVICE_MISSING.detail : null);
  };

  // Consent arrives a tick after the filter is configured; a declined dialog leaves filtering off with the reason.
  function askConsent() {
    status = { ...status, filter: 'needs_consent', error: null };
    publish();
    later(() => {
      configured = !flags.has('firewall-consent-denied');
      status = !configured
        ? {
            ...status,
            filter: 'off',
            connected: true,
            error: { code: 'E_FW_CONSENT_DENIED', detail: null }
          }
        : // Filtering starts at once; the installed-app baseline is gathered meanwhile (a few seconds on a real Mac).
          {
            ...status,
            filter: 'on',
            connected: true,
            error: null,
            preparing: true
          };
      publish();
      if (status.preparing)
        later(() => {
          status = { ...status, preparing: false };
          publish();
        }, 2);
    });
  }

  const sameTarget = (
    a: FirewallRule,
    input: {
      appKey: string | null;
      remote: string | null;
      port: number | null;
    }
  ) => (a.app?.key ?? null) === input.appKey && a.remote === input.remote && a.port === input.port;

  const handlers: Record<string, (args: Record<string, unknown>) => unknown> = {
    firewall_status: () => counted(),
    firewall_enable: args => {
      requireBundled();
      const options = args.options as FirewallEnableOptions | null;
      if (options)
        settings = {
          ...settings,
          allowApple: options.allowApple,
          allowInstalled: options.allowInstalled
        };
      if (status.extension === 'installed') {
        const before = counted();
        if (!configured) later(askConsent);
        else
          later(() => {
            // Turning filtering back on restarts the extension; only a restart brings back a missing service.
            status = {
              ...status,
              filter: 'on',
              connected: !missing,
              error: missing ? SERVICE_MISSING : null
            };
            publish();
          });
        return before;
      }
      // The activation request is submitted at once, so the reply already says so.
      status = { ...status, extension: 'activating', error: null };
      later(() => {
        status = { ...status, extension: 'needs_approval' };
        publish();
      });
      return counted();
    },
    firewall_open_approval: () => {
      requireBundled();
      // The user flips the switch in System Settings; the extension finishes installing and the filter asks.
      if (status.extension === 'needs_approval')
        later(() => {
          status = {
            ...status,
            extension: 'installed',
            extensionVersion: '1.0.0'
          };
          askConsent();
        }, 2);
      return null;
    },
    firewall_disable: () => {
      requireInstalled();
      const before = counted();
      later(() => {
        status = { ...status, filter: 'off', error: null };
        publish();
      });
      return before;
    },
    firewall_uninstall: () => {
      requireInstalled();
      status = { ...status, extension: 'uninstalling', filter: 'off' };
      // Like a real Mac: the filter stops at once, and macOS deletes the extension at the next restart. A dismissed
      // password prompt keeps the extension; filtering stays off.
      if (flags.has('firewall-removal-fails')) {
        later(() => {
          status = { ...status, extension: 'installed', error: { code: 'E_FW_REMOVAL', detail: null } };
          publish();
        }, 2);
        return counted();
      }
      later(() => {
        status = {
          ...status,
          extension: 'removal_pending',
          extensionVersion: null,
          connected: false
        };
        rules = [];
        alerts = [];
        publishAlerts();
      }, 2);
      return counted();
    },
    firewall_app_identify: args => {
      requireInstalled();
      const path = String(args.path);
      const known = Object.values(APPS).find(app => app.bundlePath === path || app.path === path);
      if (known) return known;
      const name =
        path
          .split('/')
          .pop()
          ?.replace(/\.app$/, '') ?? path;
      if (!name) throw appError('E_FW_RULE_INVALID', 'not_executable');
      return path.endsWith('.app')
        ? bundleApp(name, path, name, `com.example.${name.toLowerCase().replace(/\W+/g, '')}`, {
            id: 'A1B2C3D4E5',
            name: 'Example Developer'
          })
        : tool(name, path, 'unsigned', null);
    },
    firewall_rules: () => {
      requireConnected();
      return status.extension === 'installed' ? rules.map(item => ({ ...item })) : [];
    },
    firewall_rule_save: args => {
      requireInstalled();
      requireConnected();
      const input = args.input as FirewallRuleInput;
      if (!input.appKey && !input.appPath && input.remote === null && input.port === null)
        throw appError('E_FW_RULE_INVALID', 'global_rule_needs_destination');
      const existing = input.id ? rules.find(item => item.id === input.id) : undefined;
      const app = input.appKey
        ? (rules.find(item => item.app?.key === input.appKey)?.app ??
          activity.find(item => item.app.key === input.appKey)?.app ??
          Object.values(APPS).find(item => item.key === input.appKey) ??
          null)
        : input.appPath
          ? (handlers.firewall_app_identify!({
              path: input.appPath
            }) as FirewallApp)
          : null;
      if ((input.appKey || input.appPath) && !app) throw appError('E_FW_RULE_INVALID', 'unknown_app');
      const target = {
        appKey: app?.key ?? null,
        remote: input.remote,
        port: input.port
      };
      // One rule per program and destination (plan v1.1): saving over an existing one replaces it.
      const replaced = existing ?? rules.find(item => sameTarget(item, target));
      const saved: FirewallRule = {
        ...(replaced ??
          rule(app, input.action, 'user', {
            lastMatchAt: null,
            matchCount: 0,
            createdAt: Date.now()
          })),
        app,
        action: input.action,
        remote: input.remote,
        port: input.port,
        until: 'always',
        pid: null,
        source: 'user'
      };
      rules = replaced
        ? rules
            .filter(item => item.id === replaced.id || !sameTarget(item, target))
            .map(item => (item.id === replaced.id ? saved : item))
        : [...rules, saved];
      later(publish);
      return { ...saved };
    },
    firewall_rule_delete: args => {
      requireInstalled();
      requireConnected();
      const ids = new Set(args.ids as string[]);
      rules = rules.filter(item => !ids.has(item.id));
      later(publish);
      return null;
    },
    firewall_alerts: () => {
      requireConnected();
      return alerts.map(item => ({ ...item }));
    },
    firewall_alert_reply: args => {
      const reply = args.reply as FirewallReply;
      const answered = alerts.find(item => item.id === reply.alertId);
      if (!answered) throw appError('E_FW_UNKNOWN_ALERT', 'alert_resolved');
      alerts = alerts.filter(item => item.id !== reply.alertId);
      const remote = reply.scope === 'destination' ? (answered.remoteHost ?? answered.remoteAddress) : null;
      const port = reply.scope === 'destination' ? answered.port : null;
      const target = { appKey: answered.app.key, remote, port };
      rules = [
        ...rules.filter(item => !sameTarget(item, target)),
        rule(answered.app, reply.action, 'user', {
          remote,
          port,
          until: reply.until,
          pid: reply.until === 'app_quits' ? answered.pid : null,
          createdAt: Date.now(),
          lastMatchAt: Date.now(),
          matchCount: 1 + answered.waiting
        })
      ];
      const recorded = event(
        answered.app,
        reply.action,
        'answer',
        answered.remoteHost,
        answered.remoteAddress,
        now - Date.now(),
        { port: answered.port, count: 1 + answered.waiting }
      );
      activity = [recorded, ...activity];
      // Like the host, the shorter list and the new rule go out before the reply returns; activity is batched.
      publishAlerts();
      void ipcEvents.firewallChanged.emit({ what: 'rules' });
      later(() => void ipcEvents.firewallActivity.emit({ events: [recorded] }));
      return null;
    },
    firewall_alert_show: () => {
      requireInstalled();
      return null;
    },
    firewall_events: args => {
      requireConnected();
      const query = args.query as FirewallEventsQuery;
      return activity
        .filter(
          item =>
            (query.before === null ||
              item.at < query.before ||
              (item.at === query.before && query.beforeId !== null && item.id < query.beforeId)) &&
            (!query.blockedOnly || item.action === 'block')
        )
        .sort((a, b) => b.at - a.at || (a.id < b.id ? 1 : a.id > b.id ? -1 : 0))
        .slice(0, query.limit)
        .map(item => ({ ...item }));
    },
    firewall_events_clear: () => {
      requireInstalled();
      requireConnected();
      activity = [];
      return null;
    },
    firewall_blocklists: () => {
      requireConnected();
      return blocklists.map(item => ({ ...item }));
    },
    firewall_blocklist_add: args => {
      requireInstalled();
      requireConnected();
      const source = String(args.source).trim();
      const url = /^https:\/\//i.test(source);
      if (!url && !source.startsWith('/') && !source.startsWith('~/'))
        throw appError('E_FW_BLOCKLIST_URL', 'unsupported_source');
      if (source.includes('broken')) throw appError('E_FW_BLOCKLIST_FORMAT', 'no_entries');
      const list: FirewallBlocklist = {
        id: id('list'),
        name: url ? new URL(source).host : (source.split('/').pop() ?? source),
        source,
        enabled: true,
        entries: url ? 12500 : 240,
        updatedAt: Date.now(),
        error: null
      };
      blocklists = [...blocklists, list];
      return { ...list };
    },
    firewall_blocklist_set: args => {
      requireInstalled();
      requireConnected();
      const list = blocklists.find(item => item.id === args.id);
      if (!list) throw appError('E_FW_BLOCKLIST_URL', 'unknown_list');
      list.enabled = Boolean(args.enabled);
      return { ...list };
    },
    firewall_blocklist_refresh: args => {
      requireInstalled();
      requireConnected();
      const list = blocklists.find(item => item.id === args.id);
      if (!list) throw appError('E_FW_BLOCKLIST_URL', 'unknown_list');
      list.updatedAt = Date.now();
      list.error = null;
      return { ...list };
    },
    firewall_blocklist_remove: args => {
      requireInstalled();
      requireConnected();
      blocklists = blocklists.filter(item => item.id !== args.id);
      return null;
    },
    firewall_settings: () => {
      requireBundled();
      requireConnected();
      return { ...settings };
    },
    firewall_settings_set: args => {
      requireInstalled();
      requireConnected();
      settings = { ...(args.settings as FirewallSettings) };
      status = { ...status, mode: settings.mode };
      later(publish);
      return { ...settings };
    }
  };

  /** A new connection asks while the page is open (component tests drive this directly). */
  function ask(app: keyof typeof APPS = 'firefox', remoteHost: string | null = 'example.org') {
    alerts = [...alerts, alert(APPS[app], remoteHost, '93.184.215.14', 443)];
    publishAlerts();
  }

  /** The extension answers again after not responding (component tests drive this directly). */
  function reconnect() {
    status = { ...status, connected: true, error: null };
    publish();
  }

  function stop() {
    for (const timer of timers) clearTimeout(timer);
    timers.clear();
  }

  return { handlers, stop, ask, reconnect };
}
