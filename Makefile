.PHONY: all dev test go-test go-build lint build clean doctor prune-cache

all: lint test go-test build

dev:
	npm run dev

test:
	npm test -- --run

go-test:
	cd backend-go && go test -v ./...

go-build:
	cd backend-go && go build -o /tmp/air-traffic-control-daemon main.go && ln -sf /tmp/air-traffic-control-daemon /tmp/prox-orch-daemon

lint:
	npm run lint

build:
	npm run build

doctor:
	bash scripts/cluster-doctor.sh

prune-cache:
	npx tsx scripts/prune-cache.ts

clean:
	rm -rf dist /tmp/air-traffic-control-daemon /tmp/prox-orch-daemon
