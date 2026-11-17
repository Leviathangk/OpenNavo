import type { CreateWebApp, WebApp } from '../bindings';

type Args = Record<string, unknown>;

/** Browser-mode stand-in for the local web app commands. */
export function createWebappsMock() {
  let apps: WebApp[] = [];
  let counter = 0;
  return {
    webapps_list: () => apps.map(app => ({ ...app })),
    webapps_create: (args: Args) => {
      const input = args.input as CreateWebApp;
      if (apps.some(app => app.name === input.name))
        throw { code: 'E_WEBAPP_EXISTS', message: 'webapp_exists', detail: null };
      const id = `mock-webapp-${++counter}`;
      const app: WebApp = {
        id,
        name: input.name,
        url: input.url,
        path: `/Users/mock/Applications/${input.name}.app`,
        bundleId: `com.opennavo.webapp.mock${counter}`,
        createdAt: new Date().toISOString(),
        icon: null
      };
      apps = [app, ...apps];
      return { ...app };
    },
    webapps_delete: (args: Args) => {
      apps = apps.filter(app => app.id !== args.id);
      return null;
    },
    webapps_open: () => null,
    webapps_reveal: () => null
  };
}
