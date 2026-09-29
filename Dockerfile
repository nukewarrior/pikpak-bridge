FROM --platform=$BUILDPLATFORM golang:1.23-alpine AS build

ARG TARGETOS
ARG TARGETARCH

WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH \
    go build -trimpath -ldflags="-s -w" -o /out/pikpak-bridge ./cmd/pikpak-bridge

FROM alpine:3.21
RUN apk add --no-cache ca-certificates tzdata
WORKDIR /app
COPY --from=build /out/pikpak-bridge /usr/local/bin/pikpak-bridge
COPY config.example.yaml /app/config.example.yaml
VOLUME ["/data"]
EXPOSE 8080
ENV PIKPAK_BRIDGE_CONFIG=/data/config.yaml
ENTRYPOINT ["pikpak-bridge"]
