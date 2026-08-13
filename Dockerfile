FROM node:22-alpine AS web-build
WORKDIR /web
COPY web/package.json web/tsconfig.json ./
RUN npm install
COPY web/src ./src
RUN npm run build \
    && web_app_hash="$(sha256sum dist/app.js | cut -c1-12)" \
    && web_css_hash="$(sha256sum dist/ui.css | cut -c1-12)" \
    && mv dist/app.js "dist/app.${web_app_hash}.js" \
    && mv dist/ui.css "dist/ui.${web_css_hash}.css" \
    && sed -i "s|/app.js|/app.${web_app_hash}.js|;s|/ui.css|/ui.${web_css_hash}.css|" dist/index.html

FROM golang:1.23-alpine AS build
WORKDIR /src
COPY go.mod go.sum* ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -o /out/hr-server ./cmd/server

FROM alpine:3.21
RUN adduser -D -u 10001 app
USER app
WORKDIR /app
COPY --from=build /out/hr-server /app/hr-server
COPY --from=web-build /web/dist /app/web
EXPOSE 8080
CMD ["/app/hr-server"]
