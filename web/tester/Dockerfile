# Tester UI image — tiny Go binary + the static folder baked in. Only used
# by compose/dev.yml; the tester is not a production artifact.
FROM docker.io/golang:1.26-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/tester .

FROM gcr.io/distroless/static-debian12:nonroot
WORKDIR /app
COPY --from=build /out/tester /app/tester
COPY static /app/static
ENV TESTER_STATIC=/app/static
EXPOSE 4000
USER nonroot:nonroot
ENTRYPOINT ["/app/tester"]
