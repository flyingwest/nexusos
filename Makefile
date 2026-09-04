.PHONY: build run test tidy clean install coordinator nexusctl e2e image image-devsmoke qemu-smoke qemu-e2e test-provision

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

test-provision:
	./scripts/test-nexusos-provision.sh

e2e: build
	./scripts/e2e-two-node-mock.sh

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
