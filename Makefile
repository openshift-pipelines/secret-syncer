# Image URL to use all building/pushing image targets
IMG ?= zakisk/secret-controller:latest
REGISTRY ?= zakisk

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
	go build -o bin/secret-controller main.go

.PHONY: run
run: fmt vet ## Run locally.
	go run ./main.go

##@ Build

.PHONY: docker-build
docker-build: ## Build docker image.
	docker build -t ${IMG} .

.PHONY: docker-push
docker-push: ## Push docker image.
	docker push ${IMG}

.PHONY: docker-buildx
docker-buildx: ## Build and push docker image for multiple platforms.
	docker buildx create --use --name=crossplat --node=crossplat && \
	docker buildx build \
		--platform linux/amd64,linux/arm64 \
		--output "type=registry" \
		--tag ${REGISTRY}/${IMG} .

##@ Deployment

.PHONY: deploy
deploy: ## Deploy to the K8s cluster specified in ~/.kube/config.
	kubectl apply -f deploy/rbac.yaml
	kubectl apply -f deploy/deployment.yaml

.PHONY: undeploy
undeploy: ## Undeploy from the K8s cluster specified in ~/.kube/config.
	kubectl delete -f deploy/deployment.yaml --ignore-not-found=true
	kubectl delete -f deploy/rbac.yaml --ignore-not-found=true

.PHONY: install-samples
install-samples: ## Install sample MultiKueue CRDs and secrets.
	kubectl apply -f examples/multikueue-setup.yaml
	kubectl apply -f examples/sample-secret.yaml

.PHONY: uninstall-samples
uninstall-samples: ## Uninstall sample CRDs and secrets.
	kubectl delete -f examples/sample-secret.yaml --ignore-not-found=true
	kubectl delete -f examples/multikueue-setup.yaml --ignore-not-found=true

##@ Utilities

.PHONY: logs
logs: ## Show controller logs.
	kubectl logs -n kueue-system -l app=secret-controller -f

.PHONY: status
status: ## Show controller status.
	kubectl get deployment secret-controller -n kueue-system
	kubectl get pods -n kueue-system -l app=secret-controller

.PHONY: clean
clean: ## Clean build artifacts.
	rm -f bin/secret-controller
	rm -f cover.out

##@ Complete workflow

.PHONY: all
all: docker-build docker-push deploy

.PHONY: quick-deploy
quick-deploy: build docker-build deploy ## Quick local build and deploy (for development).
