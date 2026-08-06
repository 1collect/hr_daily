FROM node:22-alpine AS web-build
WORKDIR /web
COPY web/package.json web/tsconfig.json ./
RUN npm install
COPY web/src ./src
RUN npm run build

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
COPY migrations /app/migrations
EXPOSE 8080
CMD ["/app/hr-server"]
