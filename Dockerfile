# syntax=docker/dockerfile:1

FROM golang:1.22-alpine AS builder

RUN apk add --no-cache ca-certificates git

WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

COPY . .

ARG VERSION=dev
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build \
    -ldflags="-s -w -X main.version=${VERSION}" \
    -o /out/vpa-provisioner ./cmd/vpa-provisioner

FROM gcr.io/distroless/static:nonroot

COPY --from=builder /out/vpa-provisioner /vpa-provisioner

USER 65532:65532
ENTRYPOINT ["/vpa-provisioner"]
