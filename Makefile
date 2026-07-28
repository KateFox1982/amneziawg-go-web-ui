# Setting SHELL to bash allows bash commands to be executed in recipes.
# This is a requirement for 'setup-envtest.sh' in the test target.
# Options are set to exit when a recipe line exits with a non-zero status, or if a command in a pipeline fails.
SHELL = /usr/bin/env bash -o pipefail
.SHELLFLAGS = -ec

##@ General

# The help target prints all targets with their descriptions organized
# by category. Categories are represented by '##@' and target descriptions by '##'.
# The awk command is responsible for reading all makefiles included in this
# invocation, looking for lines of the form xyz: ## something, and then
# pretty-printing the target and help. If there is a line with ##@ something,
# it's pretty-printed as a category.
# More info on the awk command:
# http://linuxcommand.org/lc3_adv_awk.php
# More info on ANSI escape codes for terminal formatting:
# https://en.wikipedia.org/wiki/ANSI_escape_code#SGR_parameters

.PHONY: help
help: ## List all available commands
	@awk 'BEGIN {FS = ":.*##"; printf "\nUsage:\n  make \033[36m<target>\033[0m\n"} /^[a-zA-Z_0-9-]+:.*?##/ { printf "  \033[36m%-15s\033[0m %s\n", $$1, $$2 } /^##@/ { printf "\n\033[1m%s\033[0m\n", substr($$0, 5) } ' $(MAKEFILE_LIST)

##@ Backend

.PHONY: run
run: ## Build image from sources and run via docker-compose
	docker compose -f docker-compose.yml -f docker-compose.build.yml up --build

.PHONY: build
build: check-go ## Build the application to verify compilation
	go build -o server.bin .

.PHONY: exec
exec: ## Execute a command inside the container
	docker compose exec app sh

##@ Backend utilities

.PHONY: check-go
check-go: ## Check Go version
	@go version | grep -q 'go1\.26' || (echo "Please use Go 1.26.X"; exit 1)

.PHONY: vet
vet: check-go ## Run go vet
	go vet ./...

.PHONY: fix
fix: check-go ## Run go fix
	go fix ./...

.PHONY: deploy
deploy: ## Deploy via docker (host name is requested at runtime)
	@read -p "Enter host for deployment: " HOST; \
	echo "build"; \
	docker buildx build --load --file Dockerfile --progress=plain --tag "proxy:latest" .; \
	echo "exporting image"; \
	docker save "proxy:latest" > "proxy.tar"; \
	echo "remove local image"; \
	docker image rm -f "proxy:latest"; \
	echo "copying to $$HOST"; \
	rsync -avzP --mkpath .env \
		Makefile \
		docker-compose.yml \
		proxy.tar \
		"$$HOST:/usr/local/include/proxy/"; \
	echo "deploying on $$HOST"; \
	ssh "$$HOST" "cd /usr/local/include/proxy && (docker compose down || true) && (docker image rm -f 'proxy:latest' || true) && docker load < proxy.tar && docker compose up -d && rm -rf /usr/local/include/proxy"


