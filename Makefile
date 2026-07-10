.PHONY: build test lint docker clean

VERSION ?= dev
IMAGE ?= ghcr.io/runwhen-contrib/vpa-provisioner:$(VERSION)
BINARY := bin/vpa-provisioner

build:
	CGO_ENABLED=0 go build -ldflags="-s -w -X main.version=$(VERSION)" -o $(BINARY) ./cmd/vpa-provisioner

test:
	go test ./...

lint:
	go vet ./...

docker:
	docker build --build-arg VERSION=$(VERSION) -t $(IMAGE) .

clean:
	rm -rf bin/
