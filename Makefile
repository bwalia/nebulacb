.PHONY: build run test clean docker helm ui package package-deb package-rpm install-local uninstall-local

# Go
build:
	go build -o bin/nebulacb ./cmd/nebulacb
	go build -o bin/nebulacb-cli ./cmd/cli

run: build
	./bin/nebulacb --config config.json

test:
	go test ./...

clean:
	rm -rf bin/ reports/ web/nebulacb-ui/build/

# UI
ui:
	cd web/nebulacb-ui && npm install && npm run build

ui-dev:
	cd web/nebulacb-ui && npm start

# Docker
docker:
	docker build -t nebulacb:latest .

docker-up:
	docker-compose up -d

docker-down:
	docker-compose down

# Helm
helm-install:
	helm install nebulacb deploy/helm/nebulacb -n nebulacb --create-namespace

helm-upgrade:
	helm upgrade nebulacb deploy/helm/nebulacb -n nebulacb

helm-uninstall:
	helm uninstall nebulacb -n nebulacb

# CLI shortcuts
start-load:
	./bin/nebulacb-cli start-load

stop-load:
	./bin/nebulacb-cli stop-load

start-upgrade:
	./bin/nebulacb-cli start-upgrade

status:
	./bin/nebulacb-cli status

audit:
	./bin/nebulacb-cli run-audit

report:
	./bin/nebulacb-cli report

# ============================================================
# System packaging (deb / rpm) and local install
# ============================================================
NFPM ?= $(shell command -v nfpm 2>/dev/null)
DIST_DIR := dist

$(DIST_DIR):
	mkdir -p $(DIST_DIR)

package: package-deb package-rpm

package-deb: build ui $(DIST_DIR)
	@if [ -z "$(NFPM)" ]; then echo "nfpm not found — install via 'go install github.com/goreleaser/nfpm/v2/cmd/nfpm@latest'"; exit 1; fi
	cd deploy/packaging && $(NFPM) pkg --packager deb --target ../../$(DIST_DIR)/

package-rpm: build ui $(DIST_DIR)
	@if [ -z "$(NFPM)" ]; then echo "nfpm not found — install via 'go install github.com/goreleaser/nfpm/v2/cmd/nfpm@latest'"; exit 1; fi
	cd deploy/packaging && $(NFPM) pkg --packager rpm --target ../../$(DIST_DIR)/

# Local install via shell script (not via package manager)
install-local: build ui
	sudo SOURCE_CONFIG=$(SOURCE_CONFIG) deploy/install/install.sh

uninstall-local:
	sudo deploy/install/uninstall.sh $(ARGS)
