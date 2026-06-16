# syntax=docker/dockerfile:1
FROM golang:1.24-alpine AS build
WORKDIR /src
COPY go.mod go.sum* ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -o /out/paperless-api ./cmd/server

FROM alpine:3.20
RUN apk add --no-cache ca-certificates
COPY --from=build /out/paperless-api /usr/local/bin/paperless-api
EXPOSE 8080
ENTRYPOINT ["/usr/local/bin/paperless-api"]
