FROM golang:1.27.1-bookworm AS build
WORKDIR /src
ENV GOWORK=off CGO_ENABLED=0
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN go build -trimpath -ldflags="-s -w" -o /out/xops-mcp ./cmd/xops-mcp \
    && mkdir /out/data

FROM scratch
COPY --from=build /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/
COPY --from=build /out/xops-mcp /xops-mcp
COPY --from=build --chown=65532:65532 /out/data /var/lib/xops-mcp
# Storage creates its private 0700 data/ directory inside this writable volume.
USER 65532:65532
EXPOSE 8080 8081
ENTRYPOINT ["/xops-mcp"]
CMD ["serve", "--config", "/etc/xops-mcp/server.yaml"]
