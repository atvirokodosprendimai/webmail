# syntax=docker/dockerfile:1

FROM golang:1.25-alpine AS build
WORKDIR /src
RUN apk add --no-cache git ca-certificates
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN go tool templ generate
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/webmail ./cmd/webmail

FROM gcr.io/distroless/static-debian12:nonroot
WORKDIR /app
# Single binary — static CSS and SQL migrations are embedded via //go:embed.
COPY --from=build /out/webmail /app/webmail
EXPOSE 8080
USER nonroot:nonroot
ENTRYPOINT ["/app/webmail"]
