.PHONY: build run test clean docker helm ui

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
