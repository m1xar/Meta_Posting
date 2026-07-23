.PHONY: fmt test vet build run compose-up compose-down logs

fmt:
	gofmt -w $$(find cmd internal -name '*.go' -type f)

test:
	go test ./...

vet:
	go vet ./...

build:
	mkdir -p bin
	go build -o bin/api ./cmd/api
	go build -o bin/worker ./cmd/worker

run:
	go run ./cmd/api

compose-up:
	docker compose up -d --build

compose-down:
	docker compose down

logs:
	docker compose logs -f --tail=200
