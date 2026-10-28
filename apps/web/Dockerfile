# Web image (Nitro Node server, port 3000). Build from the repository root:
#   docker build -f apps/web/Dockerfile -t opennavo-web .
# Inject URLs at runtime through NUXT_* environment variables (05 §9); the image contains no environment-specific configuration.
FROM node:24.19.0-alpine@sha256:d32cdf619f63fe0471182d08996dd516c6275bb5fd31ae06e55a570bd9e1ad43 AS build
WORKDIR /repo
RUN corepack enable
# Dependencies install from the manifests alone, so the layer is reused until
# one of them or the lockfile changes; before, every source change reinstalled
# them and exported the result to the build cache again. Add a manifest here
# when the web app gains a workspace dependency (the install fails otherwise).
COPY package.json pnpm-lock.yaml pnpm-workspace.yaml ./
COPY patches/ patches/
COPY apps/web/package.json apps/web/
COPY packages/api/package.json packages/api/
COPY packages/shared/package.json packages/shared/
COPY packages/tokens/package.json packages/tokens/
COPY packages/ui/package.json packages/ui/
# Lifecycle scripts need the sources: dependency builds run in `pnpm rebuild`
# below, and `nuxt build` does what the web app's `nuxt prepare` would.
RUN pnpm install --frozen-lockfile --filter "@opennavo/web..." --ignore-scripts
COPY . .
RUN pnpm rebuild --filter "@opennavo/web..."
RUN NUXT_BUILD_ID="$(node apps/web/scripts/build-fingerprint.ts)" pnpm --filter @opennavo/web build

FROM node:24.19.0-alpine@sha256:d32cdf619f63fe0471182d08996dd516c6275bb5fd31ae06e55a570bd9e1ad43
# Production only runs Node; package managers and their dependency trees are unnecessary.
RUN apk upgrade --no-cache && rm -rf /usr/local/lib/node_modules /opt/yarn-* && \
    rm -f /usr/local/bin/npm /usr/local/bin/npx /usr/local/bin/corepack /usr/local/bin/yarn /usr/local/bin/yarnpkg
WORKDIR /app
# Keep a deterministic V8 cap slightly above the current default while leaving about 200 MiB of the
# 768 MiB production cgroup ceiling for native image/WASM allocations, Buffers, and the process itself.
# This bounds capacity; it is not a leak fix. Eight MiB semi-spaces reduce short-lived SSR bursts.
ENV NODE_ENV=production HOST=0.0.0.0 PORT=3000 NODE_OPTIONS="--max-old-space-size=448 --max-semi-space-size=8"
COPY --from=build --chown=node:node /repo/apps/web/.output ./.output
COPY --chown=node:node docs/legal/third-party/ /usr/share/licenses/opennavo/
USER node
EXPOSE 3000
CMD ["node", ".output/server/index.mjs"]
