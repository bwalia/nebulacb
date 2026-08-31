.PHONY: help install demo test lint typecheck eval eval-save baseline serve ingest clean docker dashboard

# Prefer the active interpreter (conda/venv `python`) over a bare Homebrew `python3`.
PY ?= $(shell command -v python 2>/dev/null || command -v python3)
export PYTHONPATH := src:.

help:
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | awk 'BEGIN {FS = ":.*?## "}; {printf "  \033[36m%-14s\033[0m %s\n", $$1, $$2}'

install: ## install the package and dev dependencies
	$(PY) -m pip install -e ".[dev]"

demo: ## walk all three agents end to end, offline, in a few seconds
	@$(PY) -c "import pydantic_settings" 2>/dev/null || { echo "Missing deps for $$($(PY) -c 'import sys; print(sys.executable)'). Run: make install"; exit 1; }
	$(PY) scripts/demo.py

test: ## run the unit and integration tests
	$(PY) -m pytest -q

lint: ## style and static checks
	ruff check src agents tests scripts

typecheck: ## optional static typing pass
	mypy src/aivc

eval: ## run every eval suite and fail on a breached gate
	$(PY) -m aivc.cli eval all --repeats 3

eval-save: ## run evals and write reports to reports/
	$(PY) -m aivc.cli eval all --repeats 3 --save reports

baseline: ## record the current results as the regression baseline
	$(PY) -m aivc.cli eval all --repeats 3 --save baselines

ingest: ## rebuild the retrieval index and print the manifest
	$(PY) -m aivc.cli ingest

dashboard: ## build the React workflow console into src/aivc/static/dashboard
	cd dashboard && npm install && npm run build

serve: ## run the HTTP API + dashboard on :8000
	$(PY) -m aivc.cli serve --reload

docker: ## build the container image
	docker build -t aivc-agents:local .

clean: ## remove local state and caches
	rm -rf .state reports .pytest_cache **/__pycache__
