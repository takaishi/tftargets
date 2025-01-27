.PHONY: build
build:
	go build -o dist/tftargets ./cmd/tftargets

.PHONY: install
install:
	go install github.com/takaishi/tftargets/cmd/tftargets

.PHONY: test
test:
	go test -race ./...
