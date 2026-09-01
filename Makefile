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

##@ Frontend

# Name of the WebAssembly bundle; the generated loader page fetches exactly
# this file name.
WASM_NAME = bundle

# Reproducible, stripped builds. "fyne package --release" already applies the
# same two to the wasm bundle itself, so only the server needs them spelled
# out here.
GO_BUILD_FLAGS = -trimpath -ldflags="-s -w"

# Where "fyne package" writes its output: the loader page, its light and dark
# stylesheets, the spinners, wasm_exec.js and the bundle itself. Everything in there is
# generated, and gitignored.
WASM_DIR = web-ui/wasm

.PHONY: web-ui
web-ui: check-go web-ui-clean ## Build the Fyne WebAssembly frontend into web-ui/wasm/
	cd web-ui && go tool fyne package -os wasm --name $(WASM_NAME) --release

.PHONY: web-ui-clean
web-ui-clean: ## Remove the built frontend bundle
	rm -rf $(WASM_DIR)
	mkdir -p $(WASM_DIR)

##@ Backend

.PHONY: run
run: ## Build image from sources and run via docker-compose
	docker compose -f docker-compose.yml -f docker-compose.build.yml up --build

.PHONY: build
build: web-ui ## Build the frontend bundle and the server binary that serves it
	go build $(GO_BUILD_FLAGS) -o server.bin .

.PHONY: build-server
build-server: check-go ## Build only the server; it serves the bundle already in web-ui/wasm
	go build $(GO_BUILD_FLAGS) -o server.bin .

.PHONY: exec
exec: ## Execute a command inside the container
	docker compose exec app sh

.PHONY: e2e
e2e: ## Run the Playwright browser tests against a running instance (see e2e/README.md)
	cd e2e && npm install --no-audit --no-fund && npx playwright test

##@ Backend utilities

.PHONY: check-go
check-go: ## Check Go version
	@go version | grep -q 'go1\.26' || (echo "Please use Go 1.26.X"; exit 1)

.PHONY: vet
vet: check-go ## Run go vet over both modules
	go vet ./...
	cd web-ui && GOOS=js GOARCH=wasm go vet ./...

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


