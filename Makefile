GO ?= go

.PHONY: all build test dogfood clean-branches clean-worktrees spawn-worktree verify-cross-platform

all: build

build:
	@$(GO) build ./...

test:
	@$(GO) test -v ./...

verify-cross-platform:
	GOOS=linux GOARCH=amd64 $(GO) build ./...
	GOOS=darwin GOARCH=arm64 $(GO) build ./...
	GOOS=windows GOARCH=amd64 $(GO) build ./...

clean-branches:
	@bash tools/clean_branches.sh

clean-worktrees:
	@bash tools/cleanup_worktrees.sh

spawn-worktree:
	@bash tools/spawn_worktree.sh $(BRANCH)

dogfood:
	@$(GO) build -o /tmp/g8s-dogfood ./cmd/g8s
	@echo "# Dogfood Payload" > /tmp/g8s-dogfood-payload.md
	@echo "- [x] Dogfood DoD" > /tmp/g8s-dogfood-dod.md
	@printf "# Dogfood Orchestrate Brief\n## Context\nTesting brief orchestration\n## DoD\n- [x] DoD verified\n" > /tmp/g8s-dogfood-brief.md
	@BRIEF_ID=$$(G8S_DB=/tmp/g8s-dogfood.db /tmp/g8s-dogfood brief-issue --title "make-dogfood" --payload-file /tmp/g8s-dogfood-payload.md --dod-file /tmp/g8s-dogfood-dod.md --issued-by make --ttl 5m | jq -r '.data.id // .id // empty'); \
	G8S_DB=/tmp/g8s-dogfood.db /tmp/g8s-dogfood brief-consume --id "$$BRIEF_ID"; \
	ORCH_BRIEF_ID=$$(G8S_DB=/tmp/g8s-dogfood.db /tmp/g8s-dogfood orchestrate --brief-file /tmp/g8s-dogfood-brief.md --issued-by make --ttl 5m); \
	DISPATCH_ID=$$(G8S_DB=/tmp/g8s-dogfood.db /tmp/g8s-dogfood orchestrate --dispatch "$$ORCH_BRIEF_ID" --ttl 5m); \
	G8S_DB=/tmp/g8s-dogfood.db /tmp/g8s-dogfood brief-consume --id "$$DISPATCH_ID"; \
	/tmp/g8s-dogfood cleanup-worktrees --older-than 1h; \
	rm -f /tmp/g8s-dogfood-payload.md /tmp/g8s-dogfood-dod.md /tmp/g8s-dogfood-brief.md /tmp/g8s-dogfood.db /tmp/g8s-dogfood.db-wal /tmp/g8s-dogfood.db-shm /tmp/g8s-dogfood
