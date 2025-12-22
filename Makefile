.PHONY: build
build:
	go build -o dist/tftargets ./cmd/tftargets

.PHONY: install
install:
	go install github.com/takaishi/tftargets/cmd/tftargets

.PHONY: test
test:
	go test -race ./...

.PHONY: check-licenses
check-licenses:
	go-licenses check ./... --disallowed_types=permissive,forbidden,restricted --include_tests

.PHONY: credits
credits:
	gocredits -skip-missing . > CREDITS

.PHONY: check-credits
check-credits:
	@echo "Checking CREDITS file..."
	@temp_file=$$(mktemp) && \
	gocredits -skip-missing . > $$temp_file && \
	if ! diff -q CREDITS $$temp_file > /dev/null 2>&1; then \
		echo "CREDITS file is out of date. Please run 'make credits' to update it."; \
		echo "Diff:"; \
		diff -u CREDITS $$temp_file || true; \
		rm -f $$temp_file; \
		exit 1; \
	fi && \
	rm -f $$temp_file && \
	echo "CREDITS file is up to date."

depsdev:
	go install github.com/Songmu/gocredits/cmd/gocredits@latest
	go install github.com/google/go-licenses/v2@latest
