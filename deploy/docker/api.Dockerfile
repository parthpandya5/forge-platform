FROM golang:1.25-alpine AS build
WORKDIR /src
COPY go.mod go.sum* ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/forge-api ./apps/api
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/forge-migrate ./apps/migrate

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/forge-api /forge-api
COPY --from=build /out/forge-migrate /forge-migrate
COPY migrations /migrations
USER nonroot:nonroot
EXPOSE 8080
ENTRYPOINT ["/forge-api"]
