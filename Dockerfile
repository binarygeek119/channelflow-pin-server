FROM golang:1.22-bookworm AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /pinserver ./cmd/pinserver

FROM debian:bookworm-slim
RUN apt-get update \
  && DEBIAN_FRONTEND=noninteractive apt-get install -y --no-install-recommends ca-certificates \
  && rm -rf /var/lib/apt/lists/*
COPY --from=build /pinserver /usr/local/bin/pinserver
ENV CERT_DIR=/certs
VOLUME ["/certs"]
EXPOSE 80 443 43123
USER root
ENTRYPOINT ["/usr/local/bin/pinserver"]
