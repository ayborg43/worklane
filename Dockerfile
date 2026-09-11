# syntax=docker/dockerfile:1

FROM golang:1.27-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/server ./cmd/server

FROM alpine:3.20
RUN apk add --no-cache ca-certificates tzdata && \
    addgroup -S app && adduser -S app -G app

WORKDIR /app
COPY --from=build /out/server ./server
# Templates and static assets are read from disk at runtime (html/template
# re-parses on every render, static/ is served via http.FileServer) rather
# than embedded — both need to ship alongside the binary. Migrations don't:
# they're go:embed'd into the binary at build time.
COPY web ./web

RUN mkdir -p data/attachments && chown -R app:app /app
USER app

EXPOSE 8080
ENTRYPOINT ["./server"]
