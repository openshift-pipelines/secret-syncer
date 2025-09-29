# Image URL to use all building/pushing image targets
IMG ?=
REGISTRY ?=

# Get the currently used golang install path (in GOPATH/bin, unless GOBIN is set)
ifeq (,$(shell go env GOBIN))
GOBIN=$(shell go env GOPATH)/bin
else
GOBIN=$(shell go env GOBIN)
endif

.PHONY: help
help: ## Display this help.
	@awk 'BEGIN {FS = ":.*##"; printf "\nUsage:\n  make \033[36m<target>\033[0m\n"} /^[a-zA-Z_0-9-]+:.*?##/ { printf "  \033[36m%-15s\033[0m %s\n", $$1, $$2 } /^##@/ { printf "\n\033[1m%s\033[0m\n", substr($$0, 5) } ' $(MAKEFILE_LIST)

##@ Development

.PHONY: fmt
fmt: ## Run go fmt against code.
	go fmt ./...

.PHONY: vet
vet: ## Run go vet against code.
	go vet ./...

.PHONY: test
test: fmt vet ## Run tests.
	go test ./... -coverprofile cover.out

.PHONY: build
build: fmt vet ## Build binary.
	go build -o bin/secret-service ./cmd/controller

.PHONY: run
run: fmt vet ## Run locally.
	go run ./cmd/secret-service

.PHONY: tidy
tidy: ## Run go mod tidy.
	go mod tidy

.PHONY: vendor
vendor: tidy ## Run go mod vendor.
	go mod vendor

##@ Build

.PHONY: docker-build
docker-build: ## Build docker image.
	docker build --no-cache -t ${IMG} .

.PHONY: docker-push
docker-push: ## Push docker image.
	docker push ${IMG}

.PHONY: docker-buildx
docker-buildx: ## Build and push docker image for multiple platforms.
	docker buildx create --use --name=crossplat --node=crossplat && \
	docker buildx build \
		--platform linux/amd64,linux/arm64 \
		--output "type=registry" \
		--tag ${IMG} .

##@ Deployment

.PHONY: deploy
deploy: ## Deploy to the K8s cluster specified in ~/.kube/config.
	kubectl apply -f config/namespace.yaml
	kubectl apply -f config/rbac.yaml
	kubectl apply -f config/deployment.yaml

.PHONY: undeploy
undeploy: ## Undeploy from the K8s cluster specified in ~/.kube/config.
	kubectl delete -f config/deployment.yaml --ignore-not-found=true
	kubectl delete -f config/rbac.yaml --ignore-not-found=true
	kubectl delete -f config/namespace.yaml --ignore-not-found=true

.PHONY: redeploy
redeploy: ## Restart deployment to pull latest image.
	kubectl rollout restart deployment/workload-controller -n syncer-service
	kubectl rollout status deployment/workload-controller -n syncer-service

##@ Utilities

.PHONY: logs
logs: ## Show controller logs.
	kubectl logs -n syncer-service -l app=workload-controller -f

.PHONY: status
status: ## Show controller status.
	kubectl get deployment workload-controller -n syncer-service
	kubectl get pods -n syncer-service -l app=workload-controller

.PHONY: clean
clean: ## Clean build artifacts.
	rm -f bin/workload-controller
	rm -f cover.out
	rm -rf vendor/

##@ Complete workflow

.PHONY: all
all: docker-build docker-push deploy

.PHONY: quick-deploy
quick-deploy: build docker-build deploy ## Quick local build and deploy (for development).

.PHONY: update
update: docker-build docker-push redeploy ## Build, push, and redeploy with new image.
