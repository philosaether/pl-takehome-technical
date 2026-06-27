GO ?= go
# Local sweep target (override for the cloud boxes):
PLQ_POSTGRES_DSN ?= postgres://plq:plq@localhost:5433/plq?sslmode=disable
WORKERS_SWEEP ?= 1 10 100 1000
# Per-task simulated work (ms) for the cost runs; `zero` is always swept too.
# Add 2000 for LLM-call-scale (its 1-worker point is thin — see loadgen-and-proofs).
PROCESS_MS ?= 2 20 200

PLQ_VALKEY_ADDR ?= localhost:6379
# Valkey shard sweep: each entry is one PLQ_VALKEY_ADDR (comma-separated primaries) =
# one shard count. The default 1/2/4 set is the linearity proof (head-to-head);
# load-test-valkey overrides this to a single addr for the 1-shard baseline.
# Needs the matching local instances up (`make up-valkey` brings up 4).
VALKEY_ADDRS_SWEEP ?= localhost:6379 \
                      localhost:6379,localhost:6380 \
                      localhost:6379,localhost:6380,localhost:6381,localhost:6382

.PHONY: build test proofs proofs-valkey fmt vet images up down up-valkey down-valkey \
        load-test load-test-valkey head-to-head sweep-postgres sweep-valkey graph \
        cloud-up cloud-down clean

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

## up-valkey / down-valkey: the Valkey datastores (Path 2) for the sweep + proofs.
## Brings up all 4 local instances (6379-6382) so the shard sweep can hit 1/2/4.
up-valkey:
	docker compose up -d valkey valkey-2 valkey-3 valkey-4

down-valkey:
	docker compose rm -sfv valkey valkey-2 valkey-3 valkey-4

## proofs-valkey: the correctness gate vs a live Valkey — conformance (8 scenarios)
## + ordering-under-crash. Needs PLQ_VALKEY_ADDR reachable (`make up-valkey`).
proofs-valkey:
	PLQ_TEST_VALKEY=$(PLQ_VALKEY_ADDR) $(GO) test ./tests/conformance/...
	PLQ_TEST_VALKEY=$(PLQ_VALKEY_ADDR) $(GO) test ./tests/proofs/...

## load-test: the integrated local sweep (workers x {zero, cost@Nms}) against the
## postgres path, then render the graph. Needs a reachable PLQ_POSTGRES_DSN
## (e.g. `make up` or a local container on :5433). Env-per-run: one process/point.
load-test:
	@mkdir -p results
	@rm -f results/sweep.csv results/sample-*.csv   # start fresh — don't append to a prior run
	@$(MAKE) sweep-postgres
	$(MAKE) graph

## load-test-valkey: the same sweep against the Valkey path (single shard = the
## baseline head-to-head point). Needs `make up-valkey` (or PLQ_VALKEY_ADDR set).
load-test-valkey:
	@mkdir -p results
	@rm -f results/sweep.csv results/sample-*.csv
	@$(MAKE) sweep-valkey VALKEY_ADDRS_SWEEP="$(PLQ_VALKEY_ADDR)"
	$(MAKE) graph

## head-to-head: PG sweep + Valkey sweep into ONE results/sweep.csv, then graph the
## overlaid throughput/latency comparison (M3 proof #4 — the decision gate). Needs
## both datastores reachable (`make up up-valkey`).
head-to-head:
	@mkdir -p results
	@rm -f results/sweep.csv results/sample-*.csv
	@$(MAKE) sweep-postgres
	@$(MAKE) sweep-valkey
	$(MAKE) graph

## sweep-postgres / sweep-valkey: one backend's worker x process sweep, APPENDING to
## results/sweep.csv (no clear, no graph) — the reusable unit behind the targets above.
sweep-postgres:
	@for n in $(WORKERS_SWEEP); do \
	  echo ">>> postgres workers=$$n process=zero"; \
	  PLQ_BACKEND=postgres PLQ_POSTGRES_DSN="$(PLQ_POSTGRES_DSN)" \
	  PLQ_WORKERS=$$n PLQ_PROCESS=zero PLQ_PRODUCERS=64 PLQ_RESULTS=./results \
	  $(GO) run -tags postgres ./cmd/plq loadrun ; \
	  for ms in $(PROCESS_MS); do \
	    echo ">>> postgres workers=$$n process=$${ms}ms"; \
	    PLQ_BACKEND=postgres PLQ_POSTGRES_DSN="$(PLQ_POSTGRES_DSN)" \
	    PLQ_WORKERS=$$n PLQ_PROCESS=cost PLQ_PROCESS_BASE=$${ms}ms PLQ_PRODUCERS=64 PLQ_RESULTS=./results \
	    $(GO) run -tags postgres ./cmd/plq loadrun ; \
	  done; \
	done

sweep-valkey:
	@for addrs in $(VALKEY_ADDRS_SWEEP); do \
	  shards=$$(echo $$addrs | tr ',' '\n' | grep -c .); \
	  : "shards here is for the log line only; the CSV's shards column is computed by shardCount() in cmd/plq/main.go — keep in sync"; \
	  for n in $(WORKERS_SWEEP); do \
	    echo ">>> valkey shards=$$shards workers=$$n process=zero"; \
	    PLQ_BACKEND=valkey PLQ_VALKEY_ADDR="$$addrs" \
	    PLQ_WORKERS=$$n PLQ_PROCESS=zero PLQ_PRODUCERS=64 PLQ_RESULTS=./results \
	    $(GO) run -tags valkey ./cmd/plq loadrun ; \
	    for ms in $(PROCESS_MS); do \
	      echo ">>> valkey shards=$$shards workers=$$n process=$${ms}ms"; \
	      PLQ_BACKEND=valkey PLQ_VALKEY_ADDR="$$addrs" \
	      PLQ_WORKERS=$$n PLQ_PROCESS=cost PLQ_PROCESS_BASE=$${ms}ms PLQ_PRODUCERS=64 PLQ_RESULTS=./results \
	      $(GO) run -tags valkey ./cmd/plq loadrun ; \
	    done; \
	  done; \
	done

## graph: render results/*.csv → PNGs (needs matplotlib: pip install matplotlib)
graph:
	python3 scripts/plot.py results

## cloud-up / cloud-down: provision/tear down the canonical AWS boxes (M2 cloud half).
## cloud-down is `terraform destroy` — the spend control. GATED: only run when ready.
cloud-up:
	cd deploy/terraform && terraform init && terraform apply -auto-approve

cloud-down:
	cd deploy/terraform && terraform destroy -auto-approve

clean:
	rm -rf ./results
	$(GO) clean
