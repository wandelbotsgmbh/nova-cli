FROM node:26.13.1-alpine AS base
WORKDIR /app
ENV NODE_ENV=production \
    NEXT_TELEMETRY_DISABLED=1

# pnpm only for the build stages (Node 25+ no longer ships corepack)
FROM base AS pnpm
RUN npm install -g pnpm@10.24.0 && npm cache clean --force

# Install dependencies
FROM pnpm AS deps
COPY package.json pnpm-lock.yaml ./
RUN pnpm install --frozen-lockfile

# Build the app
FROM pnpm AS builder
COPY . .
COPY --from=deps /app/node_modules ./node_modules
RUN pnpm run build

# Production image: node, the standalone build and ripgrep for the entrypoint
FROM base AS runner
# Used in entrypoint base path injection for startup speed (~500x faster)
RUN apk add --no-cache ripgrep
COPY --from=builder /app/.next/standalone ./
COPY --from=builder /app/public ./public
COPY --from=builder /app/.next/static ./.next/static
COPY --from=builder /app/scripts/entrypoint.sh ./entrypoint.sh

EXPOSE 3000

ENTRYPOINT ["/bin/sh", "./entrypoint.sh"]
