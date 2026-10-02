FROM golang:1.27-alpine AS builder

WORKDIR /src
RUN apk add --no-cache ca-certificates
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/alertmanager-route-tester .

FROM scratch
COPY --from=builder /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/
COPY --from=builder /out/alertmanager-route-tester /alertmanager-route-tester
USER 65532:65532
EXPOSE 8080
ENTRYPOINT ["/alertmanager-route-tester"]
CMD ["-config", "/etc/alertmanager-route-tester/config.yaml"]
