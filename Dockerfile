# Stage 1: Build Go binary
FROM golang:1.24-alpine AS go-builder
WORKDIR /build
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o nebulacb ./cmd/nebulacb
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o nebulacb-cli ./cmd/cli

# Stage 2: Build React UI
FROM node:20-alpine AS ui-builder
WORKDIR /build
COPY web/nebulacb-ui/package*.json ./
RUN npm ci --silent
COPY web/nebulacb-ui/ ./
RUN npm run build

# Stage 3: Final image
FROM alpine:3.19
RUN apk --no-cache add ca-certificates curl kubectl helm
WORKDIR /app

COPY --from=go-builder /build/nebulacb /app/nebulacb
COPY --from=go-builder /build/nebulacb-cli /usr/local/bin/nebulacb
COPY --from=ui-builder /build/build /app/web/nebulacb-ui/build

RUN mkdir -p /app/reports

EXPOSE 8080 9090

ENTRYPOINT ["/app/nebulacb"]
CMD ["--config", "/app/config.json"]
