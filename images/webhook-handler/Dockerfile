# Webhook handler image. The handler is control-plane only (verify signature,
# claim work, create Jobs) and runs no agent code, so distroless is fine.
FROM golang:1.24 AS builder
ARG TARGETOS=linux
ARG TARGETARCH=amd64
WORKDIR /workspace

COPY go.mod go.sum ./
RUN go mod download

COPY cmd/ cmd/
COPY api/ api/
COPY internal/ internal/

RUN CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} \
    go build -trimpath -ldflags="-s -w" -o /out/webhook-handler ./cmd/webhook-handler

FROM gcr.io/distroless/static:nonroot
WORKDIR /
COPY --from=builder /out/webhook-handler /webhook-handler
USER 65532:65532
EXPOSE 8080
ENTRYPOINT ["/webhook-handler"]
