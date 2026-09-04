.PHONY: build run test tidy clean install coordinator nexusctl e2e

COORD_DIR := coordination

build: coordinator nexusctl

coordinator:
	cd $(COORD_DIR) && go build -o ../bin/coordinator ./cmd/coordinator

nexusctl:
	cd $(COORD_DIR) && go build -o ../bin/nexusctl ./cmd/nexusctl

# Local smoke: --dev auto-generates self-signed TLS and relaxes empty-token rejection.
run: coordinator
	./bin/coordinator --dev --mock --listen :8080

test:
	cd $(COORD_DIR) && go test ./...

e2e: build
	./scripts/e2e-two-node-mock.sh

tidy:
	cd $(COORD_DIR) && go mod tidy

clean:
	rm -rf bin/

install: build
	./scripts/install.sh
