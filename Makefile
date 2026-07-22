.PHONY: build test lint deploy

build:
	go build ./...

test:
	go test -race -count=1 ./...
	pytest sync/

lint:
	ruff check sync/
	ruff format --check sync/
	mypy --config-file sync/pyproject.toml sync/sync.py
	golangci-lint run ./...

deploy:
	bash scripts/deploy.sh
