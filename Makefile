.PHONY: test
test:
	go test ./...

.PHONY: build
build:
	go fmt ./...
	go build -o ./bin/sched ./sched.go

.PHONY: run
run:
	./hack/run.sh

# re-generate openapi file for running api-server
.PHONY: openapi
openapi:
	./hack/openapi.sh
