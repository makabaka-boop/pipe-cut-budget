# syntax=docker/dockerfile:1

FROM golang:1.23-alpine AS build
WORKDIR /src
COPY go.mod ./
COPY cmd ./cmd
COPY internal ./internal
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/api ./cmd/api \
    && CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/verify ./cmd/verify

FROM alpine:3.20 AS api
RUN adduser -D -u 10001 app
COPY --from=build /out/api /usr/local/bin/api
USER app
EXPOSE 8080
ENTRYPOINT ["/usr/local/bin/api"]

FROM golang:1.23-alpine AS verify
WORKDIR /src
COPY . .
COPY --from=build /out/verify /usr/local/bin/verify
ENTRYPOINT ["sh", "-c", "go test ./... && exec /usr/local/bin/verify"]
