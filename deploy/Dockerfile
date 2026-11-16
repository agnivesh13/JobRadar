# One image for every Go service; the start command picks the role.
# Build context is the repo root.

FROM node:22-alpine AS web
WORKDIR /web
COPY web/package*.json ./
RUN npm ci
COPY web/ ./
RUN npm run build

FROM golang:1.27 AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY cmd/ cmd/
COPY internal/ internal/
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/jobradar ./cmd/jobradar

FROM alpine:3.22 AS app
RUN apk add --no-cache ca-certificates tzdata wget && adduser -D -u 10001 jobradar
WORKDIR /app
COPY --from=build /out/jobradar /usr/local/bin/jobradar
COPY --from=web /web/dist /app/web
COPY seeds/ /app/seeds/
ENV WEB_DIR=/app/web
USER jobradar
ENTRYPOINT ["jobradar"]

# Same binary plus headless Chromium, for the renderer only.
FROM app AS renderer
USER root
RUN apk add --no-cache chromium
USER jobradar
