FROM node:22-alpine
ENV NODE_ENV=production
WORKDIR /app
COPY package.json package-lock.json ./
RUN npm ci --omit=dev && chown -R node:node /app
COPY --chown=node:node server.js ./
USER node
EXPOSE 3000
CMD ["node", "server.js"]
