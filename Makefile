.PHONY: help docker-build test-coverage

help:
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | sort | awk 'BEGIN {FS = ":.*?## "}; {printf "\033[36m%-30s\033[0m %s\n", $$1, $$2}'

docker-build: ## build docker image
	docker build -t capturedcheckpoints-go:now 

test-coverage: ## Generate test coverage report
	mkdir -p tmp
	go test ./... --coverprofile tmp/outfile
	go tool cover -html=tmp/outfile

report-card: ## Generate static analysis report
	goreportcard-cli -v
