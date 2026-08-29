GO ?= go

.PHONY: all build test dogfood

all: build

build:
	@$(GO) build ./...

test:
	@$(GO) test -v ./...

dogfood:
	@$(GO) build -o /tmp/g8s-dogfood ./cmd/g8s
	@echo "# Dogfood Payload" > /tmp/g8s-dogfood-payload.md
	@echo "- [x] Dogfood DoD" > /tmp/g8s-dogfood-dod.md
	@BRIEF_ID=$$(G8S_DB=/tmp/g8s-dogfood.db /tmp/g8s-dogfood brief-issue --title "make-dogfood" --payload-file /tmp/g8s-dogfood-payload.md --dod-file /tmp/g8s-dogfood-dod.md --issued-by make --ttl 5m | jq -r '.id'); \
	G8S_DB=/tmp/g8s-dogfood.db /tmp/g8s-dogfood brief-consume --id "$$BRIEF_ID"; \
	rm -f /tmp/g8s-dogfood-payload.md /tmp/g8s-dogfood-dod.md /tmp/g8s-dogfood.db /tmp/g8s-dogfood.db-wal /tmp/g8s-dogfood.db-shm /tmp/g8s-dogfood
