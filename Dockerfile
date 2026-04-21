# Multi-stage build for the inquiryiq service. The runtime image is distroless
# static — no shell, minimal CVE surface, ~20MB. This Dockerfile is consumed
# by compose/dev.yml; production builds go through the same file.
FROM docker.io/golang:1.26-alpine AS build
WORKDIR /src

# Cache module downloads between iterations.
COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/inquiryiq ./cmd/server

FROM gcr.io/distroless/static-debian12:nonroot
WORKDIR /app
COPY --from=build /out/inquiryiq /app/inquiryiq
EXPOSE 8080
USER nonroot:nonroot
ENTRYPOINT ["/app/inquiryiq"]
