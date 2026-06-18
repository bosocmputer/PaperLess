# syntax=docker/dockerfile:1
# Next.js 14 production image (standalone output → small runtime layer).
FROM node:20-alpine AS deps
WORKDIR /app
COPY package.json package-lock.json* ./
RUN npm ci

FROM node:20-alpine AS build
WORKDIR /app
COPY --from=deps /app/node_modules ./node_modules
COPY . .
# Next.js evaluates rewrites() at BUILD time and compiles the destination into
# the standalone bundle (next.config.mjs is NOT shipped in the standalone output),
# so API_INTERNAL_URL must be a build ARG, not just a runtime env.
ARG API_INTERNAL_URL=http://api:8080
ENV API_INTERNAL_URL=${API_INTERNAL_URL}
RUN npm run build

FROM node:20-alpine AS runner
WORKDIR /app
ENV NODE_ENV=production
ENV PORT=3000
# Non-root for safety.
RUN addgroup -g 1001 -S nodejs && adduser -S nextjs -u 1001
# Standalone bundle + static assets + public dir.
COPY --from=build /app/public ./public
COPY --from=build --chown=nextjs:nodejs /app/.next/standalone ./
COPY --from=build --chown=nextjs:nodejs /app/.next/static ./.next/static
USER nextjs
EXPOSE 3000
CMD ["node", "server.js"]
