MODULE  := github.com/davidwalter0/lspbridge/org-lsp
COV_OUT := coverage.out

.PHONY: all build install lint vet vuln test cov cov-html clean check tidy

all: build

build:
	go build -o bin/org-lsp ./cmd/org-lsp

install:
	go install ./cmd/org-lsp

tidy:
	go mod tidy

lint:
	golangci-lint run ./...

vet:
	go vet ./...

vuln:
	govulncheck ./...

test:
	go test ./...

check: lint vet vuln test cov

cov:
	@go test -coverprofile=$(COV_OUT) ./pkg/... 2>&1 | \
		grep -E 'coverage:|no test files' | \
		sed 's|.*$(MODULE)/||; s/\t/ /g' | \
		awk '{ pkg=$$1; cov="0.0%"; for(i=1;i<=NF;i++) if($$i ~ /%/) cov=$$i; printf "%-45s %s\n", pkg, cov }' | \
		sort | \
		(echo ""; printf "%-45s %s\n" "Package" "Coverage"; \
		 printf "%-45s %s\n" "─────────────────────────────────────────────" "────────"; \
		 cat; \
		 printf "%-45s %s\n" "─────────────────────────────────────────────" "────────"; \
		 printf "%-45s %s\n" "TOTAL" "$$(go tool cover -func=$(COV_OUT) | tail -1 | awk '{print $$NF}')")

cov-html:
	go test -coverprofile=$(COV_OUT) ./pkg/...
	go tool cover -html=$(COV_OUT)

clean:
	rm -f $(COV_OUT)
	rm -rf bin/
