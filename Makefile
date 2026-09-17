.PHONY: deps migrate run test vet build check race integration
deps:
	docker compose up -d --wait
migrate:
	go run ./cmd/migrate up
run:
	go run ./cmd/server
test:
	go test ./... -count=1
vet:
	go vet ./...
build:
	go build ./...
check: test vet build
race:
	go test -race ./... -count=1
integration:
	RUN_INTEGRATION=1 MYSQL_ADDR=127.0.0.1:13306 MYSQL_DATABASE=locallife_test MYSQL_USER=locallife MYSQL_PASSWORD=locallife_dev REDIS_ADDR=127.0.0.1:16379 REDIS_PASSWORD= REDIS_DB=0 go test ./tests/integration -v -count=1
