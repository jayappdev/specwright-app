# SpecWright tasks. `make check` runs what CI runs.
.PHONY: check test test-race vet fmt fmt-check ui-check ui-test ui-lint ui-fmt ui-fmt-check ui-build build dev

check: fmt-check vet test ui-check

# --- Go ---------------------------------------------------------------------
test:
	go test ./...

test-race:
	go test -race ./...

vet:
	go vet ./...

fmt:
	gofmt -w $$(git ls-files '*.go')

fmt-check:
	@out=$$(gofmt -l $$(git ls-files '*.go')); if [ -n "$$out" ]; then echo "gofmt needed:"; echo "$$out"; exit 1; fi

# --- frontend ---------------------------------------------------------------
ui-check: ui-lint ui-fmt-check ui-test ui-build

ui-test:
	npm --prefix frontend test

ui-lint:
	npm --prefix frontend run lint

ui-fmt:
	npm --prefix frontend run format

ui-fmt-check:
	npm --prefix frontend run format:check

ui-build:
	npm --prefix frontend run build

# --- app --------------------------------------------------------------------
build:
	wails build

# browser preview of the UI on http://localhost:5173 (mock backend)
dev:
	npm --prefix frontend run dev
