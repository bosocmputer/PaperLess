# syntax=docker/dockerfile:1
FROM golang:1.24-alpine AS build
WORKDIR /src
COPY go.mod go.sum* ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -o /out/paperless-api ./cmd/server && \
    CGO_ENABLED=0 go build -o /out/migrate ./cmd/migrate

FROM alpine:3.20
RUN apk add --no-cache ca-certificates
COPY --from=build /out/paperless-api /usr/local/bin/paperless-api
COPY --from=build /out/migrate /usr/local/bin/migrate
COPY migrations/ /app/migrations/
ENV MIGRATIONS_PATH=/app/migrations
EXPOSE 8080
ENTRYPOINT ["/usr/local/bin/paperless-api"]
