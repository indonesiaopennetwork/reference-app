# Build stage
FROM node:22-alpine AS builder

WORKDIR /app
COPY package*.json ./
RUN npm install
COPY . .
RUN npm run build

# Production stage — nginx serves the SPA and proxies /api to the BAP
FROM nginx:stable-alpine

COPY --from=builder /app/dist /usr/share/nginx/html
COPY nginx.conf /etc/nginx/templates/default.conf.template
COPY env.sh /docker-entrypoint.d/40-env.sh
RUN chmod +x /docker-entrypoint.d/40-env.sh
ENV NGINX_ENVSUBST_FILTER='^(BAP_URL|NGINX_RESOLVER)$'

EXPOSE 80
CMD ["nginx", "-g", "daemon off;"]
