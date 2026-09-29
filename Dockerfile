FROM golang:1.23-alpine AS build

WORKDIR /src
COPY go.mod ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/pikpak-bridge ./cmd/pikpak-bridge

FROM alpine:3.21
RUN apk add --no-cache ca-certificates tzdata
WORKDIR /app
COPY --from=build /out/pikpak-bridge /usr/local/bin/pikpak-bridge
COPY config.example.yaml /app/config.example.yaml
VOLUME ["/data"]
EXPOSE 8080
ENV PIKPAK_BRIDGE_CONFIG=/app/config.yaml
ENTRYPOINT ["pikpak-bridge"]
