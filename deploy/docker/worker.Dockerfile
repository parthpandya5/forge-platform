FROM golang:1.25-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/forge-worker ./apps/worker

FROM alpine:3.21
RUN apk add --no-cache ca-certificates git docker-cli docker-cli-buildx && addgroup -S forge && adduser -S -G forge forge
COPY --from=build /out/forge-worker /forge-worker
USER forge:forge
EXPOSE 9090
ENTRYPOINT ["/forge-worker"]
