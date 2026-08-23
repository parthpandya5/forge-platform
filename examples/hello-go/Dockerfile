FROM golang:1.25-alpine AS build
WORKDIR /src
COPY go.mod main.go ./
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /hello-go .

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /hello-go /hello-go
USER nonroot:nonroot
EXPOSE 8080
ENTRYPOINT ["/hello-go"]
