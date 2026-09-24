COMPOSE := docker compose -f docker-compose-test.yml

# Settings the integration tests read. They mirror the variable names services
# use in production, pointed at the local test stack.
export DATABASE_HOST     ?= 127.0.0.1
export DATABASE_PORT     ?= 13306
export DATABASE_USERNAME ?= root
export DATABASE_PASSWORD ?= secret
export DATABASE_NAME     ?= goutils_test

export REDIS_HOST ?= 127.0.0.1
export REDIS_PORT ?= 16380

export GLOBAL_REDIS_HOST ?= 127.0.0.1
export GLOBAL_REDIS_PORT ?= 16380
export GLOBAL_REDIS_DATABASE_NUMBER ?= 2

export RABBITMQ_HOST ?= 127.0.0.1
export RABBITMQ_PORT ?= 15674
export RABBITMQ_USER ?= guest
export RABBITMQ_PASS ?= guest
export RABBITMQ_VHOST ?=
export RABBITMQ_CONTAINER ?= goutils-test-rabbitmq

export MQTT_HOST ?= 127.0.0.1
export MQTT_PORT ?= 11883
export MQTT_CONTAINER ?= goutils-test-emqx

.PHONY: check test test-race cover up down wait test-integration test-integration-race test-all

# check is the gate the tag-and-release workflow runs before cutting a tag.
# The gofmt check covers connection/ only: several root files predate it and are
# already unformatted, and reformatting them here would bury this change.
check:
	gofmt -l connection | (! grep .) || (echo "gofmt needed under connection/"; exit 1)
	go build ./...
	go vet ./...

# test runs everything that needs no broker or database.
test:
	go test ./connection/...

# test-race covers the unit tests only. The races worth finding live in the
# reconnect path, where the supervisor goroutine swaps the connection while
# Channel and Publish read it — and that path is only reached by the
# integration tests, so test-integration-race is the one that matters.
test-race:
	go test -race ./connection/...

cover:
	go test -coverprofile=coverage.out ./connection/...
	go tool cover -func=coverage.out | tail -1

up:
	$(COMPOSE) up -d

down:
	$(COMPOSE) down -v

# wait blocks until every service in the stack accepts connections. RabbitMQ and
# EMQX are polled on the port rather than through their CLIs, which need an
# Erlang cookie the host does not share.
wait:
	@echo "waiting for mysql..."
	@until docker exec goutils-test-mysql mysql -uroot -psecret -e "SELECT 1" >/dev/null 2>&1; do sleep 2; done
	@echo "waiting for redis..."
	@until docker exec goutils-test-redis redis-cli ping >/dev/null 2>&1; do sleep 1; done
	@echo "waiting for rabbitmq..."
	@until nc -z $(RABBITMQ_HOST) $(RABBITMQ_PORT) >/dev/null 2>&1; do sleep 2; done
	@echo "waiting for emqx..."
	@until nc -z $(MQTT_HOST) $(MQTT_PORT) >/dev/null 2>&1; do sleep 2; done
	@sleep 5
	@echo "stack ready"

# test-integration exercises the packages against the live stack, including the
# broker-restart reconnect test.
test-integration:
	go test -tags integration -count=1 -timeout 10m ./connection/... -v

# test-integration-race runs the same suite under the race detector, which is
# where a race between the supervisor's redial and a concurrent Channel call
# would surface.
test-integration-race:
	go test -race -tags integration -count=1 -timeout 15m ./connection/... -v

# test-all is the full cycle: bring the stack up, test under the race detector,
# tear it down.
test-all: up wait
	@$(MAKE) test
	@$(MAKE) test-integration-race; status=$$?; $(MAKE) down; exit $$status
