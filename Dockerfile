# Build on the fast build machine and cross-compile for the target (amd64 or arm64).
FROM --platform=$BUILDPLATFORM golang:1.26.11 AS build
ARG TARGETOS
ARG TARGETARCH
ARG VERSION=dev
ARG COMMIT=unknown
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH go build -trimpath \
    -ldflags "-s -w -X main.version=$VERSION -X main.commit=$COMMIT" \
    -o /out/log-analyzer ./cmd/analyzer

FROM alpine:3.24
RUN apk add --no-cache git ca-certificates kubectl && adduser -D -u 10001 app
COPY --from=build /out/log-analyzer /usr/local/bin/log-analyzer
USER 10001
ENTRYPOINT ["log-analyzer"]
