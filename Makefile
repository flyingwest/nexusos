.PHONY: build run test tidy clean install coordinator nexusctl e2e e2e-workload e2e-workload-multinode e2e-migration image image-devsmoke qemu-smoke qemu-e2e test-provision ui

COORD_DIR := coordination

build: coordinator nexusctl

# Operator UI is embedded from coordination/internal/api/httpapi/ui (repo-root ui/ symlink).
ui:
	@test -f coordination/internal/api/httpapi/ui/index.html
	@echo "operator UI assets OK (embedded at build via go:embed)"

coordinator: ui
	cd $(COORD_DIR) && go build -o ../bin/coordinator ./cmd/coordinator

nexusctl:
	cd $(COORD_DIR) && go build -o ../bin/nexusctl ./cmd/nexusctl

# Local smoke: --dev auto-generates self-signed TLS and relaxes empty-token rejection.
run: coordinator
	./bin/coordinator --dev --mock --listen :8080

test:
	cd $(COORD_DIR) && go test ./...

test-provision:
	./scripts/test-nexusos-provision.sh

e2e: build
	./scripts/e2e-two-node-mock.sh

e2e-workload: build
	./scripts/e2e-workload-mock.sh

e2e-workload-multinode: build
	./scripts/e2e-workload-multinode.sh

e2e-migration: build
	./scripts/e2e-migration-multinode.sh

tidy:
	cd $(COORD_DIR) && go mod tidy

clean:
	rm -rf bin/

install: build
	./scripts/install.sh

# Node image (requires root + mmdebstrap; see docs/node-image.md)
image:
	sudo ./image/build.sh

image-devsmoke:
	sudo ./image/build.sh --dev-smoke

qemu-smoke:
	./scripts/qemu-node-smoke.sh

qemu-e2e:
	./scripts/qemu-two-node-e2e.sh
