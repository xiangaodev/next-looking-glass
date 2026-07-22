BINARY  := next-looking-glass
LDFLAGS := -s -w

.PHONY: all build run vet fmt clean docker release

all: vet build

build:
	go build -trimpath -ldflags="$(LDFLAGS)" -o bin/$(BINARY) .

run: build
	./bin/$(BINARY) -config config.yaml

vet:
	go vet ./...

fmt:
	gofmt -w .

tidy:
	go mod tidy

clean:
	rm -rf bin/

docker:
	docker build -t nimbus/next-looking-glass .

# Cross-compile release binaries for Linux nodes.
release:
	GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -trimpath -ldflags="$(LDFLAGS)" -o bin/$(BINARY)-linux-amd64 .
	GOOS=linux GOARCH=arm64 CGO_ENABLED=0 go build -trimpath -ldflags="$(LDFLAGS)" -o bin/$(BINARY)-linux-arm64 .
