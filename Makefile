GO ?= go

.PHONY: build test proofs fmt vet images up down load-test load-test-valkey clean

## build: compile the default (in-memory) binary + typecheck all shared code
build:
	$(GO) build ./...

## test: run the unit tests (in-memory oracle)
test:
	$(GO) test ./...

## proofs: deterministic proofs — M0 smoke proofs live in internal/queue; M2 fills proofs/
proofs:
	$(GO) test ./internal/queue/... ./proofs/...

## fmt / vet: formatting + static checks
fmt:
	$(GO) fmt ./...

vet:
	$(GO) vet ./...
	$(GO) vet -tags postgres ./...
	$(GO) vet -tags valkey ./...

## images: build the two per-path single-driver images (build-tag selected)
images:
	docker build -f deploy/postgres.Dockerfile -t plq:postgres .
	docker build -f deploy/valkey.Dockerfile  -t plq:valkey  .

## up / down: local compose (postgres path) for dev
up:
	docker compose up -d --build

down:
	docker compose down -v

## load-test: THE one-command repro. M0 stubs the harness; M2 runs the sweep +
## emits the throughput-vs-workers graph to ./results. Wired now, real in M2.
load-test:
	@echo "M0: load-test harness lands in M2 (sweep 1/10/100/1000 x process-model grid)."
	@echo "    Today: 'make test' runs the in-memory M0 proofs; 'make up' brings up the postgres path."

load-test-valkey:
	@echo "M0: valkey head-to-head lands in M3 (see roadmap.md)."

clean:
	rm -rf ./results
	$(GO) clean
