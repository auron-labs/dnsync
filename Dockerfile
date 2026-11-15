FROM --platform=$BUILDPLATFORM golang:1.27.4 AS build

WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .

ARG TARGETOS=linux
ARG TARGETARCH=amd64
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH go build -o /out/dnsync ./cmd/dnsync

FROM gcr.io/distroless/static-debian13

WORKDIR /
COPY --from=build /out/dnsync /usr/local/bin/dnsync

ENTRYPOINT ["/usr/local/bin/dnsync"]
CMD ["-config", "/etc/dnsync/config.toml"]
