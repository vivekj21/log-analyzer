FROM golang:1.24 AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -o /out/analyzer ./cmd/analyzer

FROM alpine:3.20
RUN apk add --no-cache git ca-certificates kubectl && adduser -D -u 10001 app
COPY --from=build /out/analyzer /usr/local/bin/analyzer
USER 10001
ENTRYPOINT ["analyzer"]
