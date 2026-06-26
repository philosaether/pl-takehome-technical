GO ?= go
# Local sweep target (override for the cloud boxes):
PLQ_POSTGRES_DSN ?= postgres://plq:plq@localhost:5433/plq?sslmode=disable
WORKERS_SWEEP ?= 1 10 100 1000
# Per-task simulated work (ms) for the cost runs; `zero` is always swept too.
# Add 2000 for LLM-call-scale (its 1-worker point is thin — see loadgen-and-proofs).
PROCESS_MS ?= 2 20 200

.PHONY: build test proofs fmt vet images up down load-test load-test-valkey graph cloud-up cloud-down clean

## build: compile the default (in-memory) binary + typecheck all shared code
build:
	$(GO) build ./...

## test: run the unit tests (in-memory oracle)
test:
	$(GO) test ./...

## proofs: deterministic proofs (gate/flush in conformance; ordering in proofs/) +
## the look-ahead scaling bench. Each integration package runs in its OWN invocation
## (separate process) so they never hit the shared DB concurrently. Set
## PLQ_TEST_POSTGRES to run them against postgres + the look-ahead curve.
proofs:
	$(GO) test ./internal/queue/...
	$(GO) test ./tests/conformance/...
	$(GO) test ./tests/proofs/...
	$(GO) test ./tests/bench/...

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

## load-test: the integrated local sweep (workers x {zero, cost@2ms}) against the
## postgres path, then render the graph. Needs a reachable PLQ_POSTGRES_DSN
## (e.g. `make up` or a local container on :5433). Env-per-run: one process/point.
load-test:
	@mkdir -p results
	@rm -f results/sweep.csv results/sample-*.csv   # start fresh — don't append to a prior run
	@for n in $(WORKERS_SWEEP); do \
	  echo ">>> workers=$$n process=zero"; \
	  PLQ_BACKEND=postgres PLQ_POSTGRES_DSN="$(PLQ_POSTGRES_DSN)" \
	  PLQ_WORKERS=$$n PLQ_PROCESS=zero PLQ_PRODUCERS=64 PLQ_RESULTS=./results \
	  $(GO) run -tags postgres ./cmd/plq loadrun ; \
	  for ms in $(PROCESS_MS); do \
	    echo ">>> workers=$$n process=$${ms}ms"; \
	    PLQ_BACKEND=postgres PLQ_POSTGRES_DSN="$(PLQ_POSTGRES_DSN)" \
	    PLQ_WORKERS=$$n PLQ_PROCESS=cost PLQ_PROCESS_BASE=$${ms}ms PLQ_PRODUCERS=64 PLQ_RESULTS=./results \
	    $(GO) run -tags postgres ./cmd/plq loadrun ; \
	  done; \
	done
	$(MAKE) graph

## graph: render results/*.csv → PNGs (needs matplotlib: pip install matplotlib)
graph:
	python3 scripts/plot.py results

load-test-valkey:
	@echo "valkey head-to-head lands in M3 (see roadmap.md)."

## cloud-up / cloud-down: provision/tear down the canonical AWS boxes (M2 cloud half).
## cloud-down is `terraform destroy` — the spend control. GATED: only run when ready.
cloud-up:
	cd deploy/terraform && terraform init && terraform apply -auto-approve

cloud-down:
	cd deploy/terraform && terraform destroy -auto-approve

clean:
	rm -rf ./results
	$(GO) clean
